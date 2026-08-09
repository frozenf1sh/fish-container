package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	schema2ManifestMediaType = "application/vnd.docker.distribution.manifest.v2+json"
	ociManifestMediaType     = "application/vnd.oci.image.manifest.v1+json"
	dockerManifestListType   = "application/vnd.docker.distribution.manifest.list.v2+json"
	ociImageIndexType        = "application/vnd.oci.image.index.v1+json"
	dockerHubAuthEndpoint    = "https://auth.docker.io/token"
	dockerHubService         = "registry.docker.io"
)

type manifestPuller struct {
	layout Layout
	cfg    Config
}

func NewManifestPuller(cfg Config) ManifestPuller {
	return &manifestPuller{
		layout: NewLayout(cfg.DataRoot),
		cfg:    cfg,
	}
}

func (p *manifestPuller) PullManifest(ctx context.Context, reference string) (*ManifestResult, error) {
	ref, err := ParseReference(reference)
	if err != nil {
		return nil, err
	}

	body, digest, mediaType, err := p.fetchManifest(ctx, ref)
	if err != nil {
		return nil, err
	}

	if mediaType == dockerManifestListType || mediaType == ociImageIndexType {
		nextDigest, err := pickManifestForPlatform(body, p.cfg.Platform)
		if err != nil {
			return nil, err
		}
		body, digest, mediaType, err = p.fetchManifestTarget(ctx, ref, nextDigest)
		if err != nil {
			return nil, err
		}
	}
	if mediaType != "" && mediaType != schema2ManifestMediaType && mediaType != ociManifestMediaType {
		return nil, fmt.Errorf("unsupported manifest mediaType: %s", mediaType)
	}

	var manifest Schema2Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("unsupported schemaVersion: %d", manifest.SchemaVersion)
	}

	configDigestHex, err := digestHexFromSHA256(manifest.Config.Digest)
	if err != nil {
		return nil, fmt.Errorf("invalid config digest: %w", err)
	}

	configPath, imageConfig, err := p.pullImageConfig(ctx, ref, manifest.Config, configDigestHex)
	if err != nil {
		return nil, err
	}
	if err := ValidateImagePlatform(p.cfg.Platform, imageConfig); err != nil {
		return nil, err
	}

	manifestPath := p.layout.ManifestPath(ref.Registry, ref.Repository, ref.Tag)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return nil, fmt.Errorf("create manifest dir: %w", err)
	}
	if err := os.WriteFile(manifestPath, body, 0o644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	refPath := p.layout.RefPath(ref.Registry, ref.Repository, ref.Tag)
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		return nil, fmt.Errorf("create ref dir: %w", err)
	}
	if err := os.WriteFile(refPath, []byte(digest+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write ref: %w", err)
	}

	return &ManifestResult{
		Reference:    ref,
		Manifest:     manifest,
		Digest:       digest,
		ManifestPath: manifestPath,
		RefPath:      refPath,
		ConfigDigest: manifest.Config.Digest,
		ConfigPath:   configPath,
		Config:       imageConfig,
	}, nil
}

func (p *manifestPuller) pullImageConfig(ctx context.Context, ref Reference, descriptor Descriptor, digestHex string) (string, ImageConfig, error) {
	fetcher := NewBlobFetcher(p.cfg)
	blobPath, err := fetcher.FetchBlob(ctx, ref, descriptor, nil)
	if err != nil {
		return "", ImageConfig{}, fmt.Errorf("fetch image config blob: %w", err)
	}

	body, err := os.ReadFile(blobPath)
	if err != nil {
		return "", ImageConfig{}, fmt.Errorf("read image config blob: %w", err)
	}

	var cfg ImageConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", ImageConfig{}, fmt.Errorf("decode image config: %w", err)
	}

	configPath := p.layout.ConfigPath(digestHex)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return "", ImageConfig{}, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(configPath, body, 0o644); err != nil {
		return "", ImageConfig{}, fmt.Errorf("write config index: %w", err)
	}

	return configPath, cfg, nil
}

func (p *manifestPuller) fetchManifest(ctx context.Context, ref Reference) ([]byte, string, string, error) {
	return p.fetchManifestTarget(ctx, ref, ref.Tag)
}

func (p *manifestPuller) fetchManifestTarget(ctx context.Context, ref Reference, target string) ([]byte, string, string, error) {
	base := fmt.Sprintf("https://%s", ref.Registry)
	if p.cfg.RegistryMirror != "" && ref.Registry == defaultDockerRegistry {
		base = strings.TrimRight(p.cfg.RegistryMirror, "/")
	}

	manifestURL := fmt.Sprintf("%s/v2/%s/manifests/%s", strings.TrimRight(base, "/"), ref.Repository, target)

	token := ""
	if ref.Registry == defaultDockerRegistry {
		token, _ = p.fetchDockerHubToken(ctx, ref.Repository)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		body, digest, mediaType, err := p.doFetchManifest(ctx, manifestURL, token)
		if err == nil {
			return body, digest, mediaType, nil
		}
		lastErr = err
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 400 * time.Millisecond)
		}
	}

	return nil, "", "", fmt.Errorf("fetch manifest failed after retries: %w", lastErr)
}

func (p *manifestPuller) doFetchManifest(ctx context.Context, manifestURL, token string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", schema2ManifestMediaType+", "+ociManifestMediaType+", "+dockerManifestListType+", "+ociImageIndexType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("request manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", "", fmt.Errorf("manifest response status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", fmt.Errorf("read manifest response: %w", err)
	}

	digest := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest"))
	if digest == "" {
		hash := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(hash[:])
	}

	mediaType := normalizeContentType(resp.Header.Get("Content-Type"))

	return body, digest, mediaType, nil
}

func normalizeContentType(contentType string) string {
	parts := strings.Split(contentType, ";")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func pickManifestForPlatform(body []byte, target Platform) (string, error) {
	if err := validateTargetPlatform(target); err != nil {
		return "", err
	}

	var index struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
				Variant      string `json:"variant,omitempty"`
			} `json:"platform"`
		} `json:"manifests"`
	}

	if err := json.Unmarshal(body, &index); err != nil {
		return "", fmt.Errorf("decode manifest list: %w", err)
	}

	for _, item := range index.Manifests {
		if item.Platform.OS == target.OS &&
			item.Platform.Architecture == target.Architecture &&
			(target.Variant == "" || item.Platform.Variant == target.Variant) {
			return item.Digest, nil
		}
	}

	return "", fmt.Errorf("no %s manifest found in index", target.String())
}

// ValidateImagePlatform rejects image configs that cannot execute on the target host.
func ValidateImagePlatform(target Platform, cfg ImageConfig) error {
	if err := validateTargetPlatform(target); err != nil {
		return err
	}
	if cfg.OS != "" && cfg.OS != target.OS {
		return fmt.Errorf("image OS %s does not match target %s", cfg.OS, target.OS)
	}
	if cfg.Architecture != "" && cfg.Architecture != target.Architecture {
		return fmt.Errorf("image architecture %s does not match target %s", cfg.Architecture, target.Architecture)
	}
	return nil
}

func validateTargetPlatform(target Platform) error {
	if target.OS == "" || target.Architecture == "" {
		return fmt.Errorf("target image platform is required")
	}
	if target.OS != "linux" || (target.Architecture != "amd64" && target.Architecture != "arm64") {
		return fmt.Errorf("unsupported target image platform: %s (supported: linux/amd64, linux/arm64)", target.String())
	}
	return nil
}

func (p *manifestPuller) fetchDockerHubToken(ctx context.Context, repository string) (string, error) {
	q := url.Values{}
	q.Set("service", dockerHubService)
	q.Set("scope", fmt.Sprintf("repository:%s:pull", repository))

	endpoint := dockerHubAuthEndpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token response status=%d", resp.StatusCode)
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token == "" {
		return "", fmt.Errorf("empty token")
	}

	return payload.Token, nil
}

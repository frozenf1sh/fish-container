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

type blobFetcher struct {
	layout Layout
	cfg    Config
}

func NewBlobFetcher(cfg Config) BlobFetcher {
	return &blobFetcher{
		layout: NewLayout(cfg.DataRoot),
		cfg:    cfg,
	}
}

func (f *blobFetcher) FetchBlob(ctx context.Context, ref Reference, descriptor Descriptor) (string, error) {
	digestHex, err := digestHexFromSHA256(descriptor.Digest)
	if err != nil {
		return "", err
	}

	blobPath := f.layout.BlobPath(digestHex)
	if _, err := os.Stat(blobPath); err == nil {
		return blobPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat blob path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		return "", fmt.Errorf("create blob dir: %w", err)
	}

	base := fmt.Sprintf("https://%s", ref.Registry)
	if f.cfg.RegistryMirror != "" && ref.Registry == defaultDockerRegistry {
		base = strings.TrimRight(f.cfg.RegistryMirror, "/")
	}
	blobURL := fmt.Sprintf("%s/v2/%s/blobs/%s", strings.TrimRight(base, "/"), ref.Repository, descriptor.Digest)

	token := ""
	if ref.Registry == defaultDockerRegistry {
		token, _ = f.fetchDockerHubToken(ctx, ref.Repository)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := f.downloadBlob(ctx, blobURL, token, blobPath, digestHex); err == nil {
			return blobPath, nil
		} else {
			lastErr = err
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}

	return "", fmt.Errorf("fetch blob failed after retries: %w", lastErr)
}

func (f *blobFetcher) downloadBlob(ctx context.Context, blobURL, token, blobPath, expectedHex string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return fmt.Errorf("create blob request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := f.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("blob response status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(blobPath), ".blob-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp blob file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body); err != nil {
		return fmt.Errorf("write blob content: %w", err)
	}

	actualHex := hex.EncodeToString(hasher.Sum(nil))
	if actualHex != expectedHex {
		return fmt.Errorf("blob sha256 mismatch expected=%s got=%s", expectedHex, actualHex)
	}

	if err := tmpFile.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod blob temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close blob temp file: %w", err)
	}

	if err := os.Rename(tmpPath, blobPath); err != nil {
		if _, statErr := os.Stat(blobPath); statErr == nil {
			return nil
		}
		return fmt.Errorf("move blob file: %w", err)
	}

	return nil
}

func (f *blobFetcher) fetchDockerHubToken(ctx context.Context, repository string) (string, error) {
	q := url.Values{}
	q.Set("service", dockerHubService)
	q.Set("scope", fmt.Sprintf("repository:%s:pull", repository))

	endpoint := dockerHubAuthEndpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := f.cfg.HTTPClient.Do(req)
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

func digestHexFromSHA256(digest string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(digest), ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return "", fmt.Errorf("unsupported digest format: %s", digest)
	}
	if len(parts[1]) != 64 {
		return "", fmt.Errorf("invalid sha256 digest length: %s", digest)
	}

	return parts[1], nil
}

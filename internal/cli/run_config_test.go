package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fish-container/internal/image"
	"fish-container/internal/store"
)

func TestResolveContainerConfigCommandOverride(t *testing.T) {
	t.Parallel()

	base := store.ContainerConfig{
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{"echo default"},
		Env:        []string{"A=1", "B=2"},
		WorkingDir: "/",
		User:       "root",
	}

	resolved := resolveContainerConfig(base, []string{"echo override"}, []string{"B=3", "C=4"}, "/work", "1000")

	if len(resolved.Entrypoint) != 2 {
		t.Fatalf("entrypoint changed unexpectedly: %v", resolved.Entrypoint)
	}
	if len(resolved.Cmd) != 1 || resolved.Cmd[0] != "echo override" {
		t.Fatalf("cmd override not applied: %v", resolved.Cmd)
	}
	if resolved.WorkingDir != "/work" {
		t.Fatalf("working dir override not applied: %s", resolved.WorkingDir)
	}
	if resolved.User != "1000" {
		t.Fatalf("user override not applied: %s", resolved.User)
	}

	env := map[string]string{}
	for _, item := range resolved.Env {
		k, v, ok := splitEnv(item)
		if ok {
			env[k] = v
		}
	}
	if env["A"] != "1" || env["B"] != "3" || env["C"] != "4" {
		t.Fatalf("unexpected env merge result: %v", resolved.Env)
	}
}

func TestLoadOrPullManifestRejectsCachedPlatformMismatch(t *testing.T) {
	t.Parallel()

	dataRoot := t.TempDir()
	cfg := image.LoadConfigFromEnv(dataRoot)
	cfg.Platform = image.Platform{OS: "linux", Architecture: "arm64"}
	ref, err := image.ParseReference("alpine:test")
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	layout := image.NewLayout(dataRoot)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	manifest := image.Schema2Manifest{SchemaVersion: 2, Config: image.Descriptor{Digest: digest}}
	manifestBody, _ := json.Marshal(manifest)
	manifestPath := layout.ManifestPath(ref.Registry, ref.Repository, ref.Tag)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBody, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	imageCfg := image.ImageConfig{OS: "linux", Architecture: "amd64"}
	configBody, _ := json.Marshal(imageCfg)
	configPath := layout.ConfigPath(strings.TrimPrefix(digest, "sha256:"))
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = loadOrPullManifest(context.Background(), cfg, "alpine:test")
	if err == nil || !strings.Contains(err.Error(), "cached image platform mismatch") {
		t.Fatalf("expected cached platform mismatch, got %v", err)
	}
}

func TestMergeEnvIgnoresInvalidItems(t *testing.T) {
	t.Parallel()

	got := mergeEnv([]string{"A=1", "INVALID"}, []string{"A=2", "C=3", "NOPE"})
	if len(got) != 2 {
		t.Fatalf("unexpected env count: %d (%v)", len(got), got)
	}
	if got[0] != "A=2" || got[1] != "C=3" {
		t.Fatalf("unexpected merged env: %v", got)
	}
}

func splitEnv(item string) (string, string, bool) {
	for i := 0; i < len(item); i++ {
		if item[i] == '=' {
			return item[:i], item[i+1:], i > 0
		}
	}
	return "", "", false
}

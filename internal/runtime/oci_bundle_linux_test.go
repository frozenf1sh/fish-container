//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fish-container/internal/store"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestBuildOCIBundleWritesSpecAndRootfsLink(t *testing.T) {
	t.Parallel()

	dataRoot := t.TempDir()
	rootfs := filepath.Join(dataRoot, "rootfs-src")
	if err := os.MkdirAll(rootfs, 0o755); err != nil {
		t.Fatalf("mkdir rootfs: %v", err)
	}

	cfg := store.ContainerConfig{
		ID:         "demo",
		Rootfs:     rootfs,
		ImageRef:   "docker.io/library/alpine:latest",
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{"echo ok"},
		Env:        []string{"A=1"},
		WorkingDir: "/",
		Hostname:   "demo-host",
		User:       "1000:1001",
	}

	result, err := BuildOCIBundle(context.Background(), dataRoot, cfg)
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}

	if result.Spec.Version != specs.Version {
		t.Fatalf("unexpected oci version: %s", result.Spec.Version)
	}
	if result.Spec.Root == nil || result.Spec.Root.Path != "rootfs" {
		t.Fatalf("unexpected root config: %+v", result.Spec.Root)
	}
	if len(result.Spec.Process.Args) == 0 || result.Spec.Process.Args[0] != "/bin/sh" {
		t.Fatalf("unexpected process args: %v", result.Spec.Process.Args)
	}

	if _, err := os.Stat(result.SpecPath); err != nil {
		t.Fatalf("stat spec path: %v", err)
	}

	linked, err := os.Readlink(result.RootfsPath)
	if err != nil {
		t.Fatalf("read rootfs link: %v", err)
	}
	if linked != rootfs {
		t.Fatalf("unexpected rootfs link target: %s", linked)
	}

	body, err := os.ReadFile(result.SpecPath)
	if err != nil {
		t.Fatalf("read spec file: %v", err)
	}
	var specOnDisk specs.Spec
	if err := json.Unmarshal(body, &specOnDisk); err != nil {
		t.Fatalf("decode spec file: %v", err)
	}
	if specOnDisk.Process == nil || specOnDisk.Process.User.UID != 1000 || specOnDisk.Process.User.GID != 1001 {
		t.Fatalf("unexpected process user: %+v", specOnDisk.Process)
	}
}

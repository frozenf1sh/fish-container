//go:build linux

package cgroups

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerApplyDeleteEnabled(t *testing.T) {
	root := t.TempDir()
	controllers := filepath.Join(root, "cgroup.controllers")
	if err := os.WriteFile(controllers, []byte("cpu memory pids\n"), 0o644); err != nil {
		t.Fatalf("write cgroup.controllers: %v", err)
	}

	t.Setenv(envEnableCgroupV2, "1")
	t.Setenv(envCgroupV2Root, root)
	t.Setenv(envCgroupV2Prefix, "fish-test")

	mgr := NewManager()
	if err := mgr.Apply(context.Background(), "demo"); err != nil {
		t.Fatalf("apply cgroup: %v", err)
	}

	target := filepath.Join(root, "fish-test", "demo")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("stat cgroup dir: %v", err)
	}

	if err := mgr.Delete(context.Background(), "demo"); err != nil {
		t.Fatalf("delete cgroup: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected cgroup dir removed")
	}
}

func TestResolveOCIPathDisabled(t *testing.T) {
	t.Setenv(envEnableCgroupV2, "0")

	path, enabled, err := ResolveOCIPath("demo")
	if err != nil {
		t.Fatalf("resolve oci path: %v", err)
	}
	if enabled {
		t.Fatalf("expected disabled")
	}
	if path != "" {
		t.Fatalf("expected empty path, got %q", path)
	}
}

func TestResolveOCIPathEnabled(t *testing.T) {
	t.Setenv(envEnableCgroupV2, "true")
	t.Setenv(envCgroupV2Prefix, "team/fc")

	got, enabled, err := ResolveOCIPath("demo")
	if err != nil {
		t.Fatalf("resolve oci path: %v", err)
	}
	if !enabled {
		t.Fatalf("expected enabled")
	}
	if got != "/team/fc/demo" {
		t.Fatalf("unexpected oci path: %s", got)
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestContainerConfigStoreSaveLoad(t *testing.T) {
	t.Parallel()

	dataRoot := t.TempDir()
	store := NewContainerConfigStore(dataRoot)

	want := ContainerConfig{
		ID:         "demo-1",
		Rootfs:     "/tmp/rootfs",
		ImageRef:   "docker.io/library/alpine:latest",
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{"echo hello"},
		Env:        []string{"A=1", "B=2"},
		WorkingDir: "/",
		Hostname:   "fish",
	}

	path, err := store.Save(context.Background(), want)
	if err != nil {
		t.Fatalf("save config: %v", err)
	}

	expectedPath := filepath.Join(dataRoot, "containers", "demo-1", "config.json")
	if path != expectedPath {
		t.Fatalf("unexpected path: got %s want %s", path, expectedPath)
	}

	got, err := store.Load(context.Background(), "demo-1")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got.ID != want.ID {
		t.Fatalf("id mismatch: got %s want %s", got.ID, want.ID)
	}
	if got.Rootfs != want.Rootfs {
		t.Fatalf("rootfs mismatch: got %s want %s", got.Rootfs, want.Rootfs)
	}
	if len(got.Entrypoint) != len(want.Entrypoint) {
		t.Fatalf("entrypoint length mismatch: got %d want %d", len(got.Entrypoint), len(want.Entrypoint))
	}
	if len(got.Cmd) != len(want.Cmd) {
		t.Fatalf("cmd length mismatch: got %d want %d", len(got.Cmd), len(want.Cmd))
	}
	if len(got.Env) != len(want.Env) {
		t.Fatalf("env length mismatch: got %d want %d", len(got.Env), len(want.Env))
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("createdAt should be set")
	}
}

func TestContainerConfigEffectiveCommand(t *testing.T) {
	t.Parallel()

	cfg := ContainerConfig{Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{"echo hi"}}
	cmd := cfg.EffectiveCommand()
	if len(cmd) != 3 {
		t.Fatalf("unexpected command length: %d", len(cmd))
	}

	empty := ContainerConfig{}
	fallback := empty.EffectiveCommand()
	if len(fallback) != 1 || fallback[0] != "/bin/sh" {
		t.Fatalf("unexpected fallback command: %v", fallback)
	}
}

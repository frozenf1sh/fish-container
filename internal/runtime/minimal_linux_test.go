//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathInContainerEnvironment(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "demo")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	got, err := lookPathInEnv("demo", []string{"PATH=" + binDir})
	if err != nil {
		t.Fatalf("look path: %v", err)
	}
	if got != binPath {
		t.Fatalf("unexpected path: %s", got)
	}
	if _, err := lookPathInEnv("demo", nil); err == nil {
		t.Fatal("expected missing PATH error")
	}
}

package cli

import (
	"testing"

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

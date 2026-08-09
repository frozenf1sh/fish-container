package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestFileStateStoreSaveLoadDelete(t *testing.T) {
	t.Parallel()

	store := NewFileStateStore(t.TempDir())
	state := State{
		ID:     "demo",
		Status: StateCreated,
		Bundle: "/tmp/demo-bundle",
	}

	path, err := store.Save(context.Background(), state)
	if err != nil {
		t.Fatalf("save state: %v", err)
	}
	if path == "" {
		t.Fatalf("state path should not be empty")
	}

	loaded, err := store.Load(context.Background(), "demo")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.ID != "demo" || loaded.Status != StateCreated {
		t.Fatalf("unexpected state payload: %+v", loaded)
	}
	if loaded.Version == "" {
		t.Fatalf("version should be populated")
	}

	if err := store.Delete(context.Background(), "demo"); err != nil {
		t.Fatalf("delete state: %v", err)
	}
	_, err = store.Load(context.Background(), "demo")
	if err == nil {
		t.Fatalf("expected load error after delete")
	}
}

func TestFileStateStoreContextCancel(t *testing.T) {
	t.Parallel()

	store := NewFileStateStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Save(ctx, State{ID: "demo", Bundle: "/tmp/b"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got: %v", err)
	}
}

func TestFileStateStoreRejectsUnsafeID(t *testing.T) {
	t.Parallel()

	store := NewFileStateStore(t.TempDir())
	if _, err := store.Load(context.Background(), "../escape"); err == nil {
		t.Fatal("expected unsafe container id to be rejected")
	}
}

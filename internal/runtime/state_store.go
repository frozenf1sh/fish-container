package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"fish-container/internal/image"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// FileStateStore stores OCI runtime state as runtime/<id>/state.json.
type FileStateStore struct {
	layout image.Layout
}

func NewFileStateStore(dataRoot string) *FileStateStore {
	return &FileStateStore{layout: image.NewLayout(dataRoot)}
}

func (s *FileStateStore) Save(ctx context.Context, state State) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	if state.ID == "" {
		return "", fmt.Errorf("state id is required")
	}
	if state.Bundle == "" {
		return "", fmt.Errorf("state bundle is required")
	}
	if state.Version == "" {
		state.Version = specs.Version
	}
	if state.Status == "" {
		state.Status = StateCreating
	}

	path := s.layout.RuntimeStatePath(state.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create runtime state dir: %w", err)
	}

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal runtime state: %w", err)
	}
	body = append(body, '\n')

	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("write runtime state: %w", err)
	}

	return path, nil
}

func (s *FileStateStore) Load(ctx context.Context, id string) (*State, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	path := s.layout.RuntimeStatePath(id)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime state: %w", err)
	}

	var state State
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("decode runtime state: %w", err)
	}

	return &state, nil
}

func (s *FileStateStore) Delete(ctx context.Context, id string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	path := s.layout.RuntimeContainerDir(id)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete runtime state dir: %w", err)
	}
	return nil
}

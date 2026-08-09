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
	if err := ValidateContainerID(state.ID); err != nil {
		return "", err
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

	if err := writeFileAtomic(path, body, 0o644); err != nil {
		return "", fmt.Errorf("write runtime state atomically: %w", err)
	}

	return path, nil
}

func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s *FileStateStore) Load(ctx context.Context, id string) (*State, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if err := ValidateContainerID(id); err != nil {
		return nil, err
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

	if err := ValidateContainerID(id); err != nil {
		return err
	}
	path := s.layout.RuntimeContainerDir(id)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete runtime state dir: %w", err)
	}
	return nil
}

// ValidateContainerID restricts identifiers to a single safe path component.
func ValidateContainerID(id string) error {
	if id == "" {
		return fmt.Errorf("container id is required")
	}
	for index, r := range id {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
		if !allowed || (index == 0 && r == '.') {
			return fmt.Errorf("invalid container id %q", id)
		}
	}
	return nil
}

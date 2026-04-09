package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fish-container/internal/image"
)

// ContainerConfig persists the resolved runtime intent for one container instance.
type ContainerConfig struct {
	ID                  string    `json:"id"`
	ImageRef            string    `json:"imageRef,omitempty"`
	ImageManifestDigest string    `json:"imageManifestDigest,omitempty"`
	ImageConfigDigest   string    `json:"imageConfigDigest,omitempty"`
	CgroupsPath         string    `json:"cgroupsPath,omitempty"`
	Rootfs              string    `json:"rootfs"`
	Entrypoint          []string  `json:"entrypoint,omitempty"`
	Cmd                 []string  `json:"cmd,omitempty"`
	Env                 []string  `json:"env,omitempty"`
	WorkingDir          string    `json:"workingDir,omitempty"`
	User                string    `json:"user,omitempty"`
	Hostname            string    `json:"hostname,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

// EffectiveCommand returns final argv following entrypoint+cmd composition.
func (c ContainerConfig) EffectiveCommand() []string {
	command := make([]string, 0, len(c.Entrypoint)+len(c.Cmd))
	command = append(command, c.Entrypoint...)
	command = append(command, c.Cmd...)
	if len(command) == 0 {
		return []string{"/bin/sh"}
	}
	return command
}

// ContainerConfigStore saves and loads container config files.
type ContainerConfigStore struct {
	layout image.Layout
}

func NewContainerConfigStore(dataRoot string) *ContainerConfigStore {
	return &ContainerConfigStore{layout: image.NewLayout(dataRoot)}
}

func (s *ContainerConfigStore) Save(ctx context.Context, cfg ContainerConfig) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	if cfg.ID == "" {
		return "", fmt.Errorf("container id is required")
	}
	if cfg.Rootfs == "" {
		return "", fmt.Errorf("rootfs is required")
	}
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = time.Now().UTC()
	}

	path := s.layout.ContainerConfigPath(cfg.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create container dir: %w", err)
	}

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal container config: %w", err)
	}
	body = append(body, '\n')

	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("write container config: %w", err)
	}

	return path, nil
}

func (s *ContainerConfigStore) Load(ctx context.Context, containerID string) (*ContainerConfig, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	path := s.layout.ContainerConfigPath(containerID)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read container config: %w", err)
	}

	var cfg ContainerConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("decode container config: %w", err)
	}

	return &cfg, nil
}

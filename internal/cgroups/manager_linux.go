//go:build linux

package cgroups

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	envEnableCgroupV2 = "FC_ENABLE_CGROUPS_V2"
	envCgroupV2Root   = "FC_CGROUPS_V2_ROOT"
	envCgroupV2Prefix = "FC_CGROUPS_V2_PREFIX"

	defaultCgroupV2Root   = "/sys/fs/cgroup"
	defaultCgroupV2Prefix = "fish-container"
)

type v2Manager struct {
	enabled bool
	root    string
	prefix  string
}

// NewManager builds a cgroups manager from environment configuration.
func NewManager() Manager {
	cfg := loadConfigFromEnv()
	return &v2Manager{
		enabled: cfg.enabled,
		root:    cfg.root,
		prefix:  cfg.prefix,
	}
}

// ResolveOCIPath returns OCI Linux.CgroupsPath for a container when cgroups are enabled.
func ResolveOCIPath(containerID string) (string, bool, error) {
	cfg := loadConfigFromEnv()
	if !cfg.enabled {
		return "", false, nil
	}

	id, err := validateContainerID(containerID)
	if err != nil {
		return "", false, err
	}

	ociPath := "/" + path.Join(cfg.prefix, id)
	return ociPath, true, nil
}

func (m *v2Manager) Apply(ctx context.Context, containerID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if !m.enabled {
		return nil
	}

	id, err := validateContainerID(containerID)
	if err != nil {
		return err
	}
	if err := ensureUnifiedV2Mounted(m.root); err != nil {
		return err
	}

	target := filepath.Join(m.root, m.prefix, id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create cgroup dir: %w", err)
	}

	return nil
}

func (m *v2Manager) Delete(ctx context.Context, containerID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if !m.enabled {
		return nil
	}

	id, err := validateContainerID(containerID)
	if err != nil {
		return err
	}

	target := filepath.Join(m.root, m.prefix, id)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove cgroup dir: %w", err)
	}

	return nil
}

type managerConfig struct {
	enabled bool
	root    string
	prefix  string
}

func loadConfigFromEnv() managerConfig {
	enabled := parseBoolEnv(os.Getenv(envEnableCgroupV2))
	root := strings.TrimSpace(os.Getenv(envCgroupV2Root))
	if root == "" {
		root = defaultCgroupV2Root
	}

	prefix := strings.TrimSpace(os.Getenv(envCgroupV2Prefix))
	if prefix == "" {
		prefix = defaultCgroupV2Prefix
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		prefix = defaultCgroupV2Prefix
	}

	return managerConfig{enabled: enabled, root: root, prefix: prefix}
}

func parseBoolEnv(value string) bool {
	v := strings.TrimSpace(strings.ToLower(value))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func ensureUnifiedV2Mounted(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("cgroup v2 root is required")
	}

	controllersPath := filepath.Join(root, "cgroup.controllers")
	if _, err := os.Stat(controllersPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cgroup v2 unified hierarchy not detected at %s", root)
		}
		return fmt.Errorf("stat cgroup.controllers: %w", err)
	}

	return nil
}

func validateContainerID(containerID string) (string, error) {
	id := strings.TrimSpace(containerID)
	if id == "" {
		return "", fmt.Errorf("container id is required")
	}
	if strings.Contains(id, "/") {
		return "", fmt.Errorf("invalid container id: %s", containerID)
	}
	return id, nil
}

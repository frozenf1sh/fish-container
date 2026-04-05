//go:build linux

package image

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type overlaySnapshotMounter struct {
	layout Layout
}

func NewSnapshotMounter(cfg Config) SnapshotMounter {
	return &overlaySnapshotMounter{layout: NewLayout(cfg.DataRoot)}
}

func (m *overlaySnapshotMounter) Mount(_ context.Context, req MountRequest) (*MountResult, error) {
	containerID := strings.TrimSpace(req.ContainerID)
	if containerID == "" {
		return nil, fmt.Errorf("container id is required")
	}
	if strings.Contains(containerID, "/") {
		return nil, fmt.Errorf("invalid container id: %s", containerID)
	}

	lowerDirs, err := m.resolveLowerDirs(req.LowerLayerDigests)
	if err != nil {
		return nil, err
	}
	if len(lowerDirs) == 0 {
		return nil, fmt.Errorf("at least one lower layer is required")
	}

	upperDir := m.layout.OverlayUpperDir(containerID)
	workDir := m.layout.OverlayWorkDir(containerID)
	mergedDir := m.layout.OverlayMergedDir(containerID)
	lockPath := m.layout.OverlayLockPath(containerID)

	if err := os.MkdirAll(m.layout.OverlayContainerDir(containerID), 0o755); err != nil {
		return nil, fmt.Errorf("create overlay container dir: %w", err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("container %s is already running or mounted", containerID)
		}
		return nil, fmt.Errorf("acquire container lock: %w", err)
	}
	_, _ = fmt.Fprintf(lockFile, "pid=%d\n", os.Getpid())
	_ = lockFile.Close()
	lockAcquired := true
	defer func() {
		if lockAcquired {
			_ = os.Remove(lockPath)
		}
	}()

	for _, dir := range []string{upperDir, workDir, mergedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create overlay dir %s: %w", dir, err)
		}
	}

	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(lowerDirs, ":"), upperDir, workDir)
	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, options); err != nil {
		return nil, fmt.Errorf("mount overlayfs: %w", err)
	}

	result := &MountResult{
		ContainerID: containerID,
		MergedDir:   mergedDir,
		UpperDir:    upperDir,
		WorkDir:     workDir,
		LowerDirs:   lowerDirs,
	}
	if err := m.writeMountMeta(result); err != nil {
		_ = syscall.Unmount(mergedDir, syscall.MNT_DETACH)
		return nil, err
	}

	lockAcquired = false

	return result, nil
}

func (m *overlaySnapshotMounter) Unmount(_ context.Context, containerID string) error {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return fmt.Errorf("container id is required")
	}

	mergedDir := m.layout.OverlayMergedDir(containerID)
	if err := syscall.Unmount(mergedDir, syscall.MNT_DETACH); err != nil {
		if err != syscall.EINVAL && err != syscall.ENOENT {
			return fmt.Errorf("unmount overlayfs: %w", err)
		}
	}

	if err := os.RemoveAll(m.layout.OverlayContainerDir(containerID)); err != nil {
		return fmt.Errorf("cleanup overlay dirs: %w", err)
	}

	return nil
}

func (m *overlaySnapshotMounter) resolveLowerDirs(digests []string) ([]string, error) {
	if len(digests) == 0 {
		return nil, nil
	}

	lowerDirs := make([]string, 0, len(digests))
	for i := len(digests) - 1; i >= 0; i-- {
		hex, err := digestHexFromSHA256(digests[i])
		if err != nil {
			return nil, err
		}
		dir := m.layout.UnpackedPath(hex)
		if _, err := os.Stat(dir); err != nil {
			return nil, fmt.Errorf("lower layer dir missing %s: %w", dir, err)
		}
		lowerDirs = append(lowerDirs, dir)
	}

	return lowerDirs, nil
}

func (m *overlaySnapshotMounter) writeMountMeta(result *MountResult) error {
	metaPath := m.layout.OverlayMetaPath(result.ContainerID)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return fmt.Errorf("create mount meta dir: %w", err)
	}

	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mount meta: %w", err)
	}

	if err := os.WriteFile(metaPath, payload, 0o644); err != nil {
		return fmt.Errorf("write mount meta: %w", err)
	}

	return nil
}

package image

import (
	"path/filepath"
	"strings"
)

const defaultDataRoot = "/var/lib/fish-container"

// Layout describes on-disk paths for image and snapshot artifacts.
type Layout struct {
	DataRoot string
}

func NewLayout(dataRoot string) Layout {
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = defaultDataRoot
	}

	return Layout{DataRoot: dataRoot}
}

func (l Layout) ImagesDir() string {
	return filepath.Join(l.DataRoot, "images")
}

func (l Layout) BlobsDir() string {
	return filepath.Join(l.ImagesDir(), "blobs", "sha256")
}

func (l Layout) BlobPath(digest string) string {
	return filepath.Join(l.BlobsDir(), digest)
}

func (l Layout) UnpackedDir() string {
	return filepath.Join(l.ImagesDir(), "unpacked", "sha256")
}

func (l Layout) UnpackedPath(digest string) string {
	return filepath.Join(l.UnpackedDir(), digest)
}

func (l Layout) ManifestsDir(registry, repository string) string {
	return filepath.Join(l.ImagesDir(), "manifests", strings.ToLower(registry), repository)
}

func (l Layout) ManifestPath(registry, repository, tag string) string {
	return filepath.Join(l.ManifestsDir(registry, repository), tag+".json")
}

func (l Layout) RefsDir(registry, repository string) string {
	return filepath.Join(l.ImagesDir(), "refs", strings.ToLower(registry), repository)
}

func (l Layout) RefPath(registry, repository, tag string) string {
	return filepath.Join(l.RefsDir(registry, repository), tag)
}

func (l Layout) ConfigsDir() string {
	return filepath.Join(l.ImagesDir(), "configs", "sha256")
}

func (l Layout) ConfigPath(digest string) string {
	return filepath.Join(l.ConfigsDir(), digest+".json")
}

func (l Layout) SnapshotsDir() string {
	return filepath.Join(l.DataRoot, "snapshots")
}

func (l Layout) OverlaySnapshotsDir() string {
	return filepath.Join(l.SnapshotsDir(), "overlay")
}

func (l Layout) OverlayContainerDir(containerID string) string {
	return filepath.Join(l.OverlaySnapshotsDir(), containerID)
}

func (l Layout) OverlayUpperDir(containerID string) string {
	return filepath.Join(l.OverlayContainerDir(containerID), "upper")
}

func (l Layout) OverlayWorkDir(containerID string) string {
	return filepath.Join(l.OverlayContainerDir(containerID), "work")
}

func (l Layout) OverlayMergedDir(containerID string) string {
	return filepath.Join(l.OverlayContainerDir(containerID), "merged")
}

func (l Layout) OverlayMetaPath(containerID string) string {
	return filepath.Join(l.OverlayContainerDir(containerID), "mount.json")
}

func (l Layout) OverlayLockPath(containerID string) string {
	return filepath.Join(l.OverlayContainerDir(containerID), ".lock")
}

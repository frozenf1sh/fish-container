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

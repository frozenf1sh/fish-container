package image

import "context"

// ProgressFunc reports current and total bytes for long-running operations.
type ProgressFunc func(current, total int64)

// Puller downloads image metadata and blobs into local store.
type Puller interface {
	Pull(ctx context.Context, reference string) (string, error)
}

// ManifestPuller fetches and persists OCI/Docker schema2 manifests.
type ManifestPuller interface {
	PullManifest(ctx context.Context, reference string) (*ManifestResult, error)
}

// BlobFetcher fetches and stores layer blobs by digest.
type BlobFetcher interface {
	FetchBlob(ctx context.Context, ref Reference, descriptor Descriptor, progress ProgressFunc) (string, error)
}

// LayerUnpacker unpacks downloaded layer blobs into CAS directories.
type LayerUnpacker interface {
	UnpackLayer(ctx context.Context, descriptor Descriptor, progress ProgressFunc) (string, error)
}

// MountRequest describes snapshot mount inputs.
type MountRequest struct {
	ImageDigest  string
	ContainerID  string
	LowerChainID string
}

// SnapshotMounter mounts/unmounts container rootfs snapshots.
type SnapshotMounter interface {
	Mount(ctx context.Context, req MountRequest) (string, error)
	Unmount(ctx context.Context, containerID string) error
}

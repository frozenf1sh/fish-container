package image

import "context"

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
	FetchBlob(ctx context.Context, ref Reference, descriptor Descriptor) (string, error)
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

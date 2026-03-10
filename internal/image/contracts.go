package image

import "context"

// Puller downloads image metadata and blobs into local store.
type Puller interface {
	Pull(ctx context.Context, reference string) (string, error)
}

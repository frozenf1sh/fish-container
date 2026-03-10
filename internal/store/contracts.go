package store

import "context"

// StateStore persists runtime metadata by container ID.
type StateStore interface {
	Save(ctx context.Context, id string, payload []byte) error
	Load(ctx context.Context, id string) ([]byte, error)
	Delete(ctx context.Context, id string) error
}

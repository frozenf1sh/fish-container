package runtime

import "context"

// Manager orchestrates container lifecycle operations.
type Manager interface {
	Create(ctx context.Context, id string) error
	Start(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

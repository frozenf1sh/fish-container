package network

import "context"

// Manager creates and recycles container network resources.
type Manager interface {
	Attach(ctx context.Context, containerID string) error
	Detach(ctx context.Context, containerID string) error
}

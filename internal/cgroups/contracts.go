package cgroups

import "context"

// Manager applies and cleans container resource constraints.
type Manager interface {
	Apply(ctx context.Context, containerID string) error
	Delete(ctx context.Context, containerID string) error
}

//go:build !linux

package cgroups

import "context"

type noopManager struct{}

// NewManager returns a no-op manager on non-Linux platforms.
func NewManager() Manager {
	return &noopManager{}
}

// ResolveOCIPath is disabled on non-Linux platforms.
func ResolveOCIPath(_ string) (string, bool, error) {
	return "", false, nil
}

func (m *noopManager) Apply(_ context.Context, _ string) error {
	return nil
}

func (m *noopManager) Delete(_ context.Context, _ string) error {
	return nil
}

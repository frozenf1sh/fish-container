package runtime

import "fmt"

// CanTransition reports whether OCI state transition is allowed.
func CanTransition(from, to StateStatus) bool {
	switch from {
	case StateCreating:
		return to == StateCreated || to == StateStopped
	case StateCreated:
		return to == StateRunning || to == StateStopped
	case StateRunning:
		return to == StateStopped
	case StateStopped:
		return false
	default:
		return false
	}
}

// ValidateTransition validates OCI state transitions strictly.
func ValidateTransition(from, to StateStatus) error {
	if from == to {
		return nil
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid state transition: %s -> %s", from, to)
	}
	return nil
}

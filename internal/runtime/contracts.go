package runtime

import "context"

// StateStatus follows OCI runtime state.status values.
type StateStatus string

const (
	StateCreating StateStatus = "creating"
	StateCreated  StateStatus = "created"
	StateRunning  StateStatus = "running"
	StateStopped  StateStatus = "stopped"
)

// State represents OCI runtime state payload shape.
type State struct {
	Version     string            `json:"ociVersion"`
	ID          string            `json:"id"`
	Status      StateStatus       `json:"status"`
	Pid         int               `json:"pid,omitempty"`
	Bundle      string            `json:"bundle"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// StateStore persists OCI runtime state documents.
type StateStore interface {
	Save(ctx context.Context, state State) (string, error)
	Load(ctx context.Context, id string) (*State, error)
	Delete(ctx context.Context, id string) error
}

// Manager orchestrates container lifecycle operations.
type Manager interface {
	Create(ctx context.Context, id string) error
	Start(ctx context.Context, id string) error
	State(ctx context.Context, id string) (*State, error)
	Kill(ctx context.Context, id string, signal int) error
	Exec(ctx context.Context, id string, args []string) error
	Attach(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

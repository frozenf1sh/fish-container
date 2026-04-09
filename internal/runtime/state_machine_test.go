package runtime

import "testing"

func TestValidateTransition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		from    StateStatus
		to      StateStatus
		wantErr bool
	}{
		{StateCreating, StateCreated, false},
		{StateCreating, StateStopped, false},
		{StateCreated, StateRunning, false},
		{StateRunning, StateStopped, false},
		{StateCreated, StateStopped, false},
		{StateStopped, StateRunning, true},
		{StateRunning, StateCreated, true},
	}

	for _, tc := range cases {
		err := ValidateTransition(tc.from, tc.to)
		if tc.wantErr && err == nil {
			t.Fatalf("expected transition %s->%s to fail", tc.from, tc.to)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("expected transition %s->%s to pass, got: %v", tc.from, tc.to, err)
		}
	}
}

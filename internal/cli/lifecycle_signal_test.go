package cli

import (
	"syscall"
	"testing"
)

func TestParseSignal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input   string
		expect  syscall.Signal
		wantErr bool
	}{
		{"", syscall.SIGTERM, false},
		{"TERM", syscall.SIGTERM, false},
		{"SIGKILL", syscall.SIGKILL, false},
		{"9", syscall.Signal(9), false},
		{"INT", syscall.SIGINT, false},
		{"SIGUNKNOWN", 0, true},
	}

	for _, tc := range cases {
		got, err := parseSignal(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("expected error for %q", tc.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.input, err)
		}
		if got != tc.expect {
			t.Fatalf("signal mismatch for %q: got=%d want=%d", tc.input, got, tc.expect)
		}
	}
}

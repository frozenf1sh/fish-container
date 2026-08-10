package cli

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"fish-container/internal/runtime"
)

func TestParseSignal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input   string
		expect  syscall.Signal
		wantErr bool
	}{
		{"", syscall.SIGKILL, false},
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

func TestProcessAliveTreatsZombieAsExited(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer cmd.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(cmd.Process.Pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zombie pid %d was still considered alive", cmd.Process.Pid)
}

func TestReconcileCreatedZombie(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer cmd.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for processAlive(cmd.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	store := runtime.NewFileStateStore(t.TempDir())
	state := runtime.State{ID: "created-zombie", Status: runtime.StateCreated, Pid: cmd.Process.Pid, Bundle: "/tmp/bundle"}
	if _, err := store.Save(context.Background(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := reconcileStateIfExited(store, &state); err != nil {
		t.Fatalf("reconcile state: %v", err)
	}
	if state.Status != runtime.StateStopped || state.Pid != 0 {
		t.Fatalf("unexpected reconciled state: status=%s pid=%d", state.Status, state.Pid)
	}
}

func TestSignalContainerProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process group: %v", err)
	}
	defer cmd.Wait()

	childrenPath := "/proc/" + strconv.Itoa(cmd.Process.Pid) + "/task/" + strconv.Itoa(cmd.Process.Pid) + "/children"
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(childrenPath)
		if err == nil {
			fields := strings.Fields(string(body))
			if len(fields) > 0 {
				childPID, _ = strconv.Atoi(fields[0])
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("child process was not observed")
	}
	if err := signalContainerProcess(cmd.Process.Pid, syscall.SIGKILL, true); err != nil {
		t.Fatalf("signal process group: %v", err)
	}
	for time.Now().Before(deadline) && processAlive(childPID) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(childPID) {
		t.Fatalf("child pid %d survived process-group kill", childPID)
	}
}

func TestDeleteContainerIsIdempotent(t *testing.T) {
	dataRoot := t.TempDir()
	if err := deleteContainer(context.Background(), dataRoot, "missing", false); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := deleteContainer(context.Background(), dataRoot, "missing", false); err != nil {
		t.Fatalf("repeated delete: %v", err)
	}
}

//go:build linux

package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fish-container/internal/image"
	"fish-container/internal/runtime"
)

const monitorCommandName = "__monitor"

func monitorCommand(args []string) error {
	fs := flag.NewFlagSet(monitorCommandName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if containerID == "" {
		return fmt.Errorf("--container is required")
	}
	return monitorContainer(context.Background(), dataRoot, containerID)
}

func launchContainerMonitor(dataRoot, containerID string, attachIO bool) (int, error) {
	layout := image.NewLayout(dataRoot)
	logPath := filepath.Join(layout.RuntimeContainerDir(containerID), "start-daemon.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, fmt.Errorf("create monitor log dir: %w", err)
	}

	cmd := exec.Command("/proc/self/exe", monitorCommandName, "--data-root", dataRoot, "--container", containerID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if attachIO {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, fmt.Errorf("open monitor log: %w", err)
		}
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start container monitor: %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(monitorPIDPath(dataRoot, containerID), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0, fmt.Errorf("write container monitor pid: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		_ = cmd.Process.Kill()
		_ = os.Remove(monitorPIDPath(dataRoot, containerID))
		return 0, fmt.Errorf("release container monitor: %w", err)
	}
	return pid, nil
}

func createContainerFromBundle(ctx context.Context, dataRoot, containerID, bundlePath, pidFile string) error {
	if err := runtime.ValidateContainerID(containerID); err != nil {
		return err
	}
	absBundle, err := filepath.Abs(bundlePath)
	if err != nil {
		return fmt.Errorf("resolve bundle path: %w", err)
	}
	spec, err := runtime.LoadBundleSpec(absBundle)
	if err != nil {
		return err
	}
	if _, err := runtime.RunSpecFromOCISpec(absBundle, spec); err != nil {
		return err
	}

	store := runtime.NewFileStateStore(dataRoot)
	if _, err := store.Load(ctx, containerID); err == nil {
		return fmt.Errorf("container already exists: %s", containerID)
	}
	state := runtime.State{
		ID:          containerID,
		Status:      runtime.StateCreating,
		Bundle:      absBundle,
		Annotations: map[string]string{"fish-container.io/image-mounted": "false"},
	}
	if _, err := store.Save(ctx, state); err != nil {
		return err
	}
	monitorPID, err := launchContainerMonitor(dataRoot, containerID, false)
	if err != nil {
		return err
	}
	if err := waitForContainerCreated(ctx, store, containerID, 10*time.Second); err != nil {
		_ = syscall.Kill(monitorPID, syscall.SIGKILL)
		return err
	}
	created, err := store.Load(ctx, containerID)
	if err != nil {
		return err
	}
	if pidFile != "" {
		if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
			return fmt.Errorf("create pid file dir: %w", err)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(created.Pid)+"\n"), 0o644); err != nil {
			return fmt.Errorf("write pid file: %w", err)
		}
	}
	_, _ = fmt.Fprintf(os.Stdout, "container created from bundle: %s pid=%d\n", containerID, created.Pid)
	return nil
}

func monitorContainer(ctx context.Context, dataRoot, containerID string) error {
	if err := waitForMonitorRegistration(dataRoot, containerID, os.Getpid(), 2*time.Second); err != nil {
		return err
	}
	defer os.Remove(monitorPIDPath(dataRoot, containerID))
	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(ctx, containerID)
	if err != nil {
		return err
	}
	if state.Status != runtime.StateCreating {
		return fmt.Errorf("monitor requires state=creating, current=%s", state.Status)
	}

	socketPath := monitorSocketPath(dataRoot, containerID)
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return markMonitorSetupFailed(stateStore, state, fmt.Errorf("listen monitor socket: %w", err))
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return markMonitorSetupFailed(stateStore, state, fmt.Errorf("chmod monitor socket: %w", err))
	}

	spec, err := runtime.LoadBundleSpec(state.Bundle)
	if err != nil {
		return markMonitorSetupFailed(stateStore, state, err)
	}
	runSpec, err := runtime.RunSpecFromOCISpec(state.Bundle, spec)
	if err != nil {
		return markMonitorSetupFailed(stateStore, state, err)
	}
	prepared, err := runtime.Prepare(runSpec, true)
	if err != nil {
		return markMonitorSetupFailed(stateStore, state, err)
	}
	if err := prepared.WaitReady(); err != nil {
		_ = prepared.Cmd.Wait()
		return markMonitorSetupFailed(stateStore, state, err)
	}

	if err := runtime.ValidateTransition(state.Status, runtime.StateCreated); err != nil {
		_ = prepared.Cmd.Process.Kill()
		_ = prepared.Cmd.Wait()
		return markMonitorSetupFailed(stateStore, state, err)
	}
	state.Status = runtime.StateCreated
	state.Pid = prepared.Cmd.Process.Pid
	if _, err := stateStore.Save(ctx, *state); err != nil {
		_ = prepared.Cmd.Process.Kill()
		_ = prepared.Cmd.Wait()
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "container prepared: %s pid=%d\n", containerID, state.Pid)

	started, startErr := waitForStartRequest(listener, prepared, stateStore, state)
	waitErr := prepared.Cmd.Wait()
	if startErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "container start failed: %v\n", startErr)
	}
	if err := persistMonitorExit(stateStore, containerID, waitErr); err != nil {
		return err
	}
	if started {
		_, _ = fmt.Fprintf(os.Stdout, "container stopped: %s\n", containerID)
	}
	return nil
}

func waitForMonitorRegistration(dataRoot, containerID string, pid int, timeout time.Duration) error {
	path := monitorPIDPath(dataRoot, containerID)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(body)) == strconv.Itoa(pid) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("monitor pid registration did not appear: %s", path)
}

func waitForStartRequest(listener net.Listener, prepared *runtime.PreparedProcess, stateStore *runtime.FileStateStore, state *runtime.State) (bool, error) {
	unixListener, _ := listener.(*net.UnixListener)
	for {
		if !processAlive(state.Pid) {
			return false, nil
		}
		if unixListener != nil {
			_ = unixListener.SetDeadline(time.Now().Add(250 * time.Millisecond))
		}
		conn, err := listener.Accept()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return false, fmt.Errorf("accept monitor command: %w", err)
		}

		command, readErr := bufio.NewReader(conn).ReadString('\n')
		if readErr != nil {
			_, _ = fmt.Fprintf(conn, "error: %v\n", readErr)
			_ = conn.Close()
			continue
		}
		if strings.TrimSpace(command) != "start" {
			_, _ = fmt.Fprintln(conn, "error: unsupported monitor command")
			_ = conn.Close()
			continue
		}
		if err := prepared.Start(); err != nil {
			_, _ = fmt.Fprintf(conn, "error: %v\n", err)
			_ = conn.Close()
			return false, err
		}
		if err := runtime.ValidateTransition(state.Status, runtime.StateRunning); err != nil {
			_, _ = fmt.Fprintf(conn, "error: %v\n", err)
			_ = conn.Close()
			return true, err
		}
		state.Status = runtime.StateRunning
		if _, err := stateStore.Save(context.Background(), *state); err != nil {
			_, _ = fmt.Fprintf(conn, "error: %v\n", err)
			_ = conn.Close()
			return true, err
		}
		_, _ = fmt.Fprintf(os.Stdout, "container running: %s pid=%d\n", state.ID, state.Pid)
		_, _ = fmt.Fprintln(conn, "ok")
		_ = conn.Close()
		return true, nil
	}
}

func requestContainerStart(ctx context.Context, dataRoot, containerID string) error {
	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(ctx, containerID)
	if err != nil {
		return err
	}
	if state.Status == runtime.StateRunning {
		return nil
	}
	if err := runtime.ValidateTransition(state.Status, runtime.StateRunning); err != nil {
		return err
	}

	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", monitorSocketPath(dataRoot, containerID))
	if err != nil {
		return fmt.Errorf("connect container monitor: %w", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, "start"); err != nil {
		return fmt.Errorf("send start command: %w", err)
	}
	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read start response: %w", err)
	}
	response = strings.TrimSpace(response)
	if response != "ok" {
		return fmt.Errorf("container monitor: %s", response)
	}
	return nil
}

func waitForContainerCreated(ctx context.Context, store *runtime.FileStateStore, containerID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := store.Load(ctx, containerID)
		if err == nil {
			switch state.Status {
			case runtime.StateCreated:
				return nil
			case runtime.StateStopped:
				return fmt.Errorf("container init failed before created; see %s", monitorLogPath(store, containerID))
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for container %s to reach created", containerID)
}

func waitForContainerStopped(ctx context.Context, dataRoot, containerID string) error {
	store := runtime.NewFileStateStore(dataRoot)
	for {
		state, err := store.Load(ctx, containerID)
		if err != nil {
			return err
		}
		if state.Status == runtime.StateStopped {
			exitStatus, _ := strconv.Atoi(state.Annotations["fish-container.io/exit-status"])
			if exitStatus != 0 {
				return fmt.Errorf("container process exited with status %d", exitStatus)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func waitForMonitorStopped(ctx context.Context, store *runtime.FileStateStore, containerID string, timeout time.Duration) (*runtime.State, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := store.Load(ctx, containerID)
		if err != nil {
			return nil, err
		}
		if state.Status == runtime.StateStopped {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil, nil
}

func waitForMonitorExit(dataRoot, containerID string, timeout time.Duration) error {
	socketPath := monitorSocketPath(dataRoot, containerID)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(socketPath); os.IsNotExist(err) {
			return nil
		}
		conn, err := net.DialTimeout("unix", socketPath, 50*time.Millisecond)
		if err != nil {
			_ = os.Remove(socketPath)
			return nil
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("container monitor still active: %s", socketPath)
}

func stopContainerMonitor(dataRoot, containerID string, timeout time.Duration) error {
	body, err := os.ReadFile(monitorPIDPath(dataRoot, containerID))
	if err != nil {
		if os.IsNotExist(err) {
			if socketActive(monitorSocketPath(dataRoot, containerID)) {
				return fmt.Errorf("container monitor is active but monitor.pid is missing")
			}
			_ = os.Remove(monitorSocketPath(dataRoot, containerID))
			return nil
		}
		return fmt.Errorf("read container monitor pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid container monitor pid %q", strings.TrimSpace(string(body)))
	}
	if !processAlive(pid) {
		_ = os.Remove(monitorPIDPath(dataRoot, containerID))
		_ = os.Remove(monitorSocketPath(dataRoot, containerID))
		return nil
	}
	if !isContainerMonitor(pid, containerID) {
		return fmt.Errorf("refusing to signal pid %d: process is not the %s container monitor", pid, containerID)
	}
	children, err := monitorChildPIDs(pid)
	if err != nil {
		return err
	}
	for _, childPID := range children {
		if err := signalContainerProcess(childPID, syscall.SIGKILL, true); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill container init pid %d: %w", childPID, err)
		}
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill container monitor pid %d: %w", pid, err)
	}
	if err := waitProcessExit(pid, timeout); err != nil {
		return fmt.Errorf("wait container monitor exit: %w", err)
	}
	_ = os.Remove(monitorPIDPath(dataRoot, containerID))
	_ = os.Remove(monitorSocketPath(dataRoot, containerID))
	return nil
}

func monitorChildPIDs(pid int) ([]int, error) {
	path := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read container monitor children: %w", err)
	}
	fields := strings.Fields(string(body))
	children := make([]int, 0, len(fields))
	for _, field := range fields {
		childPID, err := strconv.Atoi(field)
		if err != nil || childPID <= 0 {
			return nil, fmt.Errorf("invalid container monitor child pid %q", field)
		}
		children = append(children, childPID)
	}
	return children, nil
}

func socketActive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 50*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func isContainerMonitor(pid int, containerID string) bool {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(body), "\x00"), "\x00")
	foundCommand := false
	foundContainer := false
	for index, arg := range args {
		if arg == monitorCommandName {
			foundCommand = true
		}
		if arg == "--container" && index+1 < len(args) && args[index+1] == containerID {
			foundContainer = true
		}
	}
	return foundCommand && foundContainer
}

func persistMonitorExit(store *runtime.FileStateStore, containerID string, waitErr error) error {
	state, err := store.Load(context.Background(), containerID)
	if err != nil {
		return err
	}
	if state.Status != runtime.StateStopped {
		if err := runtime.ValidateTransition(state.Status, runtime.StateStopped); err != nil {
			return err
		}
		state.Status = runtime.StateStopped
		state.Pid = 0
	}
	if state.Annotations == nil {
		state.Annotations = make(map[string]string)
	}
	state.Annotations["fish-container.io/exit-status"] = strconv.Itoa(processExitStatus(waitErr))
	state.Annotations["fish-container.io/exited-at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_, err = store.Save(context.Background(), *state)
	return err
}

func processExitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return 128 + int(status.Signal())
			}
			return status.ExitStatus()
		}
	}
	return 255
}

func markMonitorSetupFailed(store *runtime.FileStateStore, state *runtime.State, setupErr error) error {
	if state.Annotations == nil {
		state.Annotations = make(map[string]string)
	}
	state.Annotations["fish-container.io/setup-error"] = setupErr.Error()
	state.Status = runtime.StateStopped
	state.Pid = 0
	_, _ = store.Save(context.Background(), *state)
	return setupErr
}

func monitorSocketPath(dataRoot, containerID string) string {
	return filepath.Join(image.NewLayout(dataRoot).RuntimeContainerDir(containerID), "monitor.sock")
}

func monitorPIDPath(dataRoot, containerID string) string {
	return filepath.Join(image.NewLayout(dataRoot).RuntimeContainerDir(containerID), "monitor.pid")
}

func monitorLogPath(_ *runtime.FileStateStore, containerID string) string {
	return filepath.Join("runtime", containerID, "start-daemon.log")
}

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"fish-container/internal/image"
	"fish-container/internal/runtime"
	"fish-container/internal/store"
)

const startDaemonCommandName = "__start-daemon"

func createCommand(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rootfs string
	var imageRef string
	var dataRoot string
	var containerID string
	var hostname string
	var workdir string
	var user string
	var envOverrides envListFlag
	fs.StringVar(&rootfs, "rootfs", "", "local rootfs path")
	fs.StringVar(&imageRef, "image", "", "image reference, e.g. alpine:latest")
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	fs.StringVar(&hostname, "hostname", "fish-container", "container hostname")
	fs.StringVar(&workdir, "workdir", "", "override container working directory")
	fs.StringVar(&user, "user", "", "override container user")
	fs.Var(&envOverrides, "env", "override environment variable, e.g. --env KEY=VALUE")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse create flags: %w", err)
	}
	if rootfs != "" && imageRef != "" {
		return fmt.Errorf("--rootfs and --image are mutually exclusive")
	}
	if rootfs == "" && imageRef == "" {
		return fmt.Errorf("one of --rootfs or --image is required")
	}
	if containerID == "" {
		containerID = fmt.Sprintf("ctr-%d", time.Now().UnixNano())
	}

	opts := createOptions{
		dataRoot:     dataRoot,
		containerID:  containerID,
		rootfs:       rootfs,
		imageRef:     imageRef,
		hostname:     hostname,
		workdir:      workdir,
		user:         user,
		envOverrides: envOverrides,
		cmdOverride:  fs.Args(),
	}

	_, err := createContainer(context.Background(), opts)
	return err
}

func startCommand(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	var detach bool
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	fs.BoolVar(&detach, "d", false, "run in background")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse start flags: %w", err)
	}
	if containerID == "" {
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: fish-container start [--data-root PATH] [--d] --container <id> | fish-container start <id>")
		}
		containerID = fs.Arg(0)
	}

	return startContainer(context.Background(), dataRoot, containerID, detach)
}

func stateCommand(args []string) error {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse state flags: %w", err)
	}
	if containerID == "" {
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: fish-container state [--data-root PATH] --container <id> | fish-container state <id>")
		}
		containerID = fs.Arg(0)
	}

	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(context.Background(), containerID)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, string(body))
	return nil
}

func deleteCommand(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse delete flags: %w", err)
	}
	if containerID == "" {
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: fish-container delete [--data-root PATH] --container <id> | fish-container delete <id>")
		}
		containerID = fs.Arg(0)
	}

	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(context.Background(), containerID)
	if err != nil {
		return err
	}
	if state.Status != runtime.StateStopped {
		return fmt.Errorf("delete requires state=stopped, current=%s", state.Status)
	}

	mounter := image.NewSnapshotMounter(image.LoadConfigFromEnv(dataRoot))
	_ = mounter.Unmount(context.Background(), containerID)

	layout := image.NewLayout(dataRoot)
	if err := os.RemoveAll(layout.ContainerDir(containerID)); err != nil {
		return fmt.Errorf("remove container dir: %w", err)
	}
	if err := stateStore.Delete(context.Background(), containerID); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "container deleted: %s\n", containerID)
	return nil
}

func killCommand(args []string) error {
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	var signalValue string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	fs.StringVar(&signalValue, "signal", "TERM", "signal name or number")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse kill flags: %w", err)
	}
	if containerID == "" {
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: fish-container kill [--data-root PATH] [--signal TERM] --container <id> | fish-container kill <id>")
		}
		containerID = fs.Arg(0)
	}

	sig, err := parseSignal(signalValue)
	if err != nil {
		return err
	}

	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(context.Background(), containerID)
	if err != nil {
		return err
	}
	if state.Status != runtime.StateRunning {
		return fmt.Errorf("kill requires state=running, current=%s", state.Status)
	}
	if state.Pid <= 0 {
		return fmt.Errorf("invalid running pid in state: %d", state.Pid)
	}

	if err := syscall.Kill(state.Pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			if err := runtime.ValidateTransition(runtime.StateRunning, runtime.StateStopped); err != nil {
				return err
			}
			state.Status = runtime.StateStopped
			state.Pid = 0
			_, _ = stateStore.Save(context.Background(), *state)
			_, _ = fmt.Fprintf(os.Stdout, "container already exited: %s\n", containerID)
			return nil
		}
		return fmt.Errorf("send signal %d to pid %d: %w", sig, state.Pid, err)
	}

	if err := waitProcessExit(state.Pid, 5*time.Second); err != nil {
		return fmt.Errorf("wait process exit after signal: %w", err)
	}

	if err := runtime.ValidateTransition(runtime.StateRunning, runtime.StateStopped); err != nil {
		return err
	}
	state.Status = runtime.StateStopped
	state.Pid = 0
	if _, err := stateStore.Save(context.Background(), *state); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "container killed: %s\n", containerID)
	return nil
}

func psCommand(args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var quiet bool
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.BoolVar(&quiet, "q", false, "only print container ids")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse ps flags: %w", err)
	}

	layout := image.NewLayout(dataRoot)
	entries, err := os.ReadDir(layout.RuntimeDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read runtime dir: %w", err)
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)

	stateStore := runtime.NewFileStateStore(dataRoot)
	configStore := store.NewContainerConfigStore(dataRoot)

	if quiet {
		for _, id := range ids {
			state, err := stateStore.Load(context.Background(), id)
			if err != nil {
				continue
			}
			if state.Status == runtime.StateRunning && state.Pid > 0 && !processAlive(state.Pid) {
				_ = reconcileStoppedState(stateStore, state)
			}
			_, _ = fmt.Fprintln(os.Stdout, id)
		}
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tPID\tIMAGE\tCREATED\tCONFIG_PATH\tSTATE_PATH\tBUNDLE_PATH")

	for _, id := range ids {
		state, err := stateStore.Load(context.Background(), id)
		if err != nil {
			continue
		}
		if state.Status == runtime.StateRunning && state.Pid > 0 && !processAlive(state.Pid) {
			if err := reconcileStoppedState(stateStore, state); err == nil {
				state.Status = runtime.StateStopped
				state.Pid = 0
			}
		}

		imageRef := ""
		createdAt := ""
		if cfg, err := configStore.Load(context.Background(), id); err == nil {
			imageRef = cfg.ImageRef
			createdAt = cfg.CreatedAt.Format(time.RFC3339)
		}

		_, _ = fmt.Fprintf(
			tw,
			"%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			id,
			state.Status,
			state.Pid,
			imageRef,
			createdAt,
			layout.ContainerConfigPath(id),
			layout.RuntimeStatePath(id),
			state.Bundle,
		)
	}

	return tw.Flush()
}

func parseSignal(value string) (syscall.Signal, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(value))
	if trimmed == "" {
		return syscall.SIGTERM, nil
	}
	if n, err := strconv.Atoi(trimmed); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("invalid signal number: %d", n)
		}
		return syscall.Signal(n), nil
	}

	trimmed = strings.TrimPrefix(trimmed, "SIG")
	switch trimmed {
	case "TERM":
		return syscall.SIGTERM, nil
	case "KILL":
		return syscall.SIGKILL, nil
	case "INT":
		return syscall.SIGINT, nil
	case "QUIT":
		return syscall.SIGQUIT, nil
	case "HUP":
		return syscall.SIGHUP, nil
	default:
		return 0, fmt.Errorf("unsupported signal: %s", value)
	}
}

func waitProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d still alive after %s", pid, timeout)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}

func reconcileStoppedState(store *runtime.FileStateStore, state *runtime.State) error {
	if state == nil {
		return fmt.Errorf("state is required")
	}
	if err := runtime.ValidateTransition(runtime.StateRunning, runtime.StateStopped); err != nil {
		return err
	}
	state.Status = runtime.StateStopped
	state.Pid = 0
	_, err := store.Save(context.Background(), *state)
	return err
}

func startDaemonCommand(args []string) error {
	fs := flag.NewFlagSet(startDaemonCommandName, flag.ContinueOnError)
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

	if err := startContainerForeground(context.Background(), dataRoot, containerID, false); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "start daemon failed: %v\n", err)
		return err
	}
	return nil
}

func createContainer(ctx context.Context, opts createOptions) (string, error) {
	stateStore := runtime.NewFileStateStore(opts.dataRoot)
	if _, err := stateStore.Load(ctx, opts.containerID); err == nil {
		return "", fmt.Errorf("container already exists: %s", opts.containerID)
	}

	baseCfg := store.ContainerConfig{
		ID:        opts.containerID,
		Hostname:  opts.hostname,
		CreatedAt: time.Now().UTC(),
	}

	mountedFromImage := false
	if opts.imageRef != "" {
		prepared, err := prepareRootfsFromImage(ctx, opts.dataRoot, opts.containerID, opts.imageRef)
		if err != nil {
			return "", err
		}
		mountedFromImage = true

		baseCfg.ImageRef = prepared.ref.String()
		baseCfg.ImageManifestDigest = prepared.manifest.Digest
		baseCfg.ImageConfigDigest = prepared.manifest.ConfigDigest
		baseCfg.Rootfs = prepared.rootfs
		baseCfg.Entrypoint = append([]string(nil), prepared.manifest.Config.Config.Entrypoint...)
		baseCfg.Cmd = append([]string(nil), prepared.manifest.Config.Config.Cmd...)
		baseCfg.Env = append([]string(nil), prepared.manifest.Config.Config.Env...)
		baseCfg.WorkingDir = prepared.manifest.Config.Config.WorkingDir
		baseCfg.User = prepared.manifest.Config.Config.User
	} else {
		resolvedRootfs, err := absRootfs(opts.rootfs)
		if err != nil {
			return "", err
		}
		baseCfg.Rootfs = resolvedRootfs
	}

	containerCfg := resolveContainerConfig(baseCfg, opts.cmdOverride, opts.envOverrides, opts.workdir, opts.user)

	configStore := store.NewContainerConfigStore(opts.dataRoot)
	configPath, err := configStore.Save(ctx, containerCfg)
	if err != nil {
		if mountedFromImage {
			_ = image.NewSnapshotMounter(image.LoadConfigFromEnv(opts.dataRoot)).Unmount(context.Background(), opts.containerID)
		}
		return "", err
	}

	bundle, err := runtime.BuildOCIBundle(ctx, opts.dataRoot, containerCfg)
	if err != nil {
		if mountedFromImage {
			_ = image.NewSnapshotMounter(image.LoadConfigFromEnv(opts.dataRoot)).Unmount(context.Background(), opts.containerID)
		}
		return "", err
	}

	creating := runtime.State{
		ID:          opts.containerID,
		Status:      runtime.StateCreating,
		Bundle:      bundle.BundlePath,
		Annotations: map[string]string{"fish-container.io/image-mounted": fmt.Sprintf("%t", mountedFromImage)},
	}
	statePath, err := stateStore.Save(ctx, creating)
	if err != nil {
		return "", err
	}
	if err := runtime.ValidateTransition(runtime.StateCreating, runtime.StateCreated); err != nil {
		return "", err
	}
	creating.Status = runtime.StateCreated
	if _, err := stateStore.Save(ctx, creating); err != nil {
		return "", err
	}

	_, _ = fmt.Fprintf(os.Stdout, "container created: %s\n", opts.containerID)
	_, _ = fmt.Fprintf(os.Stdout, "container config: %s\n", configPath)
	_, _ = fmt.Fprintf(os.Stdout, "bundle: %s\n", bundle.BundlePath)
	_, _ = fmt.Fprintf(os.Stdout, "bundle spec: %s\n", bundle.SpecPath)
	_, _ = fmt.Fprintf(os.Stdout, "runtime state: %s\n", statePath)

	return opts.containerID, nil
}

func startContainer(ctx context.Context, dataRoot, containerID string, detach bool) error {
	if detach {
		layout := image.NewLayout(dataRoot)
		logPath := filepath.Join(layout.RuntimeContainerDir(containerID), "start-daemon.log")
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return fmt.Errorf("create daemon log dir: %w", err)
		}
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open daemon log: %w", err)
		}
		defer logFile.Close()

		cmd := exec.Command("/proc/self/exe", startDaemonCommandName, "--data-root", dataRoot, "--container", containerID)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start detached daemon: %w", err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "container started in background: %s (daemon pid=%d)\n", containerID, cmd.Process.Pid)
		return nil
	}

	return startContainerForeground(ctx, dataRoot, containerID, true)
}

func startContainerForeground(ctx context.Context, dataRoot, containerID string, attachIO bool) error {
	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(ctx, containerID)
	if err != nil {
		return err
	}
	if err := runtime.ValidateTransition(state.Status, runtime.StateRunning); err != nil {
		return err
	}

	spec, err := runtime.LoadBundleSpec(state.Bundle)
	if err != nil {
		return err
	}
	runSpec, err := runtime.RunSpecFromOCISpec(state.Bundle, spec)
	if err != nil {
		return err
	}

	cmd, err := runtime.Start(runSpec, attachIO)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return err
	}

	state.Status = runtime.StateRunning
	state.Pid = cmd.Process.Pid
	if _, err := stateStore.Save(ctx, *state); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "container running: %s pid=%d\n", containerID, cmd.Process.Pid)

	waitErr := cmd.Wait()
	if err := runtime.ValidateTransition(runtime.StateRunning, runtime.StateStopped); err != nil {
		return err
	}
	state.Status = runtime.StateStopped
	state.Pid = 0
	if _, err := stateStore.Save(context.Background(), *state); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "container stopped: %s\n", containerID)

	if waitErr != nil {
		return fmt.Errorf("container process exited with error: %w", waitErr)
	}
	return nil
}

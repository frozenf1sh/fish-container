package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"fish-container/internal/cgroups"
	"fish-container/internal/image"
	"fish-container/internal/runtime"
	"fish-container/internal/store"
)

func createCommand(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rootfs string
	var imageRef string
	var bundlePath string
	var pidFile string
	var dataRoot string
	var containerID string
	var hostname string
	var workdir string
	var user string
	var envOverrides envListFlag
	fs.StringVar(&rootfs, "rootfs", "", "local rootfs path")
	fs.StringVar(&imageRef, "image", "", "image reference, e.g. alpine:latest")
	fs.StringVar(&bundlePath, "bundle", "", "existing OCI bundle path")
	fs.StringVar(&pidFile, "pid-file", "", "write container init pid to file")
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	fs.StringVar(&hostname, "hostname", "fish-container", "container hostname")
	fs.StringVar(&workdir, "workdir", "", "override container working directory")
	fs.StringVar(&user, "user", "", "override container user")
	fs.Var(&envOverrides, "env", "override environment variable, e.g. --env KEY=VALUE")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse create flags: %w", err)
	}
	if bundlePath != "" {
		if rootfs != "" || imageRef != "" {
			return fmt.Errorf("--bundle is mutually exclusive with --rootfs and --image")
		}
		if containerID == "" {
			if fs.NArg() != 1 {
				return fmt.Errorf("usage: fish-container create --bundle PATH [--pid-file PATH] <id>")
			}
			containerID = fs.Arg(0)
		} else if fs.NArg() != 0 {
			return fmt.Errorf("create --bundle does not accept a command override")
		}
		return createContainerFromBundle(context.Background(), dataRoot, containerID, bundlePath, pidFile)
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
		attachIO:     false,
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
	if err := reconcileStateIfExited(stateStore, state); err != nil {
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
	var force bool
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	fs.BoolVar(&force, "force", false, "force-delete a running container")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse delete flags: %w", err)
	}
	if containerID == "" {
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: fish-container delete [--data-root PATH] [--force] --container <id> | fish-container delete <id>")
		}
		containerID = fs.Arg(0)
	}

	return deleteContainer(context.Background(), dataRoot, containerID, force)
}

func deleteContainer(ctx context.Context, dataRoot, containerID string, force bool) error {
	if err := runtime.ValidateContainerID(containerID); err != nil {
		return err
	}

	stateStore := runtime.NewFileStateStore(dataRoot)
	state, err := stateStore.Load(ctx, containerID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state != nil {
		if err := reconcileStateIfExited(stateStore, state); err != nil {
			return err
		}
		switch state.Status {
		case runtime.StateCreating:
			if !force {
				return fmt.Errorf("delete requires state=stopped, current=%s", state.Status)
			}
			if err := stopContainerMonitor(dataRoot, containerID, 2*time.Second); err != nil {
				return err
			}
		case runtime.StateCreated, runtime.StateRunning:
			if !force {
				return fmt.Errorf("delete requires state=stopped, current=%s", state.Status)
			}
			if err := signalContainerProcess(state.Pid, syscall.SIGKILL, true); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("force-kill pid %d: %w", state.Pid, err)
			}
			if err := waitProcessExit(state.Pid, 5*time.Second); err != nil {
				return err
			}
			if stopped, err := waitForMonitorStopped(ctx, stateStore, containerID, 2*time.Second); err != nil {
				return err
			} else if stopped != nil {
				state = stopped
			} else if err := reconcileStoppedState(stateStore, state); err != nil {
				return err
			}
		case runtime.StateStopped:
		default:
			return fmt.Errorf("unsupported container state: %s", state.Status)
		}
	}
	if state == nil && force && socketActive(monitorSocketPath(dataRoot, containerID)) {
		if err := stopContainerMonitor(dataRoot, containerID, 2*time.Second); err != nil {
			return err
		}
	}

	if err := waitForMonitorExit(dataRoot, containerID, 2*time.Second); err != nil {
		if !force {
			return err
		}
		if err := stopContainerMonitor(dataRoot, containerID, 2*time.Second); err != nil {
			return err
		}
	}
	if err := cleanupContainerResources(ctx, dataRoot, containerID); err != nil {
		return err
	}

	if state == nil {
		_, _ = fmt.Fprintf(os.Stdout, "container already deleted: %s\n", containerID)
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "container deleted: %s\n", containerID)
	}
	return nil
}

func cleanupContainerResources(ctx context.Context, dataRoot, containerID string) error {
	mounter := image.NewSnapshotMounter(image.LoadConfigFromEnv(dataRoot))
	if err := mounter.Unmount(ctx, containerID); err != nil {
		return err
	}

	layout := image.NewLayout(dataRoot)
	if err := os.RemoveAll(layout.ContainerDir(containerID)); err != nil {
		return fmt.Errorf("remove container dir: %w", err)
	}
	if err := runtime.NewFileStateStore(dataRoot).Delete(ctx, containerID); err != nil {
		return err
	}
	if err := cgroups.NewManager().Delete(ctx, containerID); err != nil {
		return err
	}
	return nil
}

func killCommand(args []string) error {
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	var signalValue string
	var signalAll bool
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	fs.StringVar(&signalValue, "signal", "KILL", "signal name or number")
	fs.BoolVar(&signalAll, "all", false, "send the signal to the container process group")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse kill flags: %w", err)
	}
	signalFlagSet := false
	fs.Visit(func(current *flag.Flag) {
		if current.Name == "signal" {
			signalFlagSet = true
		}
	})
	positionals := fs.Args()
	if containerID == "" {
		if len(positionals) < 1 || len(positionals) > 2 {
			return fmt.Errorf("usage: fish-container kill [--data-root PATH] [--all] [--signal KILL] <id> [signal]")
		}
		containerID = positionals[0]
		positionals = positionals[1:]
	}
	if len(positionals) > 1 {
		return fmt.Errorf("kill accepts at most one positional signal")
	}
	if len(positionals) == 1 {
		if signalFlagSet {
			return fmt.Errorf("signal must be supplied either positionally or with --signal, not both")
		}
		signalValue = positionals[0]
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
	if state.Status == runtime.StateStopped {
		_, _ = fmt.Fprintf(os.Stdout, "container already stopped: %s\n", containerID)
		return nil
	}
	if state.Status != runtime.StateRunning && state.Status != runtime.StateCreated {
		return fmt.Errorf("kill requires state=created|running, current=%s", state.Status)
	}
	if state.Pid <= 0 {
		return fmt.Errorf("invalid running pid in state: %d", state.Pid)
	}

	if err := signalContainerProcess(state.Pid, sig, signalAll); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			if err := reconcileStoppedState(stateStore, state); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(os.Stdout, "container already exited: %s\n", containerID)
			return nil
		}
		return fmt.Errorf("send signal %d to pid %d: %w", sig, state.Pid, err)
	}

	if err := waitProcessExit(state.Pid, 5*time.Second); err != nil {
		return fmt.Errorf("wait process exit after signal: %w", err)
	}

	if stopped, err := waitForMonitorStopped(context.Background(), stateStore, containerID, 2*time.Second); err != nil {
		return err
	} else if stopped == nil {
		if err := reconcileStoppedState(stateStore, state); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "container killed: %s\n", containerID)
	return nil
}

func psCommand(args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var quiet bool
	var containerID string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.BoolVar(&quiet, "q", false, "only print container ids")
	fs.StringVar(&containerID, "container", "", "filter by container id")

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
			if containerID != "" && entry.Name() != containerID {
				continue
			}
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
			_ = reconcileStateIfExited(stateStore, state)
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
		_ = reconcileStateIfExited(stateStore, state)

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
		return syscall.SIGKILL, nil
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

func signalContainerProcess(pid int, signal syscall.Signal, all bool) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	if !all {
		return syscall.Kill(pid, signal)
	}
	if err := syscall.Kill(-pid, signal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return syscall.Kill(pid, signal)
		}
		return err
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if err != nil {
		return true
	}

	body, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if readErr != nil {
		return !os.IsNotExist(readErr)
	}
	closingParen := strings.LastIndexByte(string(body), ')')
	if closingParen < 0 || closingParen+2 >= len(body) {
		return true
	}
	state := body[closingParen+2]
	return state != 'Z' && state != 'X' && state != 'x'
}

func reconcileStoppedState(store *runtime.FileStateStore, state *runtime.State) error {
	if state == nil {
		return fmt.Errorf("state is required")
	}
	if err := runtime.ValidateTransition(state.Status, runtime.StateStopped); err != nil {
		return err
	}
	state.Status = runtime.StateStopped
	state.Pid = 0
	_, err := store.Save(context.Background(), *state)
	return err
}

func reconcileStateIfExited(store *runtime.FileStateStore, state *runtime.State) error {
	if state == nil || state.Pid <= 0 {
		return nil
	}
	if state.Status != runtime.StateCreated && state.Status != runtime.StateRunning {
		return nil
	}
	if processAlive(state.Pid) {
		return nil
	}
	return reconcileStoppedState(store, state)
}

func createContainer(ctx context.Context, opts createOptions) (string, error) {
	if err := runtime.ValidateContainerID(opts.containerID); err != nil {
		return "", err
	}
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

	cgroupApplied := false
	if cgroupPath, enabled, err := cgroups.ResolveOCIPath(opts.containerID); err != nil {
		return "", err
	} else if enabled {
		if err := cgroups.NewManager().Apply(ctx, opts.containerID); err != nil {
			return "", err
		}
		cgroupApplied = true
		containerCfg.CgroupsPath = cgroupPath
	}

	configStore := store.NewContainerConfigStore(opts.dataRoot)
	configPath, err := configStore.Save(ctx, containerCfg)
	if err != nil {
		if cgroupApplied {
			_ = cgroups.NewManager().Delete(context.Background(), opts.containerID)
		}
		if mountedFromImage {
			_ = image.NewSnapshotMounter(image.LoadConfigFromEnv(opts.dataRoot)).Unmount(context.Background(), opts.containerID)
		}
		return "", err
	}

	bundle, err := runtime.BuildOCIBundle(ctx, opts.dataRoot, containerCfg)
	if err != nil {
		if cgroupApplied {
			_ = cgroups.NewManager().Delete(context.Background(), opts.containerID)
		}
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
	monitorPID, err := launchContainerMonitor(opts.dataRoot, opts.containerID, opts.attachIO)
	if err != nil {
		return "", err
	}
	if err := waitForContainerCreated(ctx, stateStore, opts.containerID, 10*time.Second); err != nil {
		_ = syscall.Kill(monitorPID, syscall.SIGKILL)
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
	if err := requestContainerStart(ctx, dataRoot, containerID); err != nil {
		return err
	}
	if detach {
		_, _ = fmt.Fprintf(os.Stdout, "container started in background: %s\n", containerID)
		return nil
	}
	return waitForContainerStopped(ctx, dataRoot, containerID)
}

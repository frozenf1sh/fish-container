//go:build linux

package runtime

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const initCommandName = "__init"

// RunSpec defines the minimum configuration needed for isolated execution.
type RunSpec struct {
	Rootfs   string
	Hostname string
	Command  []string
}

// Run launches a child process in new namespaces and re-enters via __init.
func Run(spec RunSpec) error {
	if spec.Rootfs == "" {
		return errors.New("rootfs is required")
	}

	absRootfs, err := filepath.Abs(spec.Rootfs)
	if err != nil {
		return fmt.Errorf("resolve rootfs: %w", err)
	}

	if _, err := os.Stat(absRootfs); err != nil {
		return fmt.Errorf("stat rootfs: %w", err)
	}

	args := []string{initCommandName, "--rootfs", absRootfs, "--hostname", spec.Hostname, "--"}
	if len(spec.Command) == 0 {
		args = append(args, "/bin/sh")
	} else {
		args = append(args, spec.Command...)
	}

	cmd := exec.Command("/proc/self/exe", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNS,
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run isolated process: %w", err)
	}

	return nil
}

// InitCommandName is the hidden subcommand used for reexec.
func InitCommandName() string {
	return initCommandName
}

// ChildMain executes inside isolated namespaces and pivots to rootfs.
func ChildMain(args []string) error {
	fs := flag.NewFlagSet(initCommandName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rootfs string
	var hostname string
	fs.StringVar(&rootfs, "rootfs", "", "rootfs path")
	fs.StringVar(&hostname, "hostname", "fish-container", "container hostname")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse init flags: %w", err)
	}

	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"/bin/sh"}
	}

	if err := setNoNewPrivs(); err != nil {
		return err
	}

	if err := setupContainerRootfs(rootfs, hostname); err != nil {
		return err
	}

	bin, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		return fmt.Errorf("lookup command: %w", err)
	}

	if err := syscall.Exec(bin, cmdArgs, os.Environ()); err != nil {
		return fmt.Errorf("exec command: %w", err)
	}

	return nil
}

func setupContainerRootfs(rootfs, hostname string) (err error) {
	if rootfs == "" {
		return errors.New("rootfs is required")
	}

	pivoted := false
	defer func() {
		if err == nil {
			return
		}
		if pivoted {
			_ = syscall.Unmount("/proc", syscall.MNT_DETACH)
			_ = syscall.Unmount("/.old_root", syscall.MNT_DETACH)
		}
	}()

	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("sethostname: %w", err)
	}

	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("set mount propagation private: %w", err)
	}

	if err := syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind rootfs: %w", err)
	}

	putOld := filepath.Join(rootfs, ".old_root")
	if err := os.MkdirAll(putOld, 0o755); err != nil {
		return fmt.Errorf("create old_root dir: %w", err)
	}

	if err := syscall.PivotRoot(rootfs, putOld); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	pivoted = true

	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("chdir new root: %w", err)
	}

	if err := syscall.Unmount("/.old_root", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old_root: %w", err)
	}

	if err := os.RemoveAll("/.old_root"); err != nil {
		return fmt.Errorf("remove old_root: %w", err)
	}

	if err := os.MkdirAll("/proc", 0o555); err != nil {
		return fmt.Errorf("create /proc: %w", err)
	}

	if err := syscall.Mount("proc", "/proc", "proc", uintptr(0), ""); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}

	return nil
}

func setNoNewPrivs() error {
	const prSetNoNewPrivs = 38

	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, uintptr(prSetNoNewPrivs), uintptr(1), 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("set no_new_privs: %w", errno)
	}

	return nil
}

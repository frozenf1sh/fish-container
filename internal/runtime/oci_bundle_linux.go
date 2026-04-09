//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fish-container/internal/image"
	"fish-container/internal/store"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// BundleResult describes generated OCI bundle artifacts.
type BundleResult struct {
	BundlePath string
	SpecPath   string
	RootfsPath string
	Spec       specs.Spec
}

// BuildOCIBundle materializes an OCI bundle (config.json + rootfs link) from container config.
func BuildOCIBundle(ctx context.Context, dataRoot string, cfg store.ContainerConfig) (*BundleResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if cfg.ID == "" {
		return nil, fmt.Errorf("container id is required")
	}
	if cfg.Rootfs == "" {
		return nil, fmt.Errorf("container rootfs is required")
	}

	layout := image.NewLayout(dataRoot)
	bundlePath, err := filepath.Abs(layout.BundleDir(cfg.ID))
	if err != nil {
		return nil, fmt.Errorf("resolve bundle dir: %w", err)
	}
	specPath := filepath.Join(bundlePath, "config.json")
	bundleRootfs := filepath.Join(bundlePath, "rootfs")

	rootfsTarget, err := filepath.Abs(cfg.Rootfs)
	if err != nil {
		return nil, fmt.Errorf("resolve container rootfs: %w", err)
	}

	if err := os.MkdirAll(bundlePath, 0o755); err != nil {
		return nil, fmt.Errorf("create bundle dir: %w", err)
	}

	if err := os.RemoveAll(bundleRootfs); err != nil {
		return nil, fmt.Errorf("cleanup bundle rootfs path: %w", err)
	}
	if err := os.Symlink(rootfsTarget, bundleRootfs); err != nil {
		return nil, fmt.Errorf("link bundle rootfs: %w", err)
	}

	spec := buildSpec(cfg)
	body, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal oci spec: %w", err)
	}
	body = append(body, '\n')

	if err := os.WriteFile(specPath, body, 0o644); err != nil {
		return nil, fmt.Errorf("write oci spec: %w", err)
	}

	return &BundleResult{
		BundlePath: bundlePath,
		SpecPath:   specPath,
		RootfsPath: bundleRootfs,
		Spec:       spec,
	}, nil
}

// LoadBundleSpec loads OCI config.json from bundle path.
func LoadBundleSpec(bundlePath string) (*specs.Spec, error) {
	specPath := filepath.Join(bundlePath, "config.json")
	body, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read bundle spec: %w", err)
	}

	var spec specs.Spec
	if err := json.Unmarshal(body, &spec); err != nil {
		return nil, fmt.Errorf("decode bundle spec: %w", err)
	}
	if spec.Process == nil {
		return nil, fmt.Errorf("invalid bundle spec: process is required")
	}
	if spec.Root == nil {
		return nil, fmt.Errorf("invalid bundle spec: root is required")
	}

	return &spec, nil
}

// RunSpecFromOCISpec converts OCI spec payload into runtime RunSpec.
func RunSpecFromOCISpec(bundlePath string, spec *specs.Spec) (RunSpec, error) {
	if spec == nil {
		return RunSpec{}, fmt.Errorf("spec is required")
	}
	if spec.Process == nil {
		return RunSpec{}, fmt.Errorf("spec.process is required")
	}
	if spec.Root == nil {
		return RunSpec{}, fmt.Errorf("spec.root is required")
	}

	rootfsPath := spec.Root.Path
	if !filepath.IsAbs(rootfsPath) {
		rootfsPath = filepath.Join(bundlePath, rootfsPath)
	}

	command := append([]string(nil), spec.Process.Args...)
	if len(command) == 0 {
		return RunSpec{}, fmt.Errorf("spec.process.args is required")
	}

	workdir := spec.Process.Cwd
	if strings.TrimSpace(workdir) == "" {
		workdir = "/"
	}

	return RunSpec{
		Rootfs:   rootfsPath,
		Hostname: spec.Hostname,
		Command:  command,
		Env:      append([]string(nil), spec.Process.Env...),
		WorkDir:  workdir,
	}, nil
}

func buildSpec(cfg store.ContainerConfig) specs.Spec {
	args := cfg.EffectiveCommand()
	uid, gid := parseUser(cfg.User)
	cwd := cfg.WorkingDir
	if strings.TrimSpace(cwd) == "" {
		cwd = "/"
	}

	spec := specs.Spec{
		Version: specs.Version,
		Process: &specs.Process{
			Terminal: false,
			User: specs.User{
				UID: uid,
				GID: gid,
			},
			Args: args,
			Env:  append([]string(nil), cfg.Env...),
			Cwd:  cwd,
		},
		Root: &specs.Root{
			Path:     "rootfs",
			Readonly: false,
		},
		Hostname: cfg.Hostname,
		Mounts: []specs.Mount{
			{Destination: "/proc", Type: "proc", Source: "proc"},
			{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
			{Destination: "/dev/pts", Type: "devpts", Source: "devpts", Options: []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620"}},
			{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"}},
			{Destination: "/dev/mqueue", Type: "mqueue", Source: "mqueue", Options: []string{"nosuid", "noexec", "nodev"}},
			{Destination: "/sys", Type: "sysfs", Source: "sysfs", Options: []string{"nosuid", "noexec", "nodev", "ro"}},
		},
		Linux: &specs.Linux{
			CgroupsPath: cfg.CgroupsPath,
			Namespaces: []specs.LinuxNamespace{
				{Type: specs.PIDNamespace},
				{Type: specs.NetworkNamespace},
				{Type: specs.IPCNamespace},
				{Type: specs.UTSNamespace},
				{Type: specs.MountNamespace},
			},
			MaskedPaths: []string{
				"/proc/acpi",
				"/proc/asound",
				"/proc/kcore",
				"/proc/keys",
				"/proc/latency_stats",
				"/proc/timer_list",
				"/proc/timer_stats",
				"/proc/sched_debug",
				"/sys/firmware",
			},
			ReadonlyPaths: []string{
				"/proc/bus",
				"/proc/fs",
				"/proc/irq",
				"/proc/sys",
				"/proc/sysrq-trigger",
			},
		},
		Annotations: map[string]string{
			"org.opencontainers.runtime.id":     cfg.ID,
			"org.opencontainers.image.ref.name": cfg.ImageRef,
		},
	}

	return spec
}

func parseUser(value string) (uint32, uint32) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, 0
	}

	uidPart := trimmed
	gidPart := "0"
	if strings.Contains(trimmed, ":") {
		parts := strings.SplitN(trimmed, ":", 2)
		uidPart = parts[0]
		if parts[1] != "" {
			gidPart = parts[1]
		}
	}

	uid, err := strconv.ParseUint(uidPart, 10, 32)
	if err != nil {
		return 0, 0
	}
	gid, err := strconv.ParseUint(gidPart, 10, 32)
	if err != nil {
		return uint32(uid), 0
	}

	return uint32(uid), uint32(gid)
}

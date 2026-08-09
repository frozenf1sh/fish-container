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
	spec := buildSpec(cfg)
	if err := ValidateSupportedOCISpec(&spec); err != nil {
		return nil, err
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
	if err := ValidateSupportedOCISpec(spec); err != nil {
		return RunSpec{}, err
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

// ValidateSupportedOCISpec rejects every OCI field that the M0 runtime cannot
// faithfully apply. This prevents a bundle from appearing supported while its
// isolation or process settings are silently ignored.
func ValidateSupportedOCISpec(spec *specs.Spec) error {
	if spec == nil {
		return fmt.Errorf("spec is required")
	}
	if spec.Version != specs.Version {
		return fmt.Errorf("unsupported ociVersion %q, expected %q", spec.Version, specs.Version)
	}
	if spec.Process == nil {
		return fmt.Errorf("spec.process is required")
	}
	if spec.Root == nil {
		return fmt.Errorf("spec.root is required")
	}
	if spec.Root.Path != "rootfs" {
		return fmt.Errorf("unsupported OCI field: root.path (M0 requires bundle-relative rootfs)")
	}
	if spec.Root.Readonly {
		return fmt.Errorf("unsupported OCI field: root.readonly")
	}
	if spec.Process.Terminal || spec.Process.ConsoleSize != nil {
		return fmt.Errorf("unsupported OCI field: process.terminal")
	}
	if len(spec.Process.Args) == 0 {
		return fmt.Errorf("spec.process.args is required")
	}
	if !filepath.IsAbs(spec.Process.Cwd) {
		return fmt.Errorf("spec.process.cwd must be absolute")
	}
	user := spec.Process.User
	if user.UID != 0 || user.GID != 0 || user.Umask != nil || len(user.AdditionalGids) != 0 || user.Username != "" {
		return fmt.Errorf("unsupported OCI field: process.user (M0 supports only uid=0,gid=0)")
	}
	if spec.Process.Capabilities != nil {
		return fmt.Errorf("unsupported OCI field: process.capabilities")
	}
	if len(spec.Process.Rlimits) != 0 {
		return fmt.Errorf("unsupported OCI field: process.rlimits")
	}
	if !spec.Process.NoNewPrivileges {
		return fmt.Errorf("unsupported OCI field: process.noNewPrivileges (M0 requires true)")
	}
	if spec.Process.ApparmorProfile != "" || spec.Process.SelinuxLabel != "" {
		return fmt.Errorf("unsupported OCI field: process LSM profile")
	}
	if spec.Process.OOMScoreAdj != nil || spec.Process.Scheduler != nil || spec.Process.IOPriority != nil || spec.Process.ExecCPUAffinity != nil {
		return fmt.Errorf("unsupported OCI field: process scheduling or priority")
	}
	if spec.Domainname != "" {
		return fmt.Errorf("unsupported OCI field: domainname")
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Destination != "/proc" || spec.Mounts[0].Type != "proc" || spec.Mounts[0].Source != "proc" || len(spec.Mounts[0].Options) != 0 {
		return fmt.Errorf("unsupported OCI field: mounts (M0 supports only proc at /proc)")
	}
	if spec.Hooks != nil {
		return fmt.Errorf("unsupported OCI field: hooks")
	}
	if spec.Solaris != nil || spec.Windows != nil || spec.VM != nil || spec.ZOS != nil {
		return fmt.Errorf("unsupported non-Linux OCI platform configuration")
	}
	if spec.Linux == nil {
		return fmt.Errorf("spec.linux is required")
	}
	if err := validateM0LinuxSpec(spec.Linux); err != nil {
		return err
	}
	return nil
}

func validateM0LinuxSpec(linux *specs.Linux) error {
	if len(linux.UIDMappings) != 0 || len(linux.GIDMappings) != 0 {
		return fmt.Errorf("unsupported OCI field: linux user mappings")
	}
	if len(linux.Sysctl) != 0 || linux.Resources != nil || linux.CgroupsPath != "" {
		return fmt.Errorf("unsupported OCI field: linux cgroups or sysctl")
	}
	if len(linux.Devices) != 0 || linux.Seccomp != nil {
		return fmt.Errorf("unsupported OCI field: linux devices or seccomp")
	}
	if linux.RootfsPropagation != "" || linux.MountLabel != "" {
		return fmt.Errorf("unsupported OCI field: linux mount configuration")
	}
	if len(linux.MaskedPaths) != 0 || len(linux.ReadonlyPaths) != 0 {
		return fmt.Errorf("unsupported OCI field: linux maskedPaths or readonlyPaths")
	}
	if linux.IntelRdt != nil || linux.Personality != nil || len(linux.TimeOffsets) != 0 {
		return fmt.Errorf("unsupported OCI field: linux RDT, personality, or time offsets")
	}

	supported := map[specs.LinuxNamespaceType]bool{
		specs.PIDNamespace:   false,
		specs.IPCNamespace:   false,
		specs.UTSNamespace:   false,
		specs.MountNamespace: false,
	}
	for _, namespace := range linux.Namespaces {
		if namespace.Path != "" {
			return fmt.Errorf("unsupported OCI namespace path for %s", namespace.Type)
		}
		if _, ok := supported[namespace.Type]; !ok {
			return fmt.Errorf("unsupported OCI namespace type: %s", namespace.Type)
		}
		if supported[namespace.Type] {
			return fmt.Errorf("duplicate OCI namespace type: %s", namespace.Type)
		}
		supported[namespace.Type] = true
	}
	for namespaceType, present := range supported {
		if !present {
			return fmt.Errorf("required M0 namespace missing: %s", namespaceType)
		}
	}
	return nil
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
			Terminal:        false,
			NoNewPrivileges: true,
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
		},
		Linux: &specs.Linux{
			CgroupsPath: cfg.CgroupsPath,
			Namespaces: []specs.LinuxNamespace{
				{Type: specs.PIDNamespace},
				{Type: specs.IPCNamespace},
				{Type: specs.UTSNamespace},
				{Type: specs.MountNamespace},
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

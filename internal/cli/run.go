package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fish-container/internal/image"
	"fish-container/internal/runtime"
	"fish-container/internal/store"
)

type envListFlag []string

func (e *envListFlag) String() string {
	return strings.Join(*e, ",")
}

func (e *envListFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("empty env override")
	}
	*e = append(*e, value)
	return nil
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rootfs string
	var imageRef string
	var dataRoot string
	var containerID string
	var keepSnapshot bool
	var hostname string
	var workdir string
	var user string
	var envOverrides envListFlag
	fs.StringVar(&rootfs, "rootfs", "", "local rootfs path")
	fs.StringVar(&imageRef, "image", "", "image reference, e.g. alpine:latest")
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id for overlay snapshot")
	fs.BoolVar(&keepSnapshot, "keep-snapshot", false, "keep overlay snapshot after run exits")
	fs.StringVar(&hostname, "hostname", "fish-container", "container hostname")
	fs.StringVar(&workdir, "workdir", "", "override container working directory")
	fs.StringVar(&user, "user", "", "override container user")
	fs.Var(&envOverrides, "env", "override environment variable, e.g. --env KEY=VALUE")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse run flags: %w", err)
	}

	if rootfs != "" && imageRef != "" {
		return fmt.Errorf("--rootfs and --image are mutually exclusive")
	}
	if rootfs == "" && imageRef == "" {
		return fmt.Errorf("one of --rootfs or --image is required")
	}
	if containerID == "" {
		containerID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}

	runSpec := runtime.RunSpec{
		Rootfs:   rootfs,
		Hostname: hostname,
		Command:  fs.Args(),
	}
	cleanup := func() {}

	if imageRef != "" {
		ctx := context.Background()
		spec, mountedRootfs, release, err := prepareRunSpecFromImage(ctx, dataRoot, containerID, imageRef, keepSnapshot, hostname, fs.Args(), envOverrides, workdir, user)
		if err != nil {
			return err
		}
		runSpec = spec
		runSpec.Rootfs = mountedRootfs
		cleanup = release
	} else {
		runSpec.WorkDir = workdir
		runSpec.Env = mergeEnv(nil, envOverrides)
	}

	defer cleanup()

	return runtime.Run(runSpec)
}

func prepareRunSpecFromImage(
	ctx context.Context,
	dataRoot, containerID, imageRef string,
	keepSnapshot bool,
	hostname string,
	commandOverride []string,
	envOverrides []string,
	workdirOverride string,
	userOverride string,
) (runtime.RunSpec, string, func(), error) {
	cfg := image.LoadConfigFromEnv(dataRoot)
	manifestResult, err := loadOrPullManifest(ctx, cfg, imageRef)
	if err != nil {
		return runtime.RunSpec{}, "", func() {}, err
	}

	ref := manifestResult.Reference
	manifest := manifestResult.Manifest
	if len(manifest.Layers) == 0 {
		return runtime.RunSpec{}, "", func() {}, fmt.Errorf("image has no layers: %s", ref.String())
	}

	_, _ = fmt.Fprintf(os.Stdout, "preparing layers for run from %s ...\n", ref.String())
	if err := fetchLayers(ctx, cfg, ref, manifest.Layers); err != nil {
		return runtime.RunSpec{}, "", func() {}, err
	}
	if err := unpackLayers(ctx, cfg, manifest.Layers); err != nil {
		return runtime.RunSpec{}, "", func() {}, err
	}

	digests := make([]string, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		digests = append(digests, layer.Digest)
	}

	mounter := image.NewSnapshotMounter(cfg)
	mountResult, err := mounter.Mount(ctx, image.MountRequest{
		ContainerID:       containerID,
		LowerLayerDigests: digests,
	})
	if err != nil {
		return runtime.RunSpec{}, "", func() {}, err
	}
	_, _ = fmt.Fprintf(os.Stdout, "run rootfs mounted: %s\n", mountResult.MergedDir)

	containerCfg := resolveContainerConfig(store.ContainerConfig{
		ID:                  containerID,
		ImageRef:            ref.String(),
		ImageManifestDigest: manifestResult.Digest,
		ImageConfigDigest:   manifestResult.ConfigDigest,
		Rootfs:              mountResult.MergedDir,
		Entrypoint:          append([]string(nil), manifestResult.Config.Config.Entrypoint...),
		Cmd:                 append([]string(nil), manifestResult.Config.Config.Cmd...),
		Env:                 append([]string(nil), manifestResult.Config.Config.Env...),
		WorkingDir:          manifestResult.Config.Config.WorkingDir,
		User:                manifestResult.Config.Config.User,
		Hostname:            hostname,
		CreatedAt:           time.Now().UTC(),
	}, commandOverride, envOverrides, workdirOverride, userOverride)

	configStore := store.NewContainerConfigStore(dataRoot)
	configPath, err := configStore.Save(ctx, containerCfg)
	if err != nil {
		_ = mounter.Unmount(context.Background(), containerID)
		return runtime.RunSpec{}, "", func() {}, err
	}
	_, _ = fmt.Fprintf(os.Stdout, "container config: %s\n", configPath)
	_, _ = fmt.Fprintf(os.Stdout, "container command: %v\n", containerCfg.EffectiveCommand())

	cleanup := func() {
		if keepSnapshot {
			_, _ = fmt.Fprintln(os.Stdout, "snapshot preserved by --keep-snapshot")
			return
		}
		if err := mounter.Unmount(context.Background(), containerID); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warn: unmount snapshot failed: %v\n", err)
			return
		}
		_, _ = fmt.Fprintln(os.Stdout, "run snapshot cleaned")
	}

	return runtime.RunSpec{
		Rootfs:   mountResult.MergedDir,
		Hostname: containerCfg.Hostname,
		Command:  containerCfg.EffectiveCommand(),
		Env:      containerCfg.Env,
		WorkDir:  containerCfg.WorkingDir,
	}, mountResult.MergedDir, cleanup, nil
}

func resolveContainerConfig(base store.ContainerConfig, cmdOverride, envOverrides []string, workdirOverride, userOverride string) store.ContainerConfig {
	if len(cmdOverride) > 0 {
		base.Cmd = append([]string(nil), cmdOverride...)
	}
	base.Env = mergeEnv(base.Env, envOverrides)
	if workdirOverride != "" {
		base.WorkingDir = workdirOverride
	}
	if userOverride != "" {
		base.User = userOverride
	}
	return base
}

func mergeEnv(base []string, overrides []string) []string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make([]string, 0, len(base)+len(overrides))
	indexByKey := make(map[string]int, len(base)+len(overrides))

	add := func(env string) {
		key, _, ok := strings.Cut(env, "=")
		if !ok || key == "" {
			return
		}
		if idx, exists := indexByKey[key]; exists {
			result[idx] = env
			return
		}
		indexByKey[key] = len(result)
		result = append(result, env)
	}

	for _, env := range base {
		add(env)
	}
	for _, env := range overrides {
		add(env)
	}

	return result
}

func loadOrPullManifest(ctx context.Context, cfg image.Config, imageRef string) (*image.ManifestResult, error) {
	ref, err := image.ParseReference(imageRef)
	if err != nil {
		return nil, err
	}

	layout := image.NewLayout(cfg.DataRoot)
	manifestPath := layout.ManifestPath(ref.Registry, ref.Repository, ref.Tag)
	if body, err := os.ReadFile(manifestPath); err == nil {
		var manifest image.Schema2Manifest
		if err := json.Unmarshal(body, &manifest); err == nil {
			configDigest := manifest.Config.Digest
			configDigestHex, err := image.DigestHexFromSHA256(configDigest)
			if err != nil {
				return nil, fmt.Errorf("invalid local config digest: %w", err)
			}

			configPath := layout.ConfigPath(configDigestHex)
			configBody, err := os.ReadFile(configPath)
			if err == nil {
				var imageCfg image.ImageConfig
				if err := json.Unmarshal(configBody, &imageCfg); err == nil {
					_, _ = fmt.Fprintf(os.Stdout, "using local manifest: %s\n", manifestPath)
					_, _ = fmt.Fprintf(os.Stdout, "using local config: %s\n", configPath)
					return &image.ManifestResult{
						Reference:    ref,
						Manifest:     manifest,
						Digest:       readLocalDigest(layout.RefPath(ref.Registry, ref.Repository, ref.Tag)),
						ManifestPath: manifestPath,
						RefPath:      layout.RefPath(ref.Registry, ref.Repository, ref.Tag),
						ConfigDigest: configDigest,
						ConfigPath:   configPath,
						Config:       imageCfg,
					}, nil
				}
			}

			_, _ = fmt.Fprintf(os.Stdout, "local config missing, repulling manifest: %s\n", imageRef)
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "manifest not found locally, pulling %s ...\n", imageRef)
	puller := image.NewManifestPuller(cfg)
	result, err := puller.PullManifest(ctx, imageRef)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func readLocalDigest(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

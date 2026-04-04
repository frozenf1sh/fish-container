package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"fish-container/internal/image"
	"fish-container/internal/runtime"
)

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rootfs string
	var imageRef string
	var dataRoot string
	var containerID string
	var keepSnapshot bool
	var hostname string
	fs.StringVar(&rootfs, "rootfs", "", "local rootfs path")
	fs.StringVar(&imageRef, "image", "", "image reference, e.g. alpine:latest")
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id for overlay snapshot")
	fs.BoolVar(&keepSnapshot, "keep-snapshot", false, "keep overlay snapshot after run exits")
	fs.StringVar(&hostname, "hostname", "fish-container", "container hostname")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse run flags: %w", err)
	}

	if rootfs != "" && imageRef != "" {
		return fmt.Errorf("--rootfs and --image are mutually exclusive")
	}
	if rootfs == "" && imageRef == "" {
		return fmt.Errorf("one of --rootfs or --image is required")
	}

	if imageRef != "" {
		ctx := context.Background()
		resolvedRootfs, cleanup, err := prepareRootfsFromImage(ctx, dataRoot, containerID, imageRef, keepSnapshot)
		if err != nil {
			return err
		}
		defer cleanup()
		rootfs = resolvedRootfs
	}

	return runtime.Run(runtime.RunSpec{
		Rootfs:   rootfs,
		Hostname: hostname,
		Command:  fs.Args(),
	})
}

func prepareRootfsFromImage(ctx context.Context, dataRoot, containerID, imageRef string, keepSnapshot bool) (string, func(), error) {
	if containerID == "" {
		containerID = "run-default"
	}

	cfg := image.LoadConfigFromEnv(dataRoot)
	ref, manifest, err := loadOrPullManifest(ctx, cfg, imageRef)
	if err != nil {
		return "", func() {}, err
	}

	if len(manifest.Layers) == 0 {
		return "", func() {}, fmt.Errorf("image has no layers: %s", ref.String())
	}

	_, _ = fmt.Fprintf(os.Stdout, "preparing layers for run from %s ...\n", ref.String())
	if err := fetchLayers(ctx, cfg, ref, manifest.Layers); err != nil {
		return "", func() {}, err
	}
	if err := unpackLayers(ctx, cfg, manifest.Layers); err != nil {
		return "", func() {}, err
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
		return "", func() {}, err
	}
	_, _ = fmt.Fprintf(os.Stdout, "run rootfs mounted: %s\n", mountResult.MergedDir)

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

	return mountResult.MergedDir, cleanup, nil
}

func loadOrPullManifest(ctx context.Context, cfg image.Config, imageRef string) (image.Reference, image.Schema2Manifest, error) {
	ref, err := image.ParseReference(imageRef)
	if err != nil {
		return image.Reference{}, image.Schema2Manifest{}, err
	}

	layout := image.NewLayout(cfg.DataRoot)
	manifestPath := layout.ManifestPath(ref.Registry, ref.Repository, ref.Tag)
	if body, err := os.ReadFile(manifestPath); err == nil {
		var manifest image.Schema2Manifest
		if err := json.Unmarshal(body, &manifest); err == nil {
			_, _ = fmt.Fprintf(os.Stdout, "using local manifest: %s\n", manifestPath)
			return ref, manifest, nil
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "manifest not found locally, pulling %s ...\n", imageRef)
	puller := image.NewManifestPuller(cfg)
	result, err := puller.PullManifest(ctx, imageRef)
	if err != nil {
		return image.Reference{}, image.Schema2Manifest{}, err
	}

	return result.Reference, result.Manifest, nil
}

//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"fish-container/internal/image"
)

func imagesCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fish-container images <mount|unmount> ...")
	}

	sub := args[0]
	switch sub {
	case "mount":
		return imagesMountCommand(args[1:])
	case "unmount":
		return imagesUnmountCommand(args[1:])
	default:
		return fmt.Errorf("unknown images subcommand: %s", sub)
	}
}

func imagesMountCommand(args []string) error {
	fs := flag.NewFlagSet("images mount", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if containerID == "" || fs.NArg() != 1 {
		return fmt.Errorf("usage: fish-container images mount --container <id> [--data-root PATH] <image:tag>")
	}

	refText := fs.Arg(0)
	ref, err := image.ParseReference(refText)
	if err != nil {
		return err
	}

	layout := image.NewLayout(dataRoot)
	manifestPath := layout.ManifestPath(ref.Registry, ref.Repository, ref.Tag)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest from store: %w", err)
	}

	var manifest image.Schema2Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}

	digests := make([]string, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		digests = append(digests, layer.Digest)
	}

	mounter := image.NewSnapshotMounter(image.LoadConfigFromEnv(dataRoot))
	result, err := mounter.Mount(context.Background(), image.MountRequest{
		ContainerID:       containerID,
		LowerLayerDigests: digests,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "overlay mounted: %s\n", result.MergedDir)
	_, _ = fmt.Fprintf(os.Stdout, "upper: %s\n", result.UpperDir)
	_, _ = fmt.Fprintf(os.Stdout, "work: %s\n", result.WorkDir)
	return nil
}

func imagesUnmountCommand(args []string) error {
	fs := flag.NewFlagSet("images unmount", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	var containerID string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")
	fs.StringVar(&containerID, "container", "", "container id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if containerID == "" {
		return fmt.Errorf("usage: fish-container images unmount --container <id> [--data-root PATH]")
	}

	mounter := image.NewSnapshotMounter(image.LoadConfigFromEnv(dataRoot))
	if err := mounter.Unmount(context.Background(), containerID); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(os.Stdout, "overlay unmounted and cleaned")
	return nil
}

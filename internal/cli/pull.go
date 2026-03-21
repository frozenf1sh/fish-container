package cli

import (
	"context"
	"flag"
	"fmt"
	"os"

	"fish-container/internal/image"
)

func pullCommand(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var dataRoot string
	fs.StringVar(&dataRoot, "data-root", "/var/lib/fish-container", "runtime data root")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse pull flags: %w", err)
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: fish-container pull [--data-root PATH] <image:tag>")
	}

	ref := fs.Arg(0)
	cfg := image.LoadConfigFromEnv(dataRoot)
	puller := image.NewManifestPuller(cfg)

	result, err := puller.PullManifest(context.Background(), ref)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "pulled manifest: %s\n", result.Reference.String())
	_, _ = fmt.Fprintf(os.Stdout, "digest: %s\n", result.Digest)
	_, _ = fmt.Fprintf(os.Stdout, "manifest: %s\n", result.ManifestPath)
	_, _ = fmt.Fprintf(os.Stdout, "ref: %s\n", result.RefPath)

	return nil
}

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"

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
	_, _ = fmt.Fprintf(os.Stdout, "pulling manifest for %s ...\n", ref)
	cfg := image.LoadConfigFromEnv(dataRoot)
	if cfg.RegistryMirror != "" {
		_, _ = fmt.Fprintf(os.Stdout, "using mirror: %s\n", cfg.RegistryMirror)
	}
	puller := image.NewManifestPuller(cfg)

	result, err := puller.PullManifest(context.Background(), ref)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "pulled manifest: %s\n", result.Reference.String())
	_, _ = fmt.Fprintf(os.Stdout, "digest: %s\n", result.Digest)
	_, _ = fmt.Fprintf(os.Stdout, "manifest: %s\n", result.ManifestPath)
	_, _ = fmt.Fprintf(os.Stdout, "ref: %s\n", result.RefPath)

	if len(result.Manifest.Layers) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no layer to download")
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "downloading %d layers ...\n", len(result.Manifest.Layers))
	if err := fetchLayers(context.Background(), cfg, result.Reference, result.Manifest.Layers); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, "layers downloaded")

	return nil
}

func fetchLayers(ctx context.Context, cfg image.Config, ref image.Reference, layers []image.Descriptor) error {
	fetcher := image.NewBlobFetcher(cfg)

	workers := 3
	if len(layers) < workers {
		workers = len(layers)
	}
	if workers == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type task struct {
		index int
		desc  image.Descriptor
	}

	tasks := make(chan task)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var outMu sync.Mutex

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				path, err := fetcher.FetchBlob(ctx, ref, t.desc)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("layer %d %s: %w", t.index+1, t.desc.Digest, err):
						cancel()
					default:
					}
					return
				}

				outMu.Lock()
				_, _ = fmt.Fprintf(os.Stdout, "[%d/%d] layer ok %s -> %s\n", t.index+1, len(layers), t.desc.Digest, path)
				outMu.Unlock()
			}
		}()
	}

dispatch:
	for i, layer := range layers {
		select {
		case <-ctx.Done():
			break dispatch
		case tasks <- task{index: i, desc: layer}:
		}
	}
	close(tasks)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

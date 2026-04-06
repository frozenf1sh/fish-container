package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
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
	_, _ = fmt.Fprintf(os.Stdout, "config digest: %s\n", result.ConfigDigest)
	_, _ = fmt.Fprintf(os.Stdout, "config: %s\n", result.ConfigPath)
	_, _ = fmt.Fprintf(os.Stdout, "config entrypoint: %v\n", result.Config.Config.Entrypoint)
	_, _ = fmt.Fprintf(os.Stdout, "config cmd: %v\n", result.Config.Config.Cmd)
	_, _ = fmt.Fprintf(os.Stdout, "config env count: %d\n", len(result.Config.Config.Env))

	if len(result.Manifest.Layers) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no layer to download")
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "downloading %d layers ...\n", len(result.Manifest.Layers))
	if err := fetchLayers(context.Background(), cfg, result.Reference, result.Manifest.Layers); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, "layers downloaded")

	_, _ = fmt.Fprintf(os.Stdout, "unpacking %d layers ...\n", len(result.Manifest.Layers))
	if err := unpackLayers(context.Background(), cfg, result.Manifest.Layers); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, "layers unpacked")

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
				lastStep := -1
				path, err := fetcher.FetchBlob(ctx, ref, t.desc, func(current, total int64) {
					step, line := renderProgressLine("downloading", t.index+1, len(layers), t.desc.Digest, current, total, &lastStep)
					if !step {
						return
					}
					outMu.Lock()
					_, _ = fmt.Fprintln(os.Stdout, line)
					outMu.Unlock()
				})
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

func unpackLayers(ctx context.Context, cfg image.Config, layers []image.Descriptor) error {
	unpacker := image.NewLayerUnpacker(cfg)
	for i, layer := range layers {
		lastStep := -1
		path, err := unpacker.UnpackLayer(ctx, layer, func(current, total int64) {
			step, line := renderProgressLine("unpacking", i+1, len(layers), layer.Digest, current, total, &lastStep)
			if !step {
				return
			}
			_, _ = fmt.Fprintln(os.Stdout, line)
		})
		if err != nil {
			return fmt.Errorf("unpack layer %d %s: %w", i+1, layer.Digest, err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "[%d/%d] layer unpacked %s -> %s\n", i+1, len(layers), layer.Digest, path)
	}

	return nil
}

func renderProgressLine(stage string, index, total int, digest string, current, totalBytes int64, lastStep *int) (bool, string) {
	if totalBytes <= 0 {
		return false, ""
	}

	percent := int((current * 100) / totalBytes)
	if percent > 100 {
		percent = 100
	}
	step := percent / 10
	if step <= *lastStep && percent < 100 {
		return false, ""
	}
	*lastStep = step

	bar := progressBar(percent)
	line := fmt.Sprintf("[%d/%d] %s %s %3d%% %s/%s %s", index, total, stage, shortDigest(digest), percent, formatBytes(current), formatBytes(totalBytes), bar)
	return true, line
}

func shortDigest(digest string) string {
	if len(digest) <= 20 {
		return digest
	}
	return digest[:20] + "..."
}

func progressBar(percent int) string {
	filled := percent / 10
	if filled > 10 {
		filled = 10
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", 10-filled) + "]"
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

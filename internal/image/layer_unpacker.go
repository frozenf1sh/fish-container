package image

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type layerUnpacker struct {
	layout Layout
}

func NewLayerUnpacker(cfg Config) LayerUnpacker {
	return &layerUnpacker{layout: NewLayout(cfg.DataRoot)}
}

func (u *layerUnpacker) UnpackLayer(_ context.Context, descriptor Descriptor) (string, error) {
	digestHex, err := digestHexFromSHA256(descriptor.Digest)
	if err != nil {
		return "", err
	}

	blobPath := u.layout.BlobPath(digestHex)
	if _, err := os.Stat(blobPath); err != nil {
		return "", fmt.Errorf("blob missing before unpack: %w", err)
	}

	targetDir := u.layout.UnpackedPath(digestHex)
	markerPath := filepath.Join(targetDir, ".unpacked")
	if _, err := os.Stat(markerPath); err == nil {
		return targetDir, nil
	}

	if err := os.RemoveAll(targetDir); err != nil {
		return "", fmt.Errorf("cleanup old unpack dir: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create unpack dir: %w", err)
	}

	if err := unpackTarArchive(blobPath, targetDir); err != nil {
		return "", err
	}

	if err := os.WriteFile(markerPath, []byte(descriptor.Digest+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write unpack marker: %w", err)
	}

	return targetDir, nil
}

func unpackTarArchive(blobPath, targetDir string) error {
	file, err := os.Open(blobPath)
	if err != nil {
		return fmt.Errorf("open blob file: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	tarReader, err := newTarReader(reader)
	if err != nil {
		return err
	}

	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}

		targetPath, err := safeJoin(targetDir, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("create dir %s: %w", targetPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create parent dir %s: %w", targetPath, err)
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(out, tarReader); err != nil {
				_ = out.Close()
				return fmt.Errorf("write file %s: %w", targetPath, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close file %s: %w", targetPath, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create symlink parent %s: %w", targetPath, err)
			}
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create symlink %s: %w", targetPath, err)
			}
		case tar.TypeLink:
			linkTarget, err := safeJoin(targetDir, hdr.Linkname)
			if err != nil {
				return err
			}
			if err := os.Link(linkTarget, targetPath); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create hard link %s: %w", targetPath, err)
			}
		default:
			// Ignore non-critical types in minimal implementation.
		}
	}

	return nil
}

func newTarReader(reader *bufio.Reader) (*tar.Reader, error) {
	magic, err := reader.Peek(2)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("peek blob magic: %w", err)
	}

	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("open gzip layer: %w", err)
		}
		return tar.NewReader(gzReader), nil
	}

	return tar.NewReader(reader), nil
}

func safeJoin(root, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." {
		return root, nil
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid tar entry path: %s", name)
	}

	joined := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, joined)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return "", fmt.Errorf("tar entry escapes root: %s", name)
	}

	return joined, nil
}

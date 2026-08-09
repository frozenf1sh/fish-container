package image

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	whiteoutPrefix = ".wh."
	opaqueWhiteout = ".wh..wh..opq"
)

type layerUnpacker struct {
	layout Layout
}

func NewLayerUnpacker(cfg Config) LayerUnpacker {
	return &layerUnpacker{layout: NewLayout(cfg.DataRoot)}
}

func (u *layerUnpacker) UnpackLayer(ctx context.Context, descriptor Descriptor, expectedDiffID string, progress ProgressFunc) (string, error) {
	digestHex, err := digestHexFromSHA256(descriptor.Digest)
	if err != nil {
		return "", err
	}

	blobPath := u.layout.BlobPath(digestHex)
	if _, err := os.Stat(blobPath); err != nil {
		return "", fmt.Errorf("blob missing before unpack: %w", err)
	}

	targetDir := u.layout.UnpackedPath(digestHex)
	markerPath := targetDir + ".unpacked"
	if _, err := os.Stat(markerPath); err == nil {
		if info, targetErr := os.Stat(targetDir); targetErr == nil && info.IsDir() {
			return targetDir, nil
		}
		_ = os.Remove(markerPath)
	}

	if err := os.RemoveAll(targetDir); err != nil {
		return "", fmt.Errorf("cleanup old unpack dir: %w", err)
	}
	_ = os.Remove(markerPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create unpack dir: %w", err)
	}

	actualDiffID, err := unpackTarArchive(ctx, blobPath, targetDir, progress)
	if err != nil {
		_ = os.RemoveAll(targetDir)
		return "", err
	}
	if expectedDiffID != "" && expectedDiffID != actualDiffID {
		_ = os.RemoveAll(targetDir)
		return "", fmt.Errorf("layer diff_id mismatch digest=%s expected=%s got=%s", descriptor.Digest, expectedDiffID, actualDiffID)
	}

	if err := os.WriteFile(markerPath, []byte(descriptor.Digest+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(targetDir)
		return "", fmt.Errorf("write unpack marker: %w", err)
	}

	return targetDir, nil
}

type directoryMetadata struct {
	path   string
	header tar.Header
}

func unpackTarArchive(ctx context.Context, blobPath, targetDir string, progress ProgressFunc) (string, error) {
	file, err := os.Open(blobPath)
	if err != nil {
		return "", fmt.Errorf("open blob file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat blob file: %w", err)
	}

	reader := bufio.NewReader(newProgressReader(file, stat.Size(), progress))
	tarReader, hasher, err := newTarReader(reader)
	if err != nil {
		return "", err
	}

	seen := make(map[string]struct{})
	var directories []directoryMetadata
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar header: %w", err)
		}

		cleanName, err := cleanArchivePath(hdr.Name)
		if err != nil {
			return "", err
		}
		if _, ok := seen[cleanName]; ok {
			return "", fmt.Errorf("duplicate tar entry: %s", hdr.Name)
		}
		seen[cleanName] = struct{}{}

		targetPath, err := secureTargetPath(targetDir, cleanName)
		if err != nil {
			return "", err
		}
		if err := ensureSecureParents(targetDir, cleanName); err != nil {
			return "", err
		}

		base := filepath.Base(cleanName)
		if strings.HasPrefix(base, whiteoutPrefix) {
			if err := applyWhiteout(targetDir, cleanName, targetPath); err != nil {
				return "", err
			}
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if info, err := os.Lstat(targetPath); err == nil {
				if !info.IsDir() {
					return "", fmt.Errorf("directory entry conflicts with existing path: %s", hdr.Name)
				}
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("lstat directory %s: %w", targetPath, err)
			} else if err := os.Mkdir(targetPath, 0o700); err != nil {
				return "", fmt.Errorf("create dir %s: %w", targetPath, err)
			}
			directories = append(directories, directoryMetadata{path: targetPath, header: *hdr})
		case tar.TypeReg, tar.TypeRegA:
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return "", fmt.Errorf("create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(out, tarReader); err != nil {
				_ = out.Close()
				return "", fmt.Errorf("write file %s: %w", targetPath, err)
			}
			if err := out.Close(); err != nil {
				return "", fmt.Errorf("close file %s: %w", targetPath, err)
			}
			if err := applyEntryMetadata(targetPath, hdr, false); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil {
				return "", fmt.Errorf("create symlink %s: %w", targetPath, err)
			}
			if err := applyEntryMetadata(targetPath, hdr, true); err != nil {
				return "", err
			}
		case tar.TypeLink:
			linkName, err := cleanArchivePath(hdr.Linkname)
			if err != nil {
				return "", fmt.Errorf("invalid hardlink target: %w", err)
			}
			linkTarget, err := secureTargetPath(targetDir, linkName)
			if err != nil {
				return "", err
			}
			info, err := os.Lstat(linkTarget)
			if err != nil {
				return "", fmt.Errorf("hardlink target %s: %w", hdr.Linkname, err)
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("hardlink target is not a regular file: %s", hdr.Linkname)
			}
			if err := os.Link(linkTarget, targetPath); err != nil {
				return "", fmt.Errorf("create hardlink %s: %w", targetPath, err)
			}
			if err := applyEntryMetadata(targetPath, hdr, false); err != nil {
				return "", err
			}
		case tar.TypeFifo, tar.TypeChar, tar.TypeBlock:
			if err := createSpecialFile(targetPath, hdr); err != nil {
				return "", err
			}
			if err := applyEntryMetadata(targetPath, hdr, false); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported tar entry type %d for %s", hdr.Typeflag, hdr.Name)
		}
	}

	for i := len(directories) - 1; i >= 0; i-- {
		entry := directories[i]
		if err := applyEntryMetadata(entry.path, &entry.header, false); err != nil {
			return "", err
		}
	}

	actualDiffID := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	return actualDiffID, nil
}

func applyWhiteout(root, cleanName, targetPath string) error {
	base := filepath.Base(cleanName)
	if base == whiteoutPrefix {
		return fmt.Errorf("invalid whiteout entry: %s", cleanName)
	}
	if base == opaqueWhiteout {
		dir := filepath.Dir(targetPath)
		if err := markDirectoryOpaque(dir); err != nil {
			return fmt.Errorf("apply opaque whiteout %s: %w", cleanName, err)
		}
		return nil
	}

	if _, err := secureTargetPath(root, cleanName); err != nil {
		return err
	}
	if err := createWhiteout(targetPath); err != nil {
		return fmt.Errorf("apply whiteout %s: %w", cleanName, err)
	}
	return nil
}

func applyEntryMetadata(path string, hdr *tar.Header, symlink bool) error {
	if err := os.Lchown(path, hdr.Uid, hdr.Gid); err != nil {
		return fmt.Errorf("lchown %s to %d:%d: %w", path, hdr.Uid, hdr.Gid, err)
	}
	if !symlink {
		if err := os.Chmod(path, hdr.FileInfo().Mode()); err != nil {
			return fmt.Errorf("chmod %s: %w", path, err)
		}
	}
	for name, value := range hdr.Xattrs {
		if strings.HasPrefix(name, "trusted.overlay.") || strings.HasPrefix(name, "user.overlay.") {
			return fmt.Errorf("reserved overlay xattr %q on %s", name, hdr.Name)
		}
		if err := setPathXattr(path, name, []byte(value), symlink); err != nil {
			return fmt.Errorf("set xattr %s on %s: %w", name, path, err)
		}
	}
	if !symlink && !hdr.ModTime.IsZero() {
		if err := os.Chtimes(path, hdr.ModTime, hdr.ModTime); err != nil {
			return fmt.Errorf("set timestamps on %s: %w", path, err)
		}
	}
	return nil
}

func cleanArchivePath(name string) (string, error) {
	cleaned := filepath.Clean(name)
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." || cleaned == "" {
		return ".", nil
	}
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid tar entry path: %s", name)
	}
	return cleaned, nil
}

func secureTargetPath(root, cleanName string) (string, error) {
	target := filepath.Join(root, cleanName)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tar entry escapes root: %s", cleanName)
	}

	current := root
	parts := strings.Split(cleanName, string(filepath.Separator))
	for _, part := range parts[:max(0, len(parts)-1)] {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect tar path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("tar entry traverses symlink: %s", cleanName)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("tar entry parent is not a directory: %s", cleanName)
		}
	}
	return target, nil
}

func ensureSecureParents(root, cleanName string) error {
	parent := filepath.Dir(cleanName)
	if parent == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("create parent dir %s: %w", current, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect parent dir %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tar entry traverses symlink: %s", cleanName)
		}
		if !info.IsDir() {
			return fmt.Errorf("tar entry parent is not a directory: %s", cleanName)
		}
	}
	return nil
}

func newTarReader(reader *bufio.Reader) (*tar.Reader, hash.Hash, error) {
	magic, err := reader.Peek(2)
	if err != nil && err != io.EOF {
		return nil, nil, fmt.Errorf("peek blob magic: %w", err)
	}

	hasher := sha256.New()
	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("open gzip layer: %w", err)
		}
		return tar.NewReader(io.TeeReader(gzReader, hasher)), hasher, nil
	}
	return tar.NewReader(io.TeeReader(reader, hasher)), hasher, nil
}

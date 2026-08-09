package image

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackLayerValidatesDiffID(t *testing.T) {
	t.Parallel()

	cfg := LoadConfigFromEnv(t.TempDir())
	layout := NewLayout(cfg.DataRoot)

	digestHex := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	descriptor := Descriptor{
		Digest: "sha256:" + digestHex,
		Size:   0,
	}

	blobPath := layout.BlobPath(digestHex)
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		t.Fatalf("mkdir blob dir: %v", err)
	}

	payload, diffID := makeTarPayload(t)
	if err := os.WriteFile(blobPath, payload, 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}

	unpacker := NewLayerUnpacker(cfg)
	if _, err := unpacker.UnpackLayer(context.Background(), descriptor, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil); err == nil {
		t.Fatalf("expected diff_id mismatch error")
	}

	targetDir := layout.UnpackedPath(digestHex)
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("target dir should be removed after mismatch")
	}

	if _, err := unpacker.UnpackLayer(context.Background(), descriptor, diffID, nil); err != nil {
		t.Fatalf("unpack with valid diff_id: %v", err)
	}

	if _, err := os.Stat(targetDir + ".unpacked"); err != nil {
		t.Fatalf("expected unpack marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, ".unpacked")); !os.IsNotExist(err) {
		t.Fatalf("unpack marker must not be visible in rootfs")
	}
}

func makeTarPayload(t *testing.T) ([]byte, string) {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("hello-layer\n")
	hdr := &tar.Header{
		Name: "hello.txt",
		Mode: 0o644,
		Uid:  os.Geteuid(),
		Gid:  os.Getegid(),
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	hash := sha256DigestHex(buf.Bytes())
	return buf.Bytes(), fmt.Sprintf("sha256:%s", hash)
}

func sha256DigestHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func TestUnpackRejectsSymlinkTraversal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	tarPath := filepath.Join(t.TempDir(), "layer.tar")
	writeTarEntries(t, tarPath, []tarTestEntry{
		{header: tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: outside, Uid: os.Geteuid(), Gid: os.Getegid()}},
		{header: tar.Header{Name: "escape/pwned", Typeflag: tar.TypeReg, Mode: 0o644, Uid: os.Geteuid(), Gid: os.Getegid()}, body: []byte("bad")},
	})

	if _, err := unpackTarArchive(context.Background(), tarPath, root, nil); err == nil {
		t.Fatal("expected symlink traversal to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Fatalf("archive wrote outside extraction root")
	}
}

func TestUnpackRejectsDuplicateEntries(t *testing.T) {
	t.Parallel()

	tarPath := filepath.Join(t.TempDir(), "layer.tar")
	entry := tarTestEntry{header: tar.Header{Name: "same", Typeflag: tar.TypeReg, Mode: 0o644, Uid: os.Geteuid(), Gid: os.Getegid()}, body: []byte("x")}
	writeTarEntries(t, tarPath, []tarTestEntry{entry, entry})
	if _, err := unpackTarArchive(context.Background(), tarPath, t.TempDir(), nil); err == nil {
		t.Fatal("expected duplicate entry to be rejected")
	}
}

type tarTestEntry struct {
	header tar.Header
	body   []byte
}

func writeTarEntries(t *testing.T, path string, entries []tarTestEntry) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tar: %v", err)
	}
	tw := tar.NewWriter(file)
	for _, entry := range entries {
		header := entry.header
		header.Size = int64(len(entry.body))
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatalf("write tar header %s: %v", header.Name, err)
		}
		if len(entry.body) > 0 {
			if _, err := tw.Write(entry.body); err != nil {
				t.Fatalf("write tar body %s: %v", header.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close tar file: %v", err)
	}
}

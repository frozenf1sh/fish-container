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

	if _, err := os.Stat(filepath.Join(targetDir, ".unpacked")); err != nil {
		t.Fatalf("expected unpack marker: %v", err)
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

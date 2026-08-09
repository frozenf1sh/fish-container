//go:build linux

package image

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnpackPreservesOwnershipModeAndXattrs(t *testing.T) {
	t.Parallel()

	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid, gid = 1234, 1234
	}
	tarPath := filepath.Join(t.TempDir(), "attrs.tar")
	writeTarEntries(t, tarPath, []tarTestEntry{{
		header: tar.Header{
			Name:     "data.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o640,
			Uid:      uid,
			Gid:      gid,
			Xattrs:   map[string]string{"user.fish-container.test": "ok"},
		},
		body: []byte("payload"),
	}})

	root := t.TempDir()
	if _, err := unpackTarArchive(context.Background(), tarPath, root, nil); err != nil {
		t.Fatalf("unpack attrs: %v", err)
	}
	path := filepath.Join(root, "data.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat extracted file: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("unexpected mode: %o", info.Mode().Perm())
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatalf("stat ownership: %v", err)
	}
	if int(stat.Uid) != uid || int(stat.Gid) != gid {
		t.Fatalf("unexpected ownership: %d:%d", stat.Uid, stat.Gid)
	}
	value := make([]byte, 16)
	n, err := unix.Getxattr(path, "user.fish-container.test", value)
	if err != nil {
		t.Fatalf("get xattr: %v", err)
	}
	if string(value[:n]) != "ok" {
		t.Fatalf("unexpected xattr value: %q", value[:n])
	}
}

func TestUnpackCreatesFIFO(t *testing.T) {
	t.Parallel()

	tarPath := filepath.Join(t.TempDir(), "fifo.tar")
	writeTarEntries(t, tarPath, []tarTestEntry{{header: tar.Header{
		Name: "run/events", Typeflag: tar.TypeFifo, Mode: 0o600, Uid: os.Geteuid(), Gid: os.Getegid(),
	}}})
	root := t.TempDir()
	if _, err := unpackTarArchive(context.Background(), tarPath, root, nil); err != nil {
		t.Fatalf("unpack fifo: %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "run/events"))
	if err != nil {
		t.Fatalf("lstat fifo: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("expected fifo, got mode %v", info.Mode())
	}
}

func TestUnpackConvertsOverlayWhiteouts(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("trusted overlay xattrs and device nodes require root")
	}

	tarPath := filepath.Join(t.TempDir(), "whiteouts.tar")
	writeTarEntries(t, tarPath, []tarTestEntry{
		{header: tar.Header{Name: "etc", Typeflag: tar.TypeDir, Mode: 0o755}},
		{header: tar.Header{Name: "etc/.wh.deleted", Typeflag: tar.TypeReg, Mode: 0o600}},
		{header: tar.Header{Name: "opt", Typeflag: tar.TypeDir, Mode: 0o755}},
		{header: tar.Header{Name: "opt/.wh..wh..opq", Typeflag: tar.TypeReg, Mode: 0o600}},
	})
	root := t.TempDir()
	if _, err := unpackTarArchive(context.Background(), tarPath, root, nil); err != nil {
		t.Fatalf("unpack whiteouts: %v", err)
	}

	var stat unix.Stat_t
	if err := unix.Lstat(filepath.Join(root, "etc/.wh.deleted"), &stat); err != nil {
		t.Fatalf("lstat whiteout: %v", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFCHR || stat.Rdev != 0 {
		t.Fatalf("whiteout is not char device 0:0: mode=%o rdev=%d", stat.Mode, stat.Rdev)
	}
	value := make([]byte, 8)
	n, err := unix.Getxattr(filepath.Join(root, "opt"), "trusted.overlay.opaque", value)
	if err != nil {
		t.Fatalf("get opaque xattr: %v", err)
	}
	if string(value[:n]) != "y" {
		t.Fatalf("unexpected opaque xattr: %q", value[:n])
	}
}

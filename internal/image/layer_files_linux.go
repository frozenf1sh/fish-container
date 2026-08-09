//go:build linux

package image

import (
	"archive/tar"
	"fmt"

	"golang.org/x/sys/unix"
)

func createWhiteout(path string) error {
	return unix.Mknod(path, unix.S_IFCHR|0o600, int(unix.Mkdev(0, 0)))
}

func markDirectoryOpaque(path string) error {
	return unix.Setxattr(path, "trusted.overlay.opaque", []byte("y"), 0)
}

func createSpecialFile(path string, hdr *tar.Header) error {
	switch hdr.Typeflag {
	case tar.TypeFifo:
		if err := unix.Mkfifo(path, uint32(hdr.Mode)); err != nil {
			return fmt.Errorf("create fifo %s: %w", path, err)
		}
	case tar.TypeChar:
		if err := unix.Mknod(path, unix.S_IFCHR|uint32(hdr.Mode), int(unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor)))); err != nil {
			return fmt.Errorf("create char device %s: %w", path, err)
		}
	case tar.TypeBlock:
		if err := unix.Mknod(path, unix.S_IFBLK|uint32(hdr.Mode), int(unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor)))); err != nil {
			return fmt.Errorf("create block device %s: %w", path, err)
		}
	default:
		return fmt.Errorf("unsupported special file type %d for %s", hdr.Typeflag, hdr.Name)
	}
	return nil
}

func setPathXattr(path, name string, value []byte, symlink bool) error {
	if symlink {
		return unix.Lsetxattr(path, name, value, 0)
	}
	return unix.Setxattr(path, name, value, 0)
}

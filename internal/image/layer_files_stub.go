//go:build !linux

package image

import (
	"archive/tar"
	"fmt"
)

func createWhiteout(_ string) error {
	return fmt.Errorf("OCI whiteouts require Linux")
}

func markDirectoryOpaque(_ string) error {
	return fmt.Errorf("OCI opaque whiteouts require Linux")
}

func createSpecialFile(_ string, hdr *tar.Header) error {
	return fmt.Errorf("special tar entry type %d requires Linux", hdr.Typeflag)
}

func setPathXattr(_, name string, _ []byte, _ bool) error {
	return fmt.Errorf("xattr %q requires Linux", name)
}

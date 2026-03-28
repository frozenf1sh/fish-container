package image

import (
	"fmt"
	"strings"
)

func digestHexFromSHA256(digest string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(digest), ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return "", fmt.Errorf("unsupported digest format: %s", digest)
	}
	if len(parts[1]) != 64 {
		return "", fmt.Errorf("invalid sha256 digest length: %s", digest)
	}

	return parts[1], nil
}

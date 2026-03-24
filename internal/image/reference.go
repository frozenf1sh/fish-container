package image

import (
	"fmt"
	"strings"
)

const (
	defaultDockerRegistry = "registry-1.docker.io"
	defaultLibraryNS      = "library"
	defaultTag            = "latest"
)

// Reference is a normalized image reference.
type Reference struct {
	Registry   string
	Repository string
	Tag        string
}

func (r Reference) String() string {
	return fmt.Sprintf("%s/%s:%s", r.Registry, r.Repository, r.Tag)
}

func ParseReference(input string) (Reference, error) {
	ref := strings.TrimSpace(input)
	if ref == "" {
		return Reference{}, fmt.Errorf("image reference is required")
	}

	registry := defaultDockerRegistry
	nameTag := ref

	parts := strings.Split(ref, "/")
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registry = strings.ToLower(parts[0])
		nameTag = strings.Join(parts[1:], "/")
	}

	repo := nameTag
	tag := defaultTag
	if idx := strings.LastIndex(nameTag, ":"); idx > -1 {
		repo = nameTag[:idx]
		tag = nameTag[idx+1:]
	}

	if !strings.Contains(repo, "/") {
		repo = defaultLibraryNS + "/" + repo
	}

	if strings.TrimSpace(repo) == "" || strings.TrimSpace(tag) == "" {
		return Reference{}, fmt.Errorf("invalid image reference: %q", input)
	}

	return Reference{Registry: registry, Repository: repo, Tag: tag}, nil
}

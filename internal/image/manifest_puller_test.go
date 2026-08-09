package image

import (
	"strings"
	"testing"
)

func TestPickManifestForPlatform(t *testing.T) {
	t.Parallel()

	index := []byte(`{
		"manifests": [
			{"digest":"sha256:amd64","platform":{"os":"linux","architecture":"amd64"}},
			{"digest":"sha256:arm64","platform":{"os":"linux","architecture":"arm64","variant":"v8"}}
		]
	}`)

	digest, err := pickManifestForPlatform(index, Platform{OS: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatalf("pick arm64 manifest: %v", err)
	}
	if digest != "sha256:arm64" {
		t.Fatalf("unexpected digest: %s", digest)
	}

	_, err = pickManifestForPlatform(index, Platform{OS: "linux", Architecture: "s390x"})
	if err == nil || !strings.Contains(err.Error(), "linux/s390x") {
		t.Fatalf("expected platform-specific error, got %v", err)
	}
}

func TestValidateImagePlatform(t *testing.T) {
	t.Parallel()

	var cfg ImageConfig
	cfg.OS = "linux"
	cfg.Architecture = "amd64"

	if err := ValidateImagePlatform(Platform{OS: "linux", Architecture: "amd64"}, cfg); err != nil {
		t.Fatalf("validate matching platform: %v", err)
	}
	if err := ValidateImagePlatform(Platform{OS: "linux", Architecture: "arm64"}, cfg); err == nil {
		t.Fatal("expected architecture mismatch")
	}
}

package image

import (
	"net/http"
	"os"
	"strings"
	"time"
)

// Config holds image pull behavior settings.
type Config struct {
	DataRoot       string
	RegistryMirror string
	HTTPClient     *http.Client
}

func LoadConfigFromEnv(dataRoot string) Config {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}

	return Config{
		DataRoot:       dataRoot,
		RegistryMirror: strings.TrimSpace(os.Getenv("FC_REGISTRY_MIRROR")),
		HTTPClient:     client,
	}
}

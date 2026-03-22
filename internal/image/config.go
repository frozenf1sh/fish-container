package image

import (
	"net/http"
	"os"
	"net/url"
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
	transport := &http.Transport{Proxy: proxyFromEnvInsensitive}
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}

	return Config{
		DataRoot:       dataRoot,
		RegistryMirror: strings.TrimSpace(getEnvInsensitive("FC_REGISTRY_MIRROR")),
		HTTPClient:     client,
	}
}

func getEnvInsensitive(key string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}

	target := strings.ToLower(strings.TrimSpace(key))
	for _, pair := range os.Environ() {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.ToLower(parts[0]) == target {
			return parts[1]
		}
	}

	return ""
}

func proxyFromEnvInsensitive(req *http.Request) (*url.URL, error) {
	httpsProxy := strings.TrimSpace(getEnvInsensitive("HTTPS_PROXY"))
	httpProxy := strings.TrimSpace(getEnvInsensitive("HTTP_PROXY"))
	noProxy := strings.TrimSpace(getEnvInsensitive("NO_PROXY"))

	if httpsProxy != "" {
		_ = os.Setenv("HTTPS_PROXY", httpsProxy)
	}
	if httpProxy != "" {
		_ = os.Setenv("HTTP_PROXY", httpProxy)
	}
	if noProxy != "" {
		_ = os.Setenv("NO_PROXY", noProxy)
	}

	return http.ProxyFromEnvironment(req)
}

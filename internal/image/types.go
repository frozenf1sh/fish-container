package image

// Descriptor follows OCI descriptor shape for manifests and layers.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Schema2Manifest is Docker schema2/OCI-compatible manifest payload.
type Schema2Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// ImageConfig represents OCI/Docker image config payload.
type ImageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Env        []string `json:"Env"`
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		WorkingDir string   `json:"WorkingDir"`
		User       string   `json:"User"`
	} `json:"config"`
}

// ManifestResult carries parsed manifest metadata and store locations.
type ManifestResult struct {
	Reference    Reference
	Manifest     Schema2Manifest
	Digest       string
	ManifestPath string
	RefPath      string
	ConfigDigest string
	ConfigPath   string
	Config       ImageConfig
}

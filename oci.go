package container

// OCI image types, as the API server reads them from the content store.

// Index is an OCI image index.
type Index struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	Manifests     []Descriptor      `json:"manifests"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Annotation keys that the image index uses.
const (
	annotationReferenceType = "vnd.docker.reference.type"
	attestationManifest     = "attestation-manifest"
	unknownPlatformValue    = "unknown"

	// AnnotationImageName holds the name of a locally built image.
	AnnotationImageName = "com.apple.containerization.image.name"
)

// RunnablePlatforms returns the platforms of the manifests that are images,
// leaving out attestations and unknown platforms.
func (x *Index) RunnablePlatforms() []Platform {
	var out []Platform
	for _, m := range x.Manifests {
		if m.Annotations[annotationReferenceType] == attestationManifest || m.Platform == nil {
			continue
		}
		if m.Platform.OS == unknownPlatformValue && m.Platform.Architecture == unknownPlatformValue {
			continue
		}
		out = append(out, *m.Platform)
	}
	return out
}

// Manifest is an OCI image manifest.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Image is an OCI image configuration document.
type Image struct {
	Created      string       `json:"created,omitempty"`
	Author       string       `json:"author,omitempty"`
	Architecture string       `json:"architecture"`
	OS           string       `json:"os"`
	Variant      string       `json:"variant,omitempty"`
	Config       *ImageConfig `json:"config,omitempty"`
}

// ImageConfig is the execution configuration in an OCI image.
type ImageConfig struct {
	User         string              `json:"User,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Volumes      map[string]struct{} `json:"Volumes,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	StopSignal   string              `json:"StopSignal,omitempty"`
}

// normalizeArch maps an architecture name to its OCI form and default
// variant.
func normalizeArch(arch string) (string, string) {
	// The API server maps the same aliases.
	switch arch {
	case "aarch64", "arm64":
		return "arm64", "v8"
	case "x86_64", "x86-64", "amd64":
		return "amd64", ""
	case "arm", "armhf", "armel":
		return "arm", "v7"
	}
	return arch, ""
}

// Equal reports whether two platforms are the same. Architecture aliases
// such as aarch64 and arm64 are equal, and an arm64 platform without a
// variant equals arm64/v8.
func (p Platform) Equal(q Platform) bool {
	// The API server compares platforms the same way.
	pa, _ := normalizeArch(p.Architecture)
	qa, _ := normalizeArch(q.Architecture)
	if p.OS != q.OS || pa != qa {
		return false
	}
	if pa == "arm64" {
		return orDefault(p.Variant, "v8") == orDefault(q.Variant, "v8")
	}
	return p.Variant == q.Variant
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// String returns the platform as os/arch or os/arch/variant. The variant v8
// of arm64 is left out, because it is the only arm64 variant.
func (p Platform) String() string {
	arch, _ := normalizeArch(p.Architecture)
	if p.Variant == "" || (arch == "arm64" && p.Variant == "v8") {
		return p.OS + "/" + arch
	}
	return p.OS + "/" + arch + "/" + p.Variant
}

// LinuxArm64Platform is the OCI platform of Linux on Apple silicon.
var LinuxArm64Platform = Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}

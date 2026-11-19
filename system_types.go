package container

// VolumeConfiguration is a named volume.
type VolumeConfiguration struct {
	Name         string            `json:"name"`
	Driver       string            `json:"driver"`
	Format       string            `json:"format"`
	Source       string            `json:"source"`
	CreationDate Date              `json:"creationDate"`
	Labels       map[string]string `json:"labels"`
	Options      map[string]string `json:"options"`
	SizeInBytes  *uint64           `json:"sizeInBytes,omitempty"`
}

// IsAnonymous reports whether the system created the volume for an unnamed
// mount.
func (v *VolumeConfiguration) IsAnonymous() bool {
	_, ok := v.Labels[LabelAnonymousVolume]
	return ok
}

// SystemHealth is the reply to Ping.
type SystemHealth struct {
	// AppRoot and InstallRoot are file URLs.
	AppRoot     string
	InstallRoot string
	// LogRoot is a path. It is empty if the server did not send one.
	LogRoot          string
	APIServerVersion string
	APIServerCommit  string
	APIServerBuild   string
	APIServerAppName string
}

// DiskUsageStats is the disk usage of the container system.
type DiskUsageStats struct {
	Images     ResourceUsage `json:"images"`
	Containers ResourceUsage `json:"containers"`
	Volumes    ResourceUsage `json:"volumes"`
}

// ResourceUsage is the disk usage of one kind of resource.
type ResourceUsage struct {
	Total       int    `json:"total"`
	Active      int    `json:"active"`
	SizeInBytes uint64 `json:"sizeInBytes"`
	Reclaimable uint64 `json:"reclaimable"`
}

// SystemPlatform is the OS and architecture of a virtual machine.
type SystemPlatform struct {
	// OS is "linux" or "darwin".
	OS string `json:"os"`
	// Architecture is "arm64" or "amd64".
	Architecture string `json:"architecture"`
}

// LinuxArm64 is the platform of Linux containers on Apple silicon.
var LinuxArm64 = SystemPlatform{OS: "linux", Architecture: "arm64"}

// Kernel is a Linux kernel for container virtual machines.
type Kernel struct {
	// Path is a file URL.
	Path        string            `json:"path"`
	Platform    SystemPlatform    `json:"platform"`
	CommandLine KernelCommandLine `json:"commandLine"`
}

// KernelCommandLine holds the kernel and init arguments.
type KernelCommandLine struct {
	KernelArgs []string `json:"kernelArgs"`
	InitArgs   []string `json:"initArgs"`
}

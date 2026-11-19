package container

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// This file holds the system configuration: the settings that the container
// CLI reads from config.toml. The defaults are those of the 1.7.1 release.
// Some of them, such as the vminit and builder images, change with each
// release.

// Default values of the system configuration for the 1.7.1 release.
const (
	DefaultBuilderImage     = "ghcr.io/apple/container-builder-shim/builder:0.13.3"
	DefaultVMInitImage      = "ghcr.io/apple/containerization/vminit:0.54.0"
	DefaultKernelBinaryPath = "opt/kata/share/kata-containers/vmlinux-6.18.56-208-debug"
	DefaultKernelURL        = "https://github.com/kata-containers/kata-containers/releases/download/4.3.1/kata-static-4.3.1-arm64.tar.zst"
	DefaultKernelDigest     = "sha256:2c5bf7cde334ad5307d482f45cceda496f91c9b6b96f5278c47f4f4eac1bbae6"
)

// SystemConfig is the configuration of the container system. Its JSON form
// is the output of `container system property list --format json`.
type SystemConfig struct {
	Build     BuildConfig       `json:"build"`
	Container ContainerDefaults `json:"container"`
	DNS       HostDNSConfig     `json:"dns"`
	Kernel    KernelConfig      `json:"kernel"`
	Machine   MachineConfig     `json:"machine"`
	Network   NetworkDefaults   `json:"network"`
	Registry  RegistryConfig    `json:"registry"`
	VMInit    VMInitConfig      `json:"vminit"`
}

func (c SystemConfig) clone() SystemConfig {
	if d := c.Container.DNS; d != nil {
		c.Container.DNS = &ContainerDNSDefaults{
			Domain:        d.Domain,
			Nameservers:   slices.Clone(d.Nameservers),
			SearchDomains: slices.Clone(d.SearchDomains),
			Options:       slices.Clone(d.Options),
		}
	}
	if c.Network.Subnet != nil {
		c.Network.Subnet = new(*c.Network.Subnet)
	}
	if c.Network.SubnetV6 != nil {
		c.Network.SubnetV6 = new(*c.Network.SubnetV6)
	}
	return c
}

// BuildConfig is the [build] section: the builder VM of `container build`.
type BuildConfig struct {
	Rosetta bool       `json:"rosetta"`
	CPUs    int        `json:"cpus"`
	Memory  MemorySize `json:"memory"`
	Image   string     `json:"image"`
}

// ContainerDefaults is the [container] section: the defaults of
// `container run` and `container create`.
type ContainerDefaults struct {
	CPUs   int                   `json:"cpus"`
	Memory MemorySize            `json:"memory"`
	DNS    *ContainerDNSDefaults `json:"dns,omitempty"`
}

// ContainerDNSDefaults is the [container.dns] section: the resolver
// settings of a container when the caller sets none. A nil slice is unset.
type ContainerDNSDefaults struct {
	Domain        string   `json:"domain,omitempty"`
	Nameservers   []string `json:"nameservers,omitempty"`
	SearchDomains []string `json:"searchDomains,omitempty"`
	Options       []string `json:"options,omitempty"`
}

// HostDNSConfig is the [dns] section. Domain qualifies container hostnames
// on the host, for example "test". Empty means no domain.
type HostDNSConfig struct {
	Domain string `json:"domain,omitempty"`
}

// KernelConfig is the [kernel] section: the kernel that
// `container system kernel set --recommended` installs. BinaryPath is the
// path of the kernel inside the archive at URL.
type KernelConfig struct {
	BinaryPath string `json:"binaryPath"`
	URL        string `json:"url"`
	Digest     string `json:"digest"`
}

// HomeMount is how a container machine mounts the home directory of the
// user.
type HomeMount string

// Home mount options.
const (
	HomeMountReadOnly  HomeMount = "ro"
	HomeMountReadWrite HomeMount = "rw"
	HomeMountNone      HomeMount = "none"
)

// MachineConfig is the [machine] section: the defaults of
// `container machine create`.
type MachineConfig struct {
	CPUs           int        `json:"cpus"`
	Memory         MemorySize `json:"memory"`
	HomeMount      HomeMount  `json:"homeMount"`
	Virtualization bool       `json:"virtualization"`
	// KernelPath is a custom kernel binary. Empty uses the system kernel.
	KernelPath string `json:"kernelPath,omitempty"`
}

// NetworkDefaults is the [network] section: the subnets of new networks.
// Nil lets the system choose.
type NetworkDefaults struct {
	Subnet   *netip.Prefix `json:"subnet,omitempty"`
	SubnetV6 *netip.Prefix `json:"subnetv6,omitempty"`
}

// RegistryConfig is the [registry] section. Domain is the registry of
// image references that have no domain.
type RegistryConfig struct {
	Domain string `json:"domain"`
}

// VMInitConfig is the [vminit] section: the image that boots container
// VMs.
type VMInitConfig struct {
	Image string `json:"image"`
}

// DefaultSystemConfig returns the configuration that the container system
// uses when no config.toml sets a value. The machine defaults depend on
// this host: half of its CPUs and half of its memory, with at least 4 CPUs
// and 1 GiB.
func DefaultSystemConfig() SystemConfig {
	return SystemConfig{
		Build: BuildConfig{
			Rosetta: true,
			CPUs:    2,
			Memory:  mustMemorySize("2048MB"),
			Image:   DefaultBuilderImage,
		},
		Container: ContainerDefaults{CPUs: DefaultCPUs, Memory: mustMemorySize("1g")},
		Kernel: KernelConfig{
			BinaryPath: DefaultKernelBinaryPath,
			URL:        DefaultKernelURL,
			Digest:     DefaultKernelDigest,
		},
		Machine: MachineConfig{
			CPUs:      max(runtime.NumCPU()/2, 4),
			Memory:    mustMemorySize(strconv.FormatUint(max(physicalMemory()/2, 1<<30)>>30, 10) + "gb"),
			HomeMount: HomeMountReadWrite,
		},
		Registry: RegistryConfig{Domain: DefaultRegistryDomain},
		VMInit:   VMInitConfig{Image: DefaultVMInitImage},
	}
}

// physicalMemory returns the memory of the host in bytes, or 0 if it is
// unknown.
func physicalMemory() uint64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	// Sysctl drops a final zero byte, which is the high byte of a
	// little-endian uint64.
	b := make([]byte, 8)
	copy(b, s)
	return binary.LittleEndian.Uint64(b)
}

// Validate returns an error with CodeInvalidArgument if the machine has no
// CPUs, has less than 1 GiB of memory, or has an unknown home mount option.
func (c *SystemConfig) Validate() error {
	if c.Machine.CPUs <= 0 {
		return invalidArgument("invalid CPU count '%d'. Must be a positive integer (e.g., 4).", c.Machine.CPUs)
	}
	if c.Machine.Memory.Bytes() < 1<<30 {
		return invalidArgument("invalid memory value '%s'. Must be greater than 1gb.", c.Machine.Memory)
	}
	switch c.Machine.HomeMount {
	case HomeMountReadOnly, HomeMountReadWrite, HomeMountNone:
	default:
		return invalidArgument("invalid home mount option '%s'. Valid options: ro, rw, none", c.Machine.HomeMount)
	}
	return nil
}

// NormalizeReference returns the full form of an image reference, with the
// registry domain of the configuration. The vminit image does not change.
func (c *SystemConfig) NormalizeReference(reference string) (string, error) {
	if reference == c.VMInit.Image {
		return reference, nil
	}
	return NormalizeReference(reference, c.Registry.Domain)
}

// DenormalizeReference returns the short form of an image reference, with
// the registry domain of the configuration.
func (c *SystemConfig) DenormalizeReference(reference string) (string, error) {
	return DenormalizeReference(reference, c.Registry.Domain)
}

// ConfigRoots are the directories that hold the configuration files of the
// container system. They are file system paths.
type ConfigRoots struct {
	// AppRoot holds the copy of the user configuration that the system
	// uses, for example ~/Library/Application Support/com.apple.container.
	AppRoot string
	// InstallRoot holds the system defaults, for example /usr/local.
	InstallRoot string
}

// ConfigFiles returns the configuration files in order of precedence: the
// user configuration in AppRoot, then the system defaults in InstallRoot. A
// value in an earlier file wins over the same key in a later file.
func (r ConfigRoots) ConfigFiles() []string {
	return []string{
		filepath.Join(r.AppRoot, "config", "config.toml"),
		filepath.Join(r.InstallRoot, "etc", "container", "config.toml"),
	}
}

// ConfigLoader reads the system configuration from the files under roots.
// The client calls it once, when it first needs the configuration. The
// module github.com/jen20/go-container/systemconfig has a loader for the
// TOML files that the CLI reads.
type ConfigLoader interface {
	LoadSystemConfig(ctx context.Context, roots ConfigRoots) (SystemConfig, error)
}

// ConfigLoaderFunc is a function that is a ConfigLoader.
type ConfigLoaderFunc func(ctx context.Context, roots ConfigRoots) (SystemConfig, error)

// LoadSystemConfig calls f.
func (f ConfigLoaderFunc) LoadSystemConfig(ctx context.Context, roots ConfigRoots) (SystemConfig, error) {
	return f(ctx, roots)
}

// Option configures a Client.
type Option func(*Client)

// WithConfig sets the client configuration to the values in cfg, without
// loading configuration files.
func WithConfig(cfg SystemConfig) Option {
	cfg = cfg.clone()
	return func(c *Client) {
		c.loader = nil
		c.sysConfig = &cfg
	}
}

// WithConfigLoader makes the client read the system configuration with l.
// The client asks the API server for the configuration roots, as
// `container system property list` does.
func WithConfigLoader(l ConfigLoader) Option {
	return func(c *Client) {
		c.loader = l
		c.sysConfig = nil
	}
}

// WithConfigFileLoader reads configuration files with load, for example
// systemconfig.Load. The loader returns one map per file, highest precedence
// first. Nested sections are maps keyed by string. Integer and array values
// use int64 and []any respectively.
//
// With no files, the client reads ConfigRoots.ConfigFiles. Settings absent
// from the returned layers use DefaultSystemConfig.
func WithConfigFileLoader(load func(context.Context, ...string) ([]map[string]any, error), files ...string) Option {
	files = slices.Clone(files)
	return WithConfigLoader(ConfigLoaderFunc(func(ctx context.Context, roots ConfigRoots) (SystemConfig, error) {
		paths := slices.Clone(files)
		if len(paths) == 0 {
			paths = roots.ConfigFiles()
		}
		layers, err := load(ctx, paths...)
		if err != nil {
			if ctx.Err() != nil {
				return SystemConfig{}, ctx.Err()
			}
			return SystemConfig{}, &Error{Code: CodeInvalidArgument, Message: err.Error(), cause: err}
		}
		return decodeConfig(layers)
	}))
}

// configLoadTimeout limits the ping that SystemConfig sends to learn the
// configuration roots.
const configLoadTimeout = 10 * time.Second

// SystemConfig returns the system configuration of the client. With a
// loader, the first call reads the configuration, and later calls return a
// copy of that result. A failed read is not kept, so the next call tries again.
func (c *Client) SystemConfig(ctx context.Context) (SystemConfig, error) {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	if c.sysConfig != nil {
		return c.sysConfig.clone(), nil
	}
	if c.loader == nil {
		return DefaultSystemConfig(), nil
	}
	pctx, cancel := context.WithTimeout(ctx, configLoadTimeout)
	defer cancel()
	health, err := c.Ping(pctx)
	if err != nil {
		return SystemConfig{}, err
	}
	roots := ConfigRoots{}
	if roots.AppRoot, err = fileURLPath(health.AppRoot); err != nil {
		return SystemConfig{}, err
	}
	if roots.InstallRoot, err = fileURLPath(health.InstallRoot); err != nil {
		return SystemConfig{}, err
	}
	cfg, err := c.loader.LoadSystemConfig(ctx, roots)
	if err != nil {
		return SystemConfig{}, err
	}
	c.sysConfig = new(cfg.clone())
	return c.sysConfig.clone(), nil
}

// fileURLPath returns the path of a file URL.
func fileURLPath(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", &Error{Code: CodeInternalError, Message: fmt.Sprintf("invalid root %q from ping", s), cause: err}
	}
	if u.Scheme != "file" {
		return "", &Error{Code: CodeInternalError, Message: fmt.Sprintf("root %q from ping is not a file URL", s)}
	}
	return u.Path, nil
}

// MemorySize is a size in bytes that the configuration writes as text, such
// as "2048mb" or "1g". All units are powers of 1024.
type MemorySize struct {
	bytes uint64
	text  string // the form that String returns
}

var memoryUnits = map[byte]struct {
	shift uint
	label string
}{
	'b': {0, "b"}, 'k': {10, "kb"}, 'm': {20, "mb"}, 'g': {30, "gb"}, 't': {40, "tb"}, 'p': {50, "pb"},
}

// ParseMemorySize parses a size. The text is a number and an optional unit:
// b, k, m, g, t, or p, each optionally followed by "b" or "ib". Case does
// not matter. A number with no unit is bytes.
func ParseMemorySize(s string) (MemorySize, error) {
	t := strings.ToLower(strings.Trim(s, " \t"))
	if t == "" {
		return MemorySize{}, invalidArgument("invalid size")
	}
	i := strings.IndexFunc(t, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	rawValue, rawUnit := t, ""
	if i >= 0 {
		rawValue, rawUnit = strings.Trim(t[:i], " \t"), strings.Trim(t[i:], " \t")
	}
	symbol := byte('b')
	if rawUnit != "" {
		symbol = rawUnit[0]
		switch rawUnit[1:] {
		case "", "ib", "b":
		default:
			return MemorySize{}, invalidArgument("invalid symbol: %s", rawUnit)
		}
	}
	unit, ok := memoryUnits[symbol]
	if !ok {
		return MemorySize{}, invalidArgument("invalid symbol: %s", rawUnit)
	}
	if !strings.Contains(rawValue, ".") {
		value, err := strconv.ParseUint(rawValue, 10, 64)
		if err != nil || value > math.MaxUint64>>unit.shift {
			return MemorySize{}, invalidArgument("invalid size")
		}
		return MemorySize{bytes: value << unit.shift, text: strconv.FormatUint(value, 10) + unit.label}, nil
	}
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return MemorySize{}, invalidArgument("invalid size")
	}
	bytes := math.Round(value * float64(uint64(1)<<unit.shift))
	if bytes >= math.MaxUint64 {
		return MemorySize{}, invalidArgument("invalid size")
	}
	return MemorySize{
		bytes: uint64(bytes),
		text:  strconv.FormatUint(uint64(value), 10) + unit.label,
	}, nil
}

func mustMemorySize(s string) MemorySize {
	m, err := ParseMemorySize(s)
	if err != nil {
		panic(err)
	}
	return m
}

// Bytes returns the size in bytes.
func (m MemorySize) Bytes() uint64 { return m.bytes }

// String returns the size in the form that the CLI shows, such as "2048mb".
// It is the whole part of the number as written, and the unit.
func (m MemorySize) String() string {
	if m.text == "" {
		return strconv.FormatUint(m.bytes, 10) + "b"
	}
	return m.text
}

// MarshalText encodes the size without losing precision.
func (m MemorySize) MarshalText() ([]byte, error) {
	text := m.String()
	if display, err := ParseMemorySize(text); err == nil && display.bytes == m.bytes {
		return []byte(text), nil
	}
	return append(strconv.AppendUint(nil, m.bytes, 10), 'b'), nil
}

// UnmarshalText parses the size with ParseMemorySize.
func (m *MemorySize) UnmarshalText(b []byte) error {
	v, err := ParseMemorySize(string(b))
	if err != nil {
		return err
	}
	*m = v
	return nil
}

package container

import (
	"cmp"
	"context"
	"os"
	"strings"
)

// ImageContainerOptions are the options of ConfigFromImage. The zero value
// gives the same result as `container run IMAGE` with no flags.
type ImageContainerOptions struct {
	// Platform selects the image platform. Nil uses linux/arm64.
	Platform *Platform
	// Arguments replace the image command (Cmd). They follow the entrypoint.
	Arguments []string
	// Entrypoint replaces the image entrypoint. When it is set, the image
	// command is not used.
	Entrypoint string
	// Env adds or replaces environment variables, in KEY=VALUE form. A
	// bare KEY takes its value from the environment of this process, and
	// is dropped if this process does not have it.
	Env []string
	// WorkingDir replaces the image working directory.
	WorkingDir string
	// User replaces the image user, as a user string such as "1000:1000".
	User string
	// Terminal gives the init process a terminal.
	Terminal bool
	// Network is the network to join. Empty uses the built-in network.
	Network string
	// DNSDomain qualifies the container hostname and is the domain of its
	// resolver. Empty uses the system configuration: dns.domain for the
	// hostname, and container.dns.domain, then dns.domain, for the resolver.
	DNSDomain string
	// CPUs and MemoryInBytes replace the container.cpus and container.memory
	// settings of the system configuration when not zero.
	CPUs          int
	MemoryInBytes uint64
	// Labels are added to the container.
	Labels map[string]string
	// Pull controls how the image is fetched. Nil fetches it with default
	// options if it is not local.
	Pull *PullOptions
	// Progress receives pull and unpack progress. It can be nil.
	Progress ProgressFunc
}

// ConfigFromImage prepares a container from an image name, as `container run`
// does before it creates the container. It fetches the image if needed,
// unpacks it for the platform, reads the image configuration for the
// process, attaches the container to a network, and returns the default
// kernel. Pass the results to CreateContainer.
func (c *Client) ConfigFromImage(ctx context.Context, id, reference string, opts *ImageContainerOptions) (Configuration, Kernel, error) {
	if opts == nil {
		opts = &ImageContainerOptions{}
	}
	sys, err := c.SystemConfig(ctx)
	if err != nil {
		return Configuration{}, Kernel{}, err
	}
	platform := LinuxArm64Platform
	if opts.Platform != nil {
		platform = *opts.Platform
	}
	pull := PullOptions{}
	if opts.Pull != nil {
		pull = *opts.Pull
	}
	pull.Platform = &platform
	if pull.Progress == nil {
		pull.Progress = opts.Progress
	}

	img, err := c.FetchImage(ctx, reference, &pull)
	if err != nil {
		return Configuration{}, Kernel{}, err
	}
	if _, err := c.GetCreateSnapshot(ctx, img, platform, opts.Progress); err != nil {
		return Configuration{}, Kernel{}, err
	}
	kernel, err := c.DefaultKernel(ctx, LinuxArm64)
	if err != nil {
		return Configuration{}, Kernel{}, err
	}
	imageSpec, err := c.ImageConfig(ctx, img, platform)
	if err != nil {
		return Configuration{}, Kernel{}, err
	}
	proc, err := processFromImage(imageSpec.Config, opts)
	if err != nil {
		return Configuration{}, Kernel{}, err
	}

	config := NewConfiguration(id, *img, proc)
	config.Platform = platform
	// The CLI rounds the default memory down to a whole MiB.
	config.Resources.CPUs = cmp.Or(opts.CPUs, sys.Container.CPUs)
	config.Resources.MemoryInBytes = cmp.Or(opts.MemoryInBytes, sys.Container.Memory.Bytes()>>20<<20)
	config.Labels = opts.Labels
	config.DNS = dnsFromSystem(opts.DNSDomain, &sys)
	if imageSpec.Config != nil {
		config.StopSignal = imageSpec.Config.StopSignal
	}
	if arch, _ := normalizeArch(platform.Architecture); arch == "amd64" {
		config.Rosetta = true
	}
	config.RuntimeHandler = "container-runtime-" + platform.OS

	network := opts.Network
	if network == "" {
		networks, err := c.ListNetworks(ctx)
		if err != nil {
			return Configuration{}, Kernel{}, err
		}
		for _, n := range networks {
			if n.IsBuiltin() {
				network = n.ID
				break
			}
		}
		if network == "" {
			return Configuration{}, Kernel{}, &Error{Code: CodeInvalidState, Message: "builtin network is not present"}
		}
	} else if _, err := c.GetNetwork(ctx, network); err != nil {
		return Configuration{}, Kernel{}, err
	}
	config.Networks = []AttachmentConfiguration{DefaultAttachment(network, id, cmp.Or(opts.DNSDomain, sys.DNS.Domain))}
	return config, *kernel, nil
}

// CreateContainerFromImage runs ConfigFromImage and then CreateContainer. It
// returns the configuration of the new container.
func (c *Client) CreateContainerFromImage(ctx context.Context, id, reference string, opts *ImageContainerOptions) (*Configuration, error) {
	config, kernel, err := c.ConfigFromImage(ctx, id, reference, opts)
	if err != nil {
		return nil, err
	}
	if err := c.CreateContainer(ctx, config, kernel, nil); err != nil {
		return nil, err
	}
	return &config, nil
}

// dnsFromSystem returns the DNS configuration of a container from the
// [container.dns] defaults of sys. The domain is the first non-empty value of
// domain, container.dns.domain, and dns.domain.
func dnsFromSystem(domain string, sys *SystemConfig) *DNSConfiguration {
	d := sys.Container.DNS
	if d == nil {
		d = &ContainerDNSDefaults{}
	}
	return &DNSConfiguration{
		Nameservers:   d.Nameservers,
		Domain:        cmp.Or(domain, d.Domain, sys.DNS.Domain),
		SearchDomains: d.SearchDomains,
		Options:       d.Options,
	}
}

// processFromImage returns the init process of a container from the image
// configuration and the options.
func processFromImage(ic *ImageConfig, opts *ImageContainerOptions) (ProcessConfiguration, error) {
	if ic == nil {
		ic = &ImageConfig{}
	}
	var args []string
	override := opts.Entrypoint != ""
	switch {
	case override:
		args = []string{opts.Entrypoint}
	case len(ic.Entrypoint) > 0 && (len(ic.Entrypoint) != 1 || ic.Entrypoint[0] != ""):
		// A single empty string clears the image entrypoint.
		args = append(args, ic.Entrypoint...)
	}
	if len(opts.Arguments) > 0 {
		args = append(args, opts.Arguments...)
	} else if !override {
		args = append(args, ic.Cmd...)
	}
	if len(args) == 0 {
		return ProcessConfiguration{}, invalidArgument("command/entrypoint not specified for container process")
	}

	workingDir := "/"
	switch {
	case opts.WorkingDir != "":
		workingDir = opts.WorkingDir
	case ic.WorkingDir != "":
		workingDir = ic.WorkingDir
	}

	user := User{}
	switch {
	case opts.User != "":
		user = User{Name: opts.User}
	case ic.User != "":
		user = User{Name: ic.User}
	}

	proc := NewProcessConfiguration(args[0], args[1:], mergeEnv(ic.Env, opts.Env))
	proc.WorkingDirectory = workingDir
	proc.User = user
	proc.Terminal = opts.Terminal
	return proc, nil
}

// mergeEnv combines image variables and user variables. A later value for a
// name replaces an earlier one. Image entries without "=" are dropped,
// because image configuration is untrusted. The result keeps the position
// of the first entry for each name.
func mergeEnv(image, user []string) []string {
	var order []string
	values := map[string]string{}
	add := func(entry string) {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := values[name]; !ok {
			order = append(order, name)
		}
		values[name] = entry
	}
	for _, e := range image {
		if strings.Contains(e, "=") {
			add(e)
		}
	}
	for _, e := range user {
		if strings.Contains(e, "=") {
			add(e)
		} else if v, ok := os.LookupEnv(e); ok {
			add(e + "=" + v)
		}
	}
	out := make([]string, 0, len(order))
	for _, name := range order {
		out = append(out, values[name])
	}
	return out
}

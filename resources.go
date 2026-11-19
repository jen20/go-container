package container

import (
	"cmp"
	"context"
	"net/url"
	"path/filepath"
	"time"

	"github.com/jen20/go-container/internal/xpc"
)

// Defaults that the CLI uses for volumes and networks.
const (
	// DefaultVolumeDriver is the driver of a volume when the caller sets
	// none.
	DefaultVolumeDriver = "local"
	// DefaultNetworkPlugin is the plugin of `container network create`.
	DefaultNetworkPlugin = "container-network-vmnet"
)

// CreateVolumeOptions are the optional parts of a volume create request.
type CreateVolumeOptions struct {
	// Driver is the volume driver. Empty means DefaultVolumeDriver.
	Driver string
	// DriverOptions are driver-specific options, for example "size".
	DriverOptions map[string]string
	// Labels are key-value labels on the volume.
	Labels map[string]string
}

// CreateVolume creates a named volume and returns its configuration. If a
// volume with that name already exists, the 1.7.1 server returns an error
// with CodeInvalidArgument, not CodeExists.
func (c *Client) CreateVolume(ctx context.Context, name string, opts *CreateVolumeOptions) (*VolumeConfiguration, error) {
	if opts == nil {
		opts = &CreateVolumeOptions{}
	}
	driverOpts, err := encodeJSON(emptyMapIfNil(opts.DriverOptions))
	if err != nil {
		return nil, err
	}
	labels, err := encodeJSON(emptyMapIfNil(opts.Labels))
	if err != nil {
		return nil, err
	}
	values := xpc.Dictionary{
		"volumeName":       name,
		"volumeDriver":     cmp.Or(opts.Driver, DefaultVolumeDriver),
		"volumeDriverOpts": driverOpts,
		"volumeLabels":     labels,
	}
	var out VolumeConfiguration
	ok, err := c.api.requestJSONTimeout(ctx, "volumeCreate", values, "volume", &out, 0)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeInternalError, Message: "invalid response from server"}
	}
	return &out, nil
}

// DeleteVolume deletes the volume with the given name. The server refuses
// to delete a volume that a container uses.
func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	return c.api.send(ctx, "volumeDelete", xpc.Dictionary{"volumeName": name}, 0)
}

// NewNetworkConfiguration returns a configuration with the same defaults as
// `container network create NAME`: the DefaultNetworkPlugin plugin, no
// preferred subnets, and the creation date now.
func NewNetworkConfiguration(name string, mode NetworkMode) NetworkConfiguration {
	return NetworkConfiguration{
		Name:         name,
		CreationDate: Date{time.Now()},
		Mode:         mode,
		Plugin:       DefaultNetworkPlugin,
	}
}

// CreateNetwork creates a network, starts its plugin, and returns the
// running network. The server checks the name and the labels.
func (c *Client) CreateNetwork(ctx context.Context, config NetworkConfiguration) (*NetworkResource, error) {
	data, err := encodeJSON(config)
	if err != nil {
		return nil, err
	}
	values := xpc.Dictionary{"networkId": config.Name, "networkConfig": data}
	var out NetworkResource
	ok, err := c.api.requestJSON(ctx, "networkCreate", values, "networkResource", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeInvalidArgument, Message: "network configuration not received"}
	}
	return &out, nil
}

// DeleteNetwork deletes the network with the given ID. The server refuses
// to delete the built-in network or a network that has containers attached.
func (c *Client) DeleteNetwork(ctx context.Context, id string) error {
	return c.api.send(ctx, "networkDelete", xpc.Dictionary{"networkId": id}, DefaultTimeout)
}

// BuiltinNetwork returns the network that the system created. It returns an
// error that matches ErrNotFound if there is no such network.
func (c *Client) BuiltinNetwork(ctx context.Context) (*NetworkResource, error) {
	list, err := c.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].IsBuiltin() {
			return &list[i], nil
		}
	}
	return nil, &Error{Code: CodeNotFound, Message: "builtin network not found"}
}

// InstallKernel copies the kernel binary at path into the system and makes
// it the default kernel for platform. A relative path is relative to the
// current directory. If force is false, the request fails when a kernel
// with the same name is already installed.
func (c *Client) InstallKernel(ctx context.Context, path string, platform SystemPlatform, force bool) error {
	// The server reads the path as a URL, so send a file URL, as the CLI
	// does.
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	fileURL := (&url.URL{Scheme: "file", Path: abs}).String()
	values, err := kernelValues(fileURL, platform, force)
	if err != nil {
		return err
	}
	return c.api.send(ctx, "installKernel", values, 0)
}

// InstallKernelArchiveOptions are the optional parts of a request to install
// a kernel from an archive.
type InstallKernelArchiveOptions struct {
	// Digest is the expected digest of the archive, for example
	// "sha256:...". Empty skips the check.
	Digest string
	// Force replaces an installed kernel with the same name.
	Force bool
	// Progress receives updates while the server downloads and extracts
	// the archive.
	Progress ProgressFunc
}

// InstallKernelFromArchive installs a kernel from a tar archive and makes
// it the default kernel for platform. The archive is a URL, which the
// server downloads, or a file path. A relative path is relative to the
// current directory. kernelPath is the path of the kernel binary inside
// the archive.
func (c *Client) InstallKernelFromArchive(ctx context.Context, archive, kernelPath string, platform SystemPlatform, opts *InstallKernelArchiveOptions) (err error) {
	if opts == nil {
		opts = &InstallKernelArchiveOptions{}
	}
	values, err := kernelValues(kernelPath, platform, opts.Force)
	if err != nil {
		return err
	}
	if u, err := url.Parse(archive); err != nil || u.Scheme == "" {
		if archive, err = filepath.Abs(archive); err != nil {
			return err
		}
	}
	values["kernelTarURL"] = archive
	if opts.Digest != "" {
		values["kernelDigest"] = opts.Digest
	}
	pr, err := startProgress(opts.Progress, values)
	if err != nil {
		return err
	}
	defer func() { err = joinErrors(err, pr.stop()) }()
	return c.api.send(ctx, "installKernel", values, 0)
}

func kernelValues(path string, platform SystemPlatform, force bool) (xpc.Dictionary, error) {
	data, err := encodeJSON(platform)
	if err != nil {
		return nil, err
	}
	return xpc.Dictionary{
		"kernelFilePath": path,
		"kernelForce":    force,
		"systemPlatform": data,
	}, nil
}

package container

import (
	"context"
	"fmt"
)

// Ping checks that the API server is running and returns its details.
func (c *Client) Ping(ctx context.Context) (_ *SystemHealth, err error) {
	reply, err := c.api.request(ctx, "ping", nil)
	if err != nil {
		return nil, err
	}
	defer closeJoin(&err, reply)
	h := &SystemHealth{
		AppRoot:          reply.String("appRoot"),
		InstallRoot:      reply.String("installRoot"),
		LogRoot:          reply.String("logRoot"),
		APIServerVersion: reply.String("apiServerVersion"),
		APIServerCommit:  reply.String("apiServerCommit"),
		APIServerBuild:   reply.String("apiServerBuild"),
		APIServerAppName: reply.String("apiServerAppName"),
	}
	for key, v := range map[string]string{
		"appRoot":          h.AppRoot,
		"installRoot":      h.InstallRoot,
		"apiServerVersion": h.APIServerVersion,
		"apiServerCommit":  h.APIServerCommit,
		"apiServerBuild":   h.APIServerBuild,
		"apiServerAppName": h.APIServerAppName,
	} {
		if v == "" {
			return nil, &Error{Code: CodeInternalError, Message: "ping reply has no " + key}
		}
	}
	return h, nil
}

// ListContainers returns the containers that match filters.
func (c *Client) ListContainers(ctx context.Context, filters ListFilters) ([]Snapshot, error) {
	data, err := encodeJSON(filters)
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	if _, err := c.api.requestJSON(ctx, "containerList", map[string]any{"listFilters": data}, "containers", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetContainer returns the container with the given ID. It returns an error
// that matches ErrNotFound if there is no such container.
func (c *Client) GetContainer(ctx context.Context, id string) (*Snapshot, error) {
	list, err := c.ListContainers(ctx, ListFilters{IDs: []string{id}})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, &Error{Code: CodeNotFound, Message: fmt.Sprintf("get failed: container %s not found", id)}
	}
	return &list[0], nil
}

// ContainerStats returns a resource usage sample for a running container.
func (c *Client) ContainerStats(ctx context.Context, id string) (*Stats, error) {
	var out Stats
	ok, err := c.api.requestJSON(ctx, "containerStats", map[string]any{"id": id}, "statistics", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeInternalError, Message: "no statistics data returned"}
	}
	return &out, nil
}

// ContainerDiskUsage returns the disk space that a container uses, in bytes.
func (c *Client) ContainerDiskUsage(ctx context.Context, id string) (_ uint64, err error) {
	reply, err := c.api.request(ctx, "containerDiskUsage", map[string]any{"id": id})
	if err != nil {
		return 0, err
	}
	defer closeJoin(&err, reply)
	return reply.Uint64("containerSize"), nil
}

// ListNetworks returns all networks.
func (c *Client) ListNetworks(ctx context.Context) ([]NetworkResource, error) {
	var out []NetworkResource
	if _, err := c.api.requestJSON(ctx, "networkList", nil, "networkResources", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetNetwork returns the network with the given ID. It returns an error that
// matches ErrNotFound if there is no such network.
func (c *Client) GetNetwork(ctx context.Context, id string) (*NetworkResource, error) {
	list, err := c.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, &Error{Code: CodeNotFound, Message: fmt.Sprintf("network %s not found", id)}
}

// ListVolumes returns all volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]VolumeConfiguration, error) {
	var out []VolumeConfiguration
	if _, err := c.api.requestJSON(ctx, "volumeList", nil, "volumes", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectVolume returns the volume with the given name. For a missing volume,
// the 1.7.1 server returns an error with CodeInvalidArgument, not
// CodeNotFound.
func (c *Client) InspectVolume(ctx context.Context, name string) (*VolumeConfiguration, error) {
	var out VolumeConfiguration
	ok, err := c.api.requestJSON(ctx, "volumeInspect", map[string]any{"volumeName": name}, "volume", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeNotFound, Message: fmt.Sprintf("volume '%s' not found", name)}
	}
	return &out, nil
}

// VolumeDiskUsage returns the disk space that a volume uses, in bytes.
func (c *Client) VolumeDiskUsage(ctx context.Context, name string) (_ uint64, err error) {
	reply, err := c.api.request(ctx, "volumeDiskUsage", map[string]any{"volumeName": name})
	if err != nil {
		return 0, err
	}
	defer closeJoin(&err, reply)
	return reply.Uint64("volumeSize"), nil
}

// SystemDiskUsage returns the disk usage of images, containers, and volumes.
func (c *Client) SystemDiskUsage(ctx context.Context) (*DiskUsageStats, error) {
	var out DiskUsageStats
	ok, err := c.api.requestJSON(ctx, "systemDiskUsage", nil, "diskUsageStats", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeInternalError, Message: "no disk usage data returned"}
	}
	return &out, nil
}

// DefaultKernel returns the kernel that the system uses for new containers
// on the given platform. It returns an error that matches ErrNotFound if no
// default kernel is configured.
func (c *Client) DefaultKernel(ctx context.Context, platform SystemPlatform) (*Kernel, error) {
	data, err := encodeJSON(platform)
	if err != nil {
		return nil, err
	}
	var out Kernel
	ok, err := c.api.requestJSON(ctx, "getDefaultKernel", map[string]any{"systemPlatform": data}, "kernel", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeInternalError, Message: "missing kernel data from XPC response"}
	}
	return &out, nil
}

package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jen20/go-container/internal/xpc"
)

// NewConfiguration returns a configuration with DefaultCPUs,
// DefaultMemoryInBytes, DefaultCPUOverhead, and DefaultRuntimeHandler. The
// platform is linux/arm64, and the creation date is now.
//
// The configuration has no network. Use DefaultAttachment to join the
// built-in network, as `container run` does.
func NewConfiguration(id string, image ImageDescription, process ProcessConfiguration) Configuration {
	return Configuration{
		ID:          id,
		Image:       image,
		InitProcess: process,
		Platform:    Platform{OS: "linux", Architecture: "arm64"},
		Resources: Resources{
			CPUs:          DefaultCPUs,
			MemoryInBytes: DefaultMemoryInBytes,
			CPUOverhead:   DefaultCPUOverhead,
		},
		RuntimeHandler: DefaultRuntimeHandler,
		CreationDate:   Date{time.Now()},
	}
}

// NewProcessConfiguration returns a process configuration with the working
// directory "/" and the user 0:0.
func NewProcessConfiguration(executable string, arguments, environment []string) ProcessConfiguration {
	return ProcessConfiguration{
		Executable:       executable,
		Arguments:        arguments,
		Environment:      environment,
		WorkingDirectory: "/",
	}
}

// DefaultAttachment returns the attachment that `container run` uses when no
// network is given: the network, a hostname, and the default MTU. The
// hostname is the container ID, qualified with dnsDomain if dnsDomain is not
// empty and the ID has no dot.
func DefaultAttachment(network, containerID, dnsDomain string) AttachmentConfiguration {
	hostname := containerID
	switch {
	case strings.Contains(containerID, "."):
		hostname = containerID + "."
	case dnsDomain != "":
		hostname = containerID + "." + dnsDomain + "."
	}
	mtu := uint32(DefaultMTU)
	return AttachmentConfiguration{
		Network: network,
		Options: AttachmentOptions{Hostname: hostname, MTU: &mtu},
	}
}

// CreateContainerOptions are the optional parts of a create request.
type CreateContainerOptions struct {
	// AutoRemove deletes the container when it stops.
	AutoRemove bool
	// RootFSOverride replaces the root filesystem that the server makes
	// from the image.
	RootFSOverride *Filesystem
	// InitImage is the reference of the VM init image. Empty uses the
	// system default.
	InitImage string
	// RuntimeData is opaque data for the runtime plugin.
	RuntimeData []byte
}

// containerCreateOptions is the JSON form of the create options.
type containerCreateOptions struct {
	AutoRemove     bool        `json:"autoRemove"`
	RootFsOverride *Filesystem `json:"rootFsOverride,omitempty"`
}

// CreateContainer creates a container. The image in config must already be
// in the local image store, and kernel is usually the result of
// DefaultKernel. The new container is stopped. Call BootstrapContainer to
// start its init process.
func (c *Client) CreateContainer(ctx context.Context, config Configuration, kernel Kernel, opts *CreateContainerOptions) error {
	if opts == nil {
		opts = &CreateContainerOptions{}
	}
	cdata, err := encodeJSON(config)
	if err != nil {
		return err
	}
	kdata, err := encodeJSON(kernel)
	if err != nil {
		return err
	}
	odata, err := encodeJSON(containerCreateOptions{AutoRemove: opts.AutoRemove, RootFsOverride: opts.RootFSOverride})
	if err != nil {
		return err
	}
	values := xpc.Dictionary{"containerConfig": cdata, "kernel": kdata, "containerOptions": odata}
	if opts.InitImage != "" {
		values["initImage"] = opts.InitImage
	}
	if opts.RuntimeData != nil {
		values["runtimeData"] = opts.RuntimeData
	}
	return c.api.send(ctx, "containerCreate", values, DefaultTimeout)
}

// Stdio holds the standard streams for a process. A nil file leaves that
// stream unconnected. The server gets its own references to the files, so
// the caller can close its files after the call returns.
type Stdio struct {
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

func (s Stdio) set(values xpc.Dictionary) {
	if s.Stdin != nil {
		values["stdin"] = s.Stdin
	}
	if s.Stdout != nil {
		values["stdout"] = s.Stdout
	}
	if s.Stderr != nil {
		values["stderr"] = s.Stderr
	}
}

// BootstrapContainer starts the virtual machine of a created container and
// creates its init process with the given stdio. The process is created but
// not started. Call Start on the result. dynamicEnv holds environment
// variables that the server computes again on each start.
func (c *Client) BootstrapContainer(ctx context.Context, id string, stdio Stdio, dynamicEnv map[string]string) (*Process, error) {
	envData, err := encodeJSON(emptyMapIfNil(dynamicEnv))
	if err != nil {
		return nil, err
	}
	values := xpc.Dictionary{"id": id, "dynamicEnv": envData}
	stdio.set(values)
	if err := c.api.send(ctx, "containerBootstrap", values, 0); err != nil {
		return nil, err
	}
	return c.Process(id, id), nil
}

// CreateProcess creates a new process in a running container. The process
// is created but not started. Call Start on the result.
func (c *Client) CreateProcess(ctx context.Context, containerID, processID string, config ProcessConfiguration, stdio Stdio) (*Process, error) {
	data, err := encodeJSON(config)
	if err != nil {
		return nil, err
	}
	values := xpc.Dictionary{"id": containerID, "processIdentifier": processID, "processConfig": data}
	stdio.set(values)
	if err := c.api.send(ctx, "containerCreateProcess", values, 0); err != nil {
		return nil, err
	}
	return c.Process(containerID, processID), nil
}

// StopOptions controls how StopContainer stops a container.
type StopOptions struct {
	// TimeoutInSeconds is how long the server waits after the signal
	// before it kills the container.
	TimeoutInSeconds int32 `json:"timeoutInSeconds"`
	// Signal is the first signal to send. Empty uses the configured stop
	// signal, or SIGTERM.
	Signal string `json:"signal,omitempty"`
}

// DefaultStopOptions sends the configured stop signal, or SIGTERM, and kills
// the container if it does not stop within 5 seconds.
var DefaultStopOptions = StopOptions{TimeoutInSeconds: 5}

// StopContainer stops a container and all of its processes.
func (c *Client) StopContainer(ctx context.Context, id string, opts StopOptions) error {
	data, err := encodeJSON(opts)
	if err != nil {
		return err
	}
	return c.api.send(ctx, "containerStop", xpc.Dictionary{"id": id, "stopOptions": data}, 0)
}

// KillContainer sends a signal to the init process of a container. The
// signal is a Linux signal name with or without the SIG prefix, or a number,
// for example "KILL", "SIGTERM", or "9".
func (c *Client) KillContainer(ctx context.Context, id, signal string) error {
	return c.Process(id, id).Kill(ctx, signal)
}

// DeleteContainer deletes a container and its resources. With force, the
// server stops a running container first.
func (c *Client) DeleteContainer(ctx context.Context, id string, force bool) error {
	return c.api.send(ctx, "containerDelete", xpc.Dictionary{"id": id, "forceDelete": force}, 0)
}

// ContainerLogs returns two open files: the output log of the container
// processes and the boot log of the virtual machine, in that order. The
// caller must close them.
func (c *Client) ContainerLogs(ctx context.Context, id string) (output, boot *os.File, err error) {
	reply, err := c.api.requestTimeout(ctx, "containerLogs", xpc.Dictionary{"id": id}, 0)
	if err != nil {
		return nil, nil, err
	}
	logs, _ := reply["logs"].([]any)
	if len(logs) != 2 {
		return nil, nil, joinErrors(&Error{Code: CodeInternalError, Message: fmt.Sprintf("expected 2 log files, got %d", len(logs))}, reply.Close())
	}
	output, ok1 := logs[0].(*os.File)
	boot, ok2 := logs[1].(*os.File)
	if !ok1 || !ok2 {
		return nil, nil, joinErrors(&Error{Code: CodeInternalError, Message: "log values are not file descriptors"}, reply.Close())
	}
	return output, boot, nil
}

// DialContainer connects to a vsock port in a container and returns the
// connected socket.
func (c *Client) DialContainer(ctx context.Context, id string, port uint32) (*os.File, error) {
	reply, err := c.api.requestTimeout(ctx, "containerDial", xpc.Dictionary{"id": id, "port": uint64(port)}, 0)
	if err != nil {
		return nil, err
	}
	f := reply.File("fd")
	if f == nil {
		return nil, joinErrors(&Error{Code: CodeInternalError, Message: fmt.Sprintf("failed to get fd for vsock port %d", port)}, reply.Close())
	}
	delete(reply, "fd")
	if err := reply.Close(); err != nil {
		return nil, joinErrors(err, f.Close())
	}
	return f, nil
}

// CopyIn copies a host file or directory into a running container. mode is
// the file mode of the copy, for example 0o644. With createParents, the
// server creates missing parent directories of destination.
func (c *Client) CopyIn(ctx context.Context, id, source, destination string, mode uint32, createParents bool) error {
	return c.api.send(ctx, "containerCopyIn", xpc.Dictionary{
		"id": id, "sourcePath": source, "destinationPath": destination,
		"fileMode": uint64(mode), "createParents": createParents,
	}, 300*time.Second)
}

// CopyOut copies a file or directory from a running container to the host.
func (c *Client) CopyOut(ctx context.Context, id, source, destination string, createParents bool) error {
	return c.api.send(ctx, "containerCopyOut", xpc.Dictionary{
		"id": id, "sourcePath": source, "destinationPath": destination,
		"createParents": createParents,
	}, 300*time.Second)
}

// ExportContainer writes the root filesystem of a container to a tar archive
// at the given absolute host path.
func (c *Client) ExportContainer(ctx context.Context, id, archivePath string) error {
	return c.api.send(ctx, "containerExport", xpc.Dictionary{"id": id, "archive": archivePath}, 0)
}

// CleanContainer asks the runtime of a running container to clean it, as
// `container clean` does.
func (c *Client) CleanContainer(ctx context.Context, id string) error {
	return c.api.send(ctx, "containerClean", xpc.Dictionary{"id": id}, 0)
}

// Process is a process in a container. The init process has the same ID as
// its container.
type Process struct {
	c           *Client
	ContainerID string
	ID          string
}

// Process returns a handle for an existing process. It does not contact the
// server.
func (c *Client) Process(containerID, processID string) *Process {
	return &Process{c: c, ContainerID: containerID, ID: processID}
}

func (p *Process) values() xpc.Dictionary {
	return xpc.Dictionary{"id": p.ContainerID, "processIdentifier": p.ID}
}

// Start starts a created process.
//
// To learn how the process ends, prefer StartWait or Run. When the init
// process exits, the container stops and the server can no longer answer a
// wait for it. A Wait that is sent after that point fails with
// CodeInvalidState.
func (p *Process) Start(ctx context.Context) error {
	return p.c.api.send(ctx, "containerStartProcess", p.values(), 0)
}

// StartWait sends a wait request for the process and then starts it. It
// returns a function that waits for the exit status. Because the wait
// request reaches the server first, the server is already waiting when the
// process starts. This makes a lost exit status very unlikely, but the
// server handles the two requests concurrently, so it is not a guarantee.
//
// The caller must call the returned function. ctx applies to the wait too.
func (p *Process) StartWait(ctx context.Context) (wait func() (ExitStatus, error), err error) {
	pw, err := p.c.api.start(ctx, "containerWait", p.values(), 0)
	if err != nil {
		return nil, err
	}
	wait = func() (_ ExitStatus, err error) {
		reply, err := pw.wait()
		if err != nil {
			return ExitStatus{}, err
		}
		defer closeJoin(&err, reply)
		return exitStatus(reply), nil
	}
	if err := p.Start(ctx); err != nil {
		// The process did not start. Stop waiting for it. The cancelled
		// wait ends with context.Canceled, which says nothing new, but
		// any other error is reported with the start error.
		pw.cancel()
		if _, werr := wait(); werr != nil && !errors.Is(werr, context.Canceled) {
			err = joinErrors(err, werr)
		}
		return nil, err
	}
	return wait, nil
}

// Run starts the process and waits for it to exit. See StartWait.
func (p *Process) Run(ctx context.Context) (ExitStatus, error) {
	wait, err := p.StartWait(ctx)
	if err != nil {
		return ExitStatus{}, err
	}
	return wait()
}

// Kill sends a signal to the process. See KillContainer for the forms of
// signal.
func (p *Process) Kill(ctx context.Context, signal string) error {
	v := p.values()
	v["signal"] = signal
	return p.c.api.send(ctx, "containerKill", v, 0)
}

// Resize sets the terminal size of a process that has a terminal.
func (p *Process) Resize(ctx context.Context, width, height uint16) error {
	v := p.values()
	v["width"] = uint64(width)
	v["height"] = uint64(height)
	return p.c.api.send(ctx, "containerResize", v, 0)
}

// ExitStatus is how a process ended.
type ExitStatus struct {
	Code     int32
	ExitedAt time.Time
}

// Wait waits for the process to exit and returns its exit status. It has no
// timeout of its own. Use ctx to give up early. For a process that is not
// started yet, use StartWait instead, to avoid the race described at Start.
func (p *Process) Wait(ctx context.Context) (_ ExitStatus, err error) {
	reply, err := p.c.api.requestTimeout(ctx, "containerWait", p.values(), 0)
	if err != nil {
		return ExitStatus{}, err
	}
	defer closeJoin(&err, reply)
	return exitStatus(reply), nil
}

func exitStatus(reply xpc.Dictionary) ExitStatus {
	return ExitStatus{Code: int32(reply.Int64("exitCode")), ExitedAt: reply.Date("exitedAt")}
}

// send sends a request whose reply has no values.
func (s *service) send(ctx context.Context, route string, values xpc.Dictionary, timeout time.Duration) error {
	reply, err := s.requestTimeout(ctx, route, values, timeout)
	if err != nil {
		return err
	}
	return reply.Close()
}

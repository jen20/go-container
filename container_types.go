package container

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// RuntimeStatus is the run state of a container.
type RuntimeStatus string

// Runtime states.
const (
	StatusUnknown  RuntimeStatus = "unknown"
	StatusStopped  RuntimeStatus = "stopped"
	StatusRunning  RuntimeStatus = "running"
	StatusStopping RuntimeStatus = "stopping"
)

// Snapshot is the state of a container at one point in time.
type Snapshot struct {
	Configuration Configuration `json:"configuration"`
	Status        RuntimeStatus `json:"status"`
	Networks      []Attachment  `json:"networks"`
	StartedDate   *Date         `json:"startedDate,omitempty"`
}

// ID returns the container ID.
func (s *Snapshot) ID() string { return s.Configuration.ID }

// ListFilters selects containers for ListContainers. The zero value
// selects all containers.
type ListFilters struct {
	// IDs selects containers with one of these IDs. Empty selects all.
	IDs []string `json:"ids"`
	// Status selects containers in this state. Empty selects all.
	Status RuntimeStatus `json:"status,omitempty"`
	// Labels selects containers whose label values match these regular
	// expressions.
	Labels map[string]string `json:"labels"`
}

// MarshalJSON encodes nil IDs and Labels as empty values instead of null.
func (f ListFilters) MarshalJSON() ([]byte, error) {
	// The server requires the array and the map.
	type plain ListFilters
	p := plain(f)
	p.IDs = emptyIfNil(p.IDs)
	p.Labels = emptyMapIfNil(p.Labels)
	return json.Marshal(p)
}

// Defaults of NewConfiguration.
const (
	DefaultCPUs           = 4
	DefaultMemoryInBytes  = 1024 * 1024 * 1024
	DefaultCPUOverhead    = 1
	DefaultRuntimeHandler = "container-runtime-linux"

	// DefaultMTU is the MTU that the CLI sets on a network attachment.
	DefaultMTU = 1280
)

// Configuration is the configuration of a container.
type Configuration struct {
	ID               string                    `json:"id"`
	Image            ImageDescription          `json:"image"`
	Mounts           []Filesystem              `json:"mounts"`
	PublishedPorts   []PublishPort             `json:"publishedPorts"`
	PublishedSockets []PublishSocket           `json:"publishedSockets"`
	Labels           map[string]string         `json:"labels"`
	Sysctls          map[string]string         `json:"sysctls"`
	Networks         []AttachmentConfiguration `json:"networks"`
	DNS              *DNSConfiguration         `json:"dns,omitempty"`
	Rosetta          bool                      `json:"rosetta"`
	InitProcess      ProcessConfiguration      `json:"initProcess"`
	Platform         Platform                  `json:"platform"`
	Resources        Resources                 `json:"resources"`
	RuntimeHandler   string                    `json:"runtimeHandler"`
	Virtualization   bool                      `json:"virtualization"`
	SSH              bool                      `json:"ssh"`
	ReadOnly         bool                      `json:"readOnly"`
	UseInit          bool                      `json:"useInit"`
	CapAdd           []string                  `json:"capAdd"`
	CapDrop          []string                  `json:"capDrop"`
	ShmSize          *uint64                   `json:"shmSize,omitempty"`
	StopSignal       string                    `json:"stopSignal,omitempty"`
	// MaskedPaths and ReadonlyPaths are paths that the runtime masks or
	// makes read-only. Nil uses the runtime defaults. Empty means no paths.
	MaskedPaths   []string `json:"maskedPaths"`
	ReadonlyPaths []string `json:"readonlyPaths"`
	CreationDate  Date     `json:"creationDate"`
}

// DNSConfiguration is the DNS configuration of a container.
type DNSConfiguration struct {
	Nameservers   []string `json:"nameservers"`
	Domain        string   `json:"domain,omitempty"`
	SearchDomains []string `json:"searchDomains"`
	Options       []string `json:"options"`
}

// Resources is the CPU, memory, and storage allocation of a container.
type Resources struct {
	CPUs          int     `json:"cpus"`
	MemoryInBytes uint64  `json:"memoryInBytes"`
	Storage       *uint64 `json:"storage,omitempty"`
	CPUOverhead   int     `json:"cpuOverhead"`
}

// ImageDescription identifies the image of a container.
type ImageDescription struct {
	Reference  string     `json:"reference"`
	Descriptor Descriptor `json:"descriptor"`
}

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	URLs         []string          `json:"urls,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Platform     *Platform         `json:"platform,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
}

// Platform is an OCI platform.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

// ProcessConfiguration describes a process in a container.
type ProcessConfiguration struct {
	Executable         string   `json:"executable"`
	Arguments          []string `json:"arguments"`
	Environment        []string `json:"environment"`
	WorkingDirectory   string   `json:"workingDirectory"`
	Terminal           bool     `json:"terminal"`
	User               User     `json:"user"`
	SupplementalGroups []uint32 `json:"supplementalGroups"`
	Rlimits            []Rlimit `json:"rlimits"`
}

// Rlimit is a resource limit for a process.
type Rlimit struct {
	Limit string `json:"limit"`
	Soft  uint64 `json:"soft"`
	Hard  uint64 `json:"hard"`
}

// User is the user that a process runs as. If Name is set, it is a user
// string that the container resolves, such as "nobody" or "1000:1000".
// Otherwise UID and GID are used.
type User struct {
	Name string
	UID  uint32
	GID  uint32
}

func (u User) String() string {
	if u.Name != "" {
		return u.Name
	}
	return fmt.Sprintf("%d:%d", u.UID, u.GID)
}

// MarshalJSON encodes the user as a raw user string or as a UID and GID.
func (u User) MarshalJSON() ([]byte, error) {
	if u.Name != "" {
		return encodeEnum("raw", struct {
			UserString string `json:"userString"`
		}{u.Name})
	}
	return encodeEnum("id", struct {
		UID uint32 `json:"uid"`
		GID uint32 `json:"gid"`
	}{u.UID, u.GID})
}

// UnmarshalJSON decodes a raw user string or a UID and GID.
func (u *User) UnmarshalJSON(b []byte) error {
	name, payload, err := decodeEnum(b)
	if err != nil {
		return err
	}
	switch name {
	case "raw":
		var p struct {
			UserString string `json:"userString"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		*u = User{Name: p.UserString}
	case "id":
		var p struct {
			UID uint32 `json:"uid"`
			GID uint32 `json:"gid"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		*u = User{UID: p.UID, GID: p.GID}
	default:
		return fmt.Errorf("client: unknown user kind %q", name)
	}
	return nil
}

// Filesystem is a mount in a container.
type Filesystem struct {
	Type        FSType   `json:"type"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Options     []string `json:"options"`
}

// FSKind is the kind of a Filesystem.
type FSKind string

// Filesystem kinds.
const (
	FSBlock    FSKind = "block"
	FSVolume   FSKind = "volume"
	FSVirtiofs FSKind = "virtiofs"
	FSTmpfs    FSKind = "tmpfs"
)

// CacheMode is the cache mode of a block filesystem.
type CacheMode string

// Cache modes.
const (
	CacheOn   CacheMode = "on"
	CacheOff  CacheMode = "off"
	CacheAuto CacheMode = "auto"
)

// SyncMode is the sync mode of a block filesystem.
type SyncMode string

// Sync modes.
const (
	SyncFull   SyncMode = "full"
	SyncFsync  SyncMode = "fsync"
	SyncNosync SyncMode = "nosync"
)

// FSType is the type of a Filesystem. Name is set only for FSVolume.
// Format, Cache, and Sync are set only for FSBlock and FSVolume.
type FSType struct {
	Kind   FSKind
	Name   string
	Format string
	Cache  CacheMode
	Sync   SyncMode
}

type blockPayload struct {
	Name   string     `json:"name,omitempty"`
	Format string     `json:"format"`
	Cache  simpleEnum `json:"cache"`
	Sync   simpleEnum `json:"sync"`
}

// MarshalJSON encodes the type as an object keyed by its kind.
func (t FSType) MarshalJSON() ([]byte, error) {
	switch t.Kind {
	case FSBlock, FSVolume:
		p := blockPayload{Format: t.Format, Cache: simpleEnum(t.Cache), Sync: simpleEnum(t.Sync)}
		if t.Kind == FSVolume {
			p.Name = t.Name
		}
		return encodeEnum(string(t.Kind), p)
	case FSVirtiofs, FSTmpfs:
		return encodeEnum(string(t.Kind), nil)
	default:
		return nil, fmt.Errorf("client: unknown filesystem kind %q", t.Kind)
	}
}

// UnmarshalJSON decodes an object keyed by the kind of the type.
func (t *FSType) UnmarshalJSON(b []byte) error {
	name, payload, err := decodeEnum(b)
	if err != nil {
		return err
	}
	*t = FSType{Kind: FSKind(name)}
	switch t.Kind {
	case FSBlock, FSVolume:
		var p blockPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		t.Name, t.Format, t.Cache, t.Sync = p.Name, p.Format, CacheMode(p.Cache), SyncMode(p.Sync)
	}
	return nil
}

// PublishProtocol is the transport protocol of a published port.
type PublishProtocol string

// Published port protocols.
const (
	ProtocolTCP PublishProtocol = "tcp"
	ProtocolUDP PublishProtocol = "udp"
)

// PublishPort forwards a range of host ports to a container.
type PublishPort struct {
	HostAddress   netip.Addr      `json:"hostAddress"`
	HostPort      uint16          `json:"hostPort"`
	ContainerPort uint16          `json:"containerPort"`
	Proto         PublishProtocol `json:"proto"`
	// Count is the number of ports in the range, starting at HostPort and
	// ContainerPort. Zero means 1.
	Count uint16 `json:"count"`
}

// PublishSocket forwards a Unix socket in a container to a host path.
type PublishSocket struct {
	ContainerPath string `json:"containerPath"`
	HostPath      string `json:"hostPath"`
	// Permissions is the file mode of the host socket. Nil keeps the default.
	Permissions *FilePermissions `json:"permissions,omitempty"`
}

// FilePermissions is a Unix file mode. It encodes as a JSON number.
type FilePermissions uint16

// UnmarshalJSON accepts a number or an object with a rawValue key.
func (p *FilePermissions) UnmarshalJSON(b []byte) error {
	// The server sends a number, but the {"rawValue": n} form is accepted
	// too.
	var n uint16
	if err := json.Unmarshal(b, &n); err == nil {
		*p = FilePermissions(n)
		return nil
	}
	var obj struct {
		RawValue uint16 `json:"rawValue"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("client: invalid file permissions %s", b)
	}
	*p = FilePermissions(obj.RawValue)
	return nil
}

// Stats is a sample of the resource usage of a container. A nil field means
// that the runtime did not report it.
type Stats struct {
	ID               string  `json:"id"`
	MemoryUsageBytes *uint64 `json:"memoryUsageBytes,omitempty"`
	MemoryLimitBytes *uint64 `json:"memoryLimitBytes,omitempty"`
	CPUUsageUsec     *uint64 `json:"cpuUsageUsec,omitempty"`
	NetworkRxBytes   *uint64 `json:"networkRxBytes,omitempty"`
	NetworkTxBytes   *uint64 `json:"networkTxBytes,omitempty"`
	BlockReadBytes   *uint64 `json:"blockReadBytes,omitempty"`
	BlockWriteBytes  *uint64 `json:"blockWriteBytes,omitempty"`
	NumProcesses     *uint64 `json:"numProcesses,omitempty"`
}

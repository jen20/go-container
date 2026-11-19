package container

import "net/netip"

// Label keys and values that the container system uses on resources.
const (
	LabelPlugin = "com.apple.container.plugin"
	LabelRole   = "com.apple.container.resource.role"

	RoleBuilder = "builder"
	RoleBuiltin = "builtin"

	// LabelAnonymousVolume marks a volume that the system created for an
	// unnamed mount.
	LabelAnonymousVolume = "com.apple.container.resource.anonymous"
)

// NetworkMode is the mode of a network.
type NetworkMode string

// Network modes.
const (
	NetworkNAT      NetworkMode = "nat"
	NetworkHostOnly NetworkMode = "hostOnly"
)

// NetworkResource is a network and its current state.
type NetworkResource struct {
	ID            string               `json:"id"`
	Configuration NetworkConfiguration `json:"configuration"`
	Status        NetworkStatus        `json:"status"`
}

// IsBuiltin reports whether the system created the network.
func (n *NetworkResource) IsBuiltin() bool {
	return n.Configuration.Labels[LabelRole] == RoleBuiltin
}

// NetworkConfiguration is the configuration of a network.
type NetworkConfiguration struct {
	Name         string            `json:"name"`
	CreationDate Date              `json:"creationDate"`
	Mode         NetworkMode       `json:"mode"`
	IPv4Subnet   *netip.Prefix     `json:"ipv4Subnet,omitempty"`
	IPv6Subnet   *netip.Prefix     `json:"ipv6Subnet,omitempty"`
	Labels       map[string]string `json:"labels"`
	Plugin       string            `json:"plugin"`
	Options      map[string]string `json:"options"`
}

// NetworkStatus is the address state of a running network.
type NetworkStatus struct {
	IPv4Subnet  netip.Prefix  `json:"ipv4Subnet"`
	IPv4Gateway netip.Addr    `json:"ipv4Gateway"`
	IPv6Subnet  *netip.Prefix `json:"ipv6Subnet,omitempty"`
}

// AttachmentConfiguration asks for a container to join a network.
type AttachmentConfiguration struct {
	Network string            `json:"network"`
	Options AttachmentOptions `json:"options"`
}

// AttachmentOptions are the per-container settings on a network.
type AttachmentOptions struct {
	Hostname   string   `json:"hostname"`
	MACAddress string   `json:"macAddress,omitempty"`
	MTU        *uint32  `json:"mtu,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
}

// Attachment is the address that a container has on a network.
type Attachment struct {
	Network     string        `json:"network"`
	Hostname    string        `json:"hostname"`
	IPv4Address netip.Prefix  `json:"ipv4Address"`
	IPv4Gateway netip.Addr    `json:"ipv4Gateway"`
	IPv6Address *netip.Prefix `json:"ipv6Address,omitempty"`
	MACAddress  string        `json:"macAddress,omitempty"`
	MTU         *uint32       `json:"mtu,omitempty"`
	Variant     string        `json:"variant,omitempty"`
}

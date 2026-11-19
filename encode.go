package container

import "encoding/json"

// The server requires every non-optional array and dictionary, so a nil Go
// slice or map must not encode as null. An optional value that is nil is
// omitted. The MarshalJSON methods in this file produce that form.

// MarshalJSON encodes nil slices and maps as empty values, except that a nil
// MaskedPaths or ReadonlyPaths is omitted.
func (c Configuration) MarshalJSON() ([]byte, error) {
	type plain Configuration
	p := plain(c)
	p.Mounts = emptyIfNil(p.Mounts)
	p.PublishedPorts = emptyIfNil(p.PublishedPorts)
	p.PublishedSockets = emptyIfNil(p.PublishedSockets)
	p.Labels = emptyMapIfNil(p.Labels)
	p.Sysctls = emptyMapIfNil(p.Sysctls)
	p.Networks = emptyIfNil(p.Networks)
	p.CapAdd = emptyIfNil(p.CapAdd)
	p.CapDrop = emptyIfNil(p.CapDrop)
	return json.Marshal(struct {
		plain
		// These fields hide the ones in plain. A nil slice is omitted
		// and an empty slice is written as [].
		MaskedPaths   *[]string `json:"maskedPaths,omitempty"`
		ReadonlyPaths *[]string `json:"readonlyPaths,omitempty"`
	}{p, optionalSlice(c.MaskedPaths), optionalSlice(c.ReadonlyPaths)})
}

func optionalSlice[T any](s []T) *[]T {
	if s == nil {
		return nil
	}
	return &s
}

// MarshalJSON encodes nil slices as empty arrays.
func (p ProcessConfiguration) MarshalJSON() ([]byte, error) {
	type plain ProcessConfiguration
	q := plain(p)
	q.Arguments = emptyIfNil(q.Arguments)
	q.Environment = emptyIfNil(q.Environment)
	q.SupplementalGroups = emptyIfNil(q.SupplementalGroups)
	q.Rlimits = emptyIfNil(q.Rlimits)
	return json.Marshal(q)
}

// MarshalJSON encodes nil slices as empty arrays.
func (d DNSConfiguration) MarshalJSON() ([]byte, error) {
	type plain DNSConfiguration
	q := plain(d)
	q.Nameservers = emptyIfNil(q.Nameservers)
	q.SearchDomains = emptyIfNil(q.SearchDomains)
	q.Options = emptyIfNil(q.Options)
	return json.Marshal(q)
}

// MarshalJSON encodes nil Options as an empty array.
func (f Filesystem) MarshalJSON() ([]byte, error) {
	type plain Filesystem
	q := plain(f)
	q.Options = emptyIfNil(q.Options)
	return json.Marshal(q)
}

// MarshalJSON encodes nil Networks as an empty array.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type plain Snapshot
	q := plain(s)
	q.Networks = emptyIfNil(q.Networks)
	return json.Marshal(q)
}

// MarshalJSON encodes nil Labels and Options as empty objects.
func (n NetworkConfiguration) MarshalJSON() ([]byte, error) {
	type plain NetworkConfiguration
	q := plain(n)
	q.Labels = emptyMapIfNil(q.Labels)
	q.Options = emptyMapIfNil(q.Options)
	return json.Marshal(q)
}

// MarshalJSON encodes nil Labels and Options as empty objects.
func (v VolumeConfiguration) MarshalJSON() ([]byte, error) {
	type plain VolumeConfiguration
	q := plain(v)
	q.Labels = emptyMapIfNil(q.Labels)
	q.Options = emptyMapIfNil(q.Options)
	return json.Marshal(q)
}

// MarshalJSON encodes nil slices as empty arrays.
func (k KernelCommandLine) MarshalJSON() ([]byte, error) {
	type plain KernelCommandLine
	q := plain(k)
	q.KernelArgs = emptyIfNil(q.KernelArgs)
	q.InitArgs = emptyIfNil(q.InitArgs)
	return json.Marshal(q)
}

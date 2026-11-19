package container

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// decodeConfig applies values from the file layers to the client defaults.
func decodeConfig(layers []map[string]any) (SystemConfig, error) {
	cfg := DefaultSystemConfig()
	if len(layers) == 0 {
		return cfg, nil
	}
	d := decoder{layers: layers}
	d.decode(&cfg)
	if d.err == nil {
		d.err = cfg.Validate()
	}
	if d.err != nil {
		return SystemConfig{}, invalidArgument("failed to decode configuration: %v", d.err)
	}
	return cfg, nil
}

// decoder looks up each key in the layers in order and keeps the first
// error.
type decoder struct {
	layers []map[string]any
	err    error
}

func (d *decoder) decode(cfg *SystemConfig) {
	d.bool("build.rosetta", &cfg.Build.Rosetta)
	d.int("build.cpus", &cfg.Build.CPUs)
	d.memory("build.memory", &cfg.Build.Memory)
	d.string("build.image", &cfg.Build.Image)

	d.int("container.cpus", &cfg.Container.CPUs)
	d.memory("container.memory", &cfg.Container.Memory)
	if d.has("container.dns") {
		dns := &ContainerDNSDefaults{}
		d.string("container.dns.domain", &dns.Domain)
		d.strings("container.dns.nameservers", &dns.Nameservers)
		d.strings("container.dns.searchDomains", &dns.SearchDomains)
		d.strings("container.dns.options", &dns.Options)
		cfg.Container.DNS = dns
	}

	d.string("dns.domain", &cfg.DNS.Domain)

	d.string("kernel.binaryPath", &cfg.Kernel.BinaryPath)
	if d.string("kernel.url", &cfg.Kernel.URL) {
		if u, err := url.Parse(cfg.Kernel.URL); err != nil || cfg.Kernel.URL == "" || u.String() == "" {
			d.fail(fmt.Errorf("invalid kernel URL '%s'", cfg.Kernel.URL))
		}
	}
	if !d.string("kernel.digest", &cfg.Kernel.Digest) && cfg.Kernel.URL != DefaultKernelURL {
		d.fail(errors.New("kernel.digest is required when kernel.url is not the default URL"))
	}

	d.int("machine.cpus", &cfg.Machine.CPUs)
	d.memory("machine.memory", &cfg.Machine.Memory)
	var homeMount string
	if d.string("machine.homeMount", &homeMount) {
		cfg.Machine.HomeMount = HomeMount(homeMount)
	}
	d.bool("machine.virtualization", &cfg.Machine.Virtualization)
	d.string("machine.kernelPath", &cfg.Machine.KernelPath)

	d.prefix("network.subnet", false, &cfg.Network.Subnet)
	d.prefix("network.subnetv6", true, &cfg.Network.SubnetV6)

	d.string("registry.domain", &cfg.Registry.Domain)
	d.string("vminit.image", &cfg.VMInit.Image)
}

func (d *decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// lookup returns the value of a dotted key in the first layer that has it.
func (d *decoder) lookup(key string) (any, bool) {
	parts := strings.Split(key, ".")
	for _, layer := range d.layers {
		var v any = layer
		found := true
		for _, p := range parts {
			m, ok := v.(map[string]any)
			if !ok {
				found = false
				break
			}
			if v, ok = m[p]; !ok {
				found = false
				break
			}
		}
		if found {
			return v, true
		}
	}
	return nil, false
}

func (d *decoder) has(key string) bool {
	_, ok := d.lookup(key)
	return ok
}

// get sets *out to the value of key if the key is present and has type T.
// It reports whether the key is present.
func get[T any](d *decoder, key, typeName string, out *T) bool {
	v, ok := d.lookup(key)
	if !ok {
		return false
	}
	t, ok := v.(T)
	if !ok {
		d.fail(fmt.Errorf("%s: expected %s, got %T", key, typeName, v))
		return true
	}
	*out = t
	return true
}

func (d *decoder) string(key string, out *string) bool { return get(d, key, "a string", out) }

func (d *decoder) bool(key string, out *bool) bool { return get(d, key, "a boolean", out) }

func (d *decoder) int(key string, out *int) bool {
	var n int64
	if !get(d, key, "an integer", &n) {
		return false
	}
	*out = int(n)
	return true
}

func (d *decoder) strings(key string, out *[]string) {
	var list []any
	if !get(d, key, "an array of strings", &list) {
		return
	}
	s := make([]string, 0, len(list))
	for _, v := range list {
		str, ok := v.(string)
		if !ok {
			d.fail(fmt.Errorf("%s: expected an array of strings, got an element of type %T", key, v))
			return
		}
		s = append(s, str)
	}
	*out = s
}

func (d *decoder) memory(key string, out *MemorySize) {
	var s string
	if !d.string(key, &s) {
		return
	}
	m, err := ParseMemorySize(s)
	if err != nil {
		d.fail(fmt.Errorf("%s: %w", key, err))
		return
	}
	*out = m
}

func (d *decoder) prefix(key string, v6 bool, out **netip.Prefix) {
	var s string
	if !d.string(key, &s) {
		return
	}
	p, err := netip.ParsePrefix(s)
	if err == nil && p.Addr().Is6() != v6 {
		err = fmt.Errorf("wrong address family")
	}
	if err != nil {
		d.fail(fmt.Errorf("%s: invalid CIDR '%s': %w", key, s, err))
		return
	}
	*out = &p
}

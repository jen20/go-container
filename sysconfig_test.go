package container

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestParseMemorySize(t *testing.T) {
	tests := []struct {
		in    string
		bytes uint64
		text  string
	}{
		{"1024b", 1024, "1024b"},
		{"2048", 2048, "2048b"},
		{"512k", 512 << 10, "512kb"},
		{"512KB", 512 << 10, "512kb"},
		{"512kib", 512 << 10, "512kb"},
		{"2048mb", 2048 << 20, "2048mb"},
		{"1g", 1 << 30, "1gb"},
		{" 4 GiB ", 4 << 30, "4gb"},
		{"1.5g", 3 << 29, "1gb"},
		{"1t", 1 << 40, "1tb"},
		{"1p", 1 << 50, "1pb"},
	}
	for _, tt := range tests {
		m, err := ParseMemorySize(tt.in)
		if err != nil || m.Bytes() != tt.bytes || m.String() != tt.text {
			t.Errorf("ParseMemorySize(%q) = %d %q, %v; want %d %q", tt.in, m.Bytes(), m, err, tt.bytes, tt.text)
		}
	}
	for _, bad := range []string{"", "g", "1x", "1gx", "1.2.3g", "-1g"} {
		if _, err := ParseMemorySize(bad); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("ParseMemorySize(%q) gave %v, want ErrInvalidArgument", bad, err)
		}
	}
}

func TestMemorySizeJSONRoundTrip(t *testing.T) {
	for _, input := range []string{"1.5g", "0.5g", "1.001k", "2048MB", "9007199254740993b", "18446744073709551615b"} {
		t.Run(input, func(t *testing.T) {
			want, err := ParseMemorySize(input)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var got MemorySize
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if got.Bytes() != want.Bytes() {
				t.Errorf("round trip through %s returned %d bytes, want %d", b, got.Bytes(), want.Bytes())
			}
		})
	}
}

func TestSystemConfigOptions(t *testing.T) {
	ctx := t.Context()
	custom := DefaultSystemConfig()
	custom.Registry.Domain = "registry.example.com"
	loader := ConfigLoaderFunc(func(context.Context, ConfigRoots) (SystemConfig, error) {
		t.Error("loader called")
		return SystemConfig{}, nil
	})
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{"default", nil, DefaultRegistryDomain},
		{"fixed", []Option{WithConfig(custom)}, "registry.example.com"},
		{"fixed after loader", []Option{WithConfigLoader(loader), WithConfig(custom)}, "registry.example.com"},
	}
	for _, tt := range tests {
		c := NewClient(tt.opts...)
		cfg, err := c.SystemConfig(ctx)
		if err != nil || cfg.Registry.Domain != tt.want {
			t.Errorf("%s: registry %q, %v; want %q", tt.name, cfg.Registry.Domain, err, tt.want)
		}
		checkClose(t, c)
	}
}

func TestSystemConfigCopiesMutableFields(t *testing.T) {
	want := DefaultSystemConfig()
	want.Container.DNS = &ContainerDNSDefaults{
		Domain:        "example.com",
		Nameservers:   []string{"1.1.1.1"},
		SearchDomains: []string{"test"},
		Options:       []string{"ndots:2"},
	}
	want.Network.Subnet = new(netip.MustParsePrefix("192.168.0.0/24"))
	want.Network.SubnetV6 = new(netip.MustParsePrefix("fd00::/64"))

	original := want.clone()
	option := WithConfig(original)
	change := func(cfg SystemConfig) {
		cfg.Container.DNS.Domain = "modified"
		cfg.Container.DNS.Nameservers[0] = "modified"
		cfg.Container.DNS.SearchDomains[0] = "modified"
		cfg.Container.DNS.Options[0] = "modified"
		*cfg.Network.Subnet = netip.Prefix{}
		*cfg.Network.SubnetV6 = netip.Prefix{}
	}
	change(original)
	c := NewClient(option)
	t.Cleanup(func() { checkClose(t, c) })
	for range 2 {
		got, err := c.SystemConfig(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got.Container.DNS.Domain != want.Container.DNS.Domain ||
			!slices.Equal(got.Container.DNS.Nameservers, want.Container.DNS.Nameservers) ||
			!slices.Equal(got.Container.DNS.SearchDomains, want.Container.DNS.SearchDomains) ||
			!slices.Equal(got.Container.DNS.Options, want.Container.DNS.Options) ||
			*got.Network.Subnet != *want.Network.Subnet || *got.Network.SubnetV6 != *want.Network.SubnetV6 {
			t.Errorf("SystemConfig returned modified values: %+v, %+v", got.Container.DNS, got.Network)
		}
		change(got)
	}
}

func TestSystemConfigNormalize(t *testing.T) {
	cfg := DefaultSystemConfig()
	cfg.Registry.Domain = "registry.example.com"
	if got, _ := cfg.NormalizeReference("app"); got != "registry.example.com/app:latest" {
		t.Errorf("NormalizeReference(app) = %q", got)
	}
	if got, _ := cfg.DenormalizeReference("registry.example.com/app:latest"); got != "app:latest" {
		t.Errorf("DenormalizeReference = %q", got)
	}
	// The vminit image is not normalized, even when it is a short name.
	cfg.VMInit.Image = "vminit:latest"
	if got, _ := cfg.NormalizeReference("vminit:latest"); got != "vminit:latest" {
		t.Errorf("NormalizeReference(vminit) = %q", got)
	}
}

func TestDNSFromSystem(t *testing.T) {
	cfg := DefaultSystemConfig()
	if d := dnsFromSystem("", &cfg); d.Domain != "" || d.Nameservers != nil {
		t.Errorf("no settings: %+v", d)
	}
	cfg.DNS.Domain = "test"
	if d := dnsFromSystem("", &cfg); d.Domain != "test" {
		t.Errorf("host domain: %+v", d)
	}
	cfg.Container.DNS = &ContainerDNSDefaults{Domain: "inner", Nameservers: []string{"1.1.1.1"}, Options: []string{"ndots:2"}}
	d := dnsFromSystem("", &cfg)
	if d.Domain != "inner" || !slices.Equal(d.Nameservers, []string{"1.1.1.1"}) || !slices.Equal(d.Options, []string{"ndots:2"}) {
		t.Errorf("container defaults: %+v", d)
	}
	if d := dnsFromSystem("flag", &cfg); d.Domain != "flag" {
		t.Errorf("domain option: %+v", d)
	}
}

func TestSystemConfigValidate(t *testing.T) {
	cfg := DefaultSystemConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for name, change := range map[string]func(*SystemConfig){
		"cpus":   func(c *SystemConfig) { c.Machine.CPUs = 0 },
		"memory": func(c *SystemConfig) { c.Machine.Memory = mustMemorySize("512mb") },
		"home":   func(c *SystemConfig) { c.Machine.HomeMount = "sometimes" },
	} {
		c := DefaultSystemConfig()
		change(&c)
		if err := c.Validate(); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s: %v, want ErrInvalidArgument", name, err)
		}
	}
}

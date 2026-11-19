package container

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestConfigFileLoader(t *testing.T) {
	user := map[string]any{
		"container": map[string]any{
			"cpus": int64(8),
			"dns":  map[string]any{"nameservers": []any{"1.1.1.1"}},
		},
		"dns":      map[string]any{"domain": "test"},
		"registry": map[string]any{"domain": "registry.example.com"},
		"kernel":   map[string]any{"url": "https://example.com/kernel.tar"},
	}
	system := map[string]any{
		"container": map[string]any{
			"cpus":   int64(2),
			"memory": "4g",
			"dns":    map[string]any{"nameservers": []any{"8.8.8.8"}, "searchDomains": []any{"example.com"}},
		},
		"registry": map[string]any{"domain": "ignored.example.com"},
		"kernel":   map[string]any{"digest": "sha256:abc"},
		"network":  map[string]any{"subnet": "192.168.100.0/24", "subnetv6": "fd00:abcd::/64"},
		"machine":  map[string]any{"homeMount": "ro"},
	}
	roots := ConfigRoots{AppRoot: t.TempDir(), InstallRoot: t.TempDir()}
	for _, paths := range [][]string{nil, {"user.toml", "system.toml"}} {
		c := NewClient(WithConfigFileLoader(func(_ context.Context, files ...string) ([]map[string]any, error) {
			want := paths
			if len(want) == 0 {
				want = roots.ConfigFiles()
			}
			if !slices.Equal(files, want) {
				t.Errorf("loader paths = %v, want %v", files, want)
			}
			return []map[string]any{user, system}, nil
		}, paths...))
		t.Cleanup(func() { checkClose(t, c) })
		got, err := c.loader.LoadSystemConfig(t.Context(), roots)
		if err != nil {
			t.Fatal(err)
		}
		want := DefaultSystemConfig()
		want.Container.CPUs = 8
		want.Container.Memory = mustMemorySize("4g")
		want.Container.DNS = &ContainerDNSDefaults{
			Nameservers:   []string{"1.1.1.1"},
			SearchDomains: []string{"example.com"},
		}
		want.DNS.Domain = "test"
		want.Registry.Domain = "registry.example.com"
		want.Kernel.URL = "https://example.com/kernel.tar"
		want.Kernel.Digest = "sha256:abc"
		want.Machine.HomeMount = HomeMountReadOnly
		if got.Network.Subnet == nil || got.Network.Subnet.String() != "192.168.100.0/24" ||
			got.Network.SubnetV6 == nil || got.Network.SubnetV6.String() != "fd00:abcd::/64" {
			t.Errorf("network = %+v", got.Network)
		}
		want.Network = got.Network
		if !reflect.DeepEqual(got, want) {
			t.Errorf("configuration = %+v, want %+v", got, want)
		}
	}
}

func TestDecodeConfigValidation(t *testing.T) {
	cfg, err := decodeConfig(nil)
	if err != nil || !reflect.DeepEqual(cfg, DefaultSystemConfig()) {
		t.Errorf("missing files returned %+v, %v", cfg, err)
	}
	for _, tc := range []struct {
		section string
		values  map[string]any
	}{
		{"container", map[string]any{"cpus": "four"}},
		{"container", map[string]any{"memory": "lots"}},
		{"container", map[string]any{"dns": map[string]any{"nameservers": []any{int64(1)}}}},
		{"network", map[string]any{"subnet": "fd00::/64"}},
		{"machine", map[string]any{"cpus": int64(0)}},
		{"machine", map[string]any{"homeMount": "sometimes"}},
		{"kernel", map[string]any{"url": "https://example.com/kernel.tar"}},
	} {
		_, err := decodeConfig([]map[string]any{{tc.section: tc.values}})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s %v returned %v, want an invalid argument error", tc.section, tc.values, err)
		}
	}
}

package container

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jen20/go-container/internal/testenv"
)

// TestLiveDefaultSystemConfig compares DefaultSystemConfig with the merged
// configuration that the CLI prints. It is the same only on a system with
// no config.toml, so the test skips when either file exists.
func TestLiveDefaultSystemConfig(t *testing.T) {
	c := liveClient(t)
	ctx := liveContext(t)
	roots := liveRoots(t, c)
	for _, f := range roots.ConfigFiles() {
		if _, err := os.Stat(f); err == nil {
			t.Skipf("%s exists", f)
		}
	}
	cli, err := exec.LookPath("container")
	if err != nil {
		t.Skip("no container CLI on PATH")
	}
	out, err := exec.CommandContext(ctx, cli, "system", "property", "list", "--format", "json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(DefaultSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, d := range jsonDiff("", want, got) {
		t.Error(d)
	}
}

// TestLiveConfigLoader checks that the client gives the loader the roots
// from ping and calls it once.
func TestLiveConfigLoader(t *testing.T) {
	testenv.RequireLive(t)
	var calls atomic.Int32
	var roots ConfigRoots
	source := DefaultSystemConfig()
	source.Registry.Domain = "registry.example.com"
	source.Container.DNS = &ContainerDNSDefaults{Nameservers: []string{"1.1.1.1"}}
	loader := ConfigLoaderFunc(func(_ context.Context, r ConfigRoots) (SystemConfig, error) {
		calls.Add(1)
		roots = r
		return source, nil
	})
	c := NewClient(WithConfigLoader(loader))
	t.Cleanup(func() { checkClose(t, c) })
	ctx := liveContext(t)
	for i := range 2 {
		cfg, err := c.SystemConfig(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Registry.Domain != "registry.example.com" {
			t.Errorf("registry %q", cfg.Registry.Domain)
		}
		if cfg.Container.DNS.Nameservers[0] != "1.1.1.1" {
			t.Errorf("cached nameservers were modified: %v", cfg.Container.DNS.Nameservers)
		}
		cfg.Container.DNS.Nameservers[0] = "8.8.8.8"
		if i == 0 {
			if source.Container.DNS.Nameservers[0] != "1.1.1.1" {
				t.Error("changing the first result changed the loader's configuration")
			}
			source.Container.DNS.Nameservers[0] = "9.9.9.9"
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("loader called %d times", n)
	}
	for _, dir := range []string{roots.AppRoot, roots.InstallRoot} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("root %q: %v", dir, err)
		}
	}
	if !strings.HasSuffix(strings.TrimSuffix(roots.AppRoot, "/"), "com.apple.container") {
		t.Errorf("app root %q", roots.AppRoot)
	}
}

func liveRoots(t *testing.T, c *Client) ConfigRoots {
	t.Helper()
	h, err := c.Ping(liveContext(t))
	if err != nil {
		t.Fatal(err)
	}
	app, err := fileURLPath(h.AppRoot)
	if err != nil {
		t.Fatal(err)
	}
	install, err := fileURLPath(h.InstallRoot)
	if err != nil {
		t.Fatal(err)
	}
	return ConfigRoots{AppRoot: app, InstallRoot: install}
}

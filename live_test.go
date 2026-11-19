package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/testenv"
	"github.com/jen20/go-container/internal/xpc"
)

// The tests in this file talk to the real API server. Set
// GO_CONTAINER_TEST_LIVE=1 and start the server with
// `container system start` to run them. They only use read-only routes.

func liveClient(t *testing.T) *Client {
	t.Helper()
	testenv.RequireLive(t)
	c := NewClient()
	t.Cleanup(func() { checkClose(t, c) })
	return c
}

func liveContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestLivePing(t *testing.T) {
	c := liveClient(t)
	h, err := c.Ping(liveContext(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", h)
}

// TestLiveRoundTrip decodes each JSON reply into the Go types, encodes the
// result, and compares it with the server's JSON. A difference means that a
// Go type is missing a field or encodes one in a different form.
func TestLiveRoundTrip(t *testing.T) {
	c := liveClient(t)
	ctx := liveContext(t)
	filters, _ := json.Marshal(ListFilters{})
	platform, _ := json.Marshal(LinuxArm64)
	tests := []struct {
		route    string
		values   xpc.Dictionary
		replyKey string
		out      any
	}{
		{"containerList", xpc.Dictionary{"listFilters": filters}, "containers", &[]Snapshot{}},
		{"networkList", nil, "networkResources", &[]NetworkResource{}},
		{"volumeList", nil, "volumes", &[]VolumeConfiguration{}},
		{"systemDiskUsage", nil, "diskUsageStats", &DiskUsageStats{}},
		{"getDefaultKernel", xpc.Dictionary{"systemPlatform": platform}, "kernel", &Kernel{}},
		{"imageList", nil, "imageDescriptions", &[]ImageDescription{}},
	}
	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			rc := &c.api
			if strings.HasPrefix(tt.route, "image") {
				rc = &c.images
			}
			reply, err := rc.request(ctx, tt.route, tt.values)
			if errors.Is(err, ErrNotFound) {
				t.Skip(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer checkClose(t, reply)
			raw := reply.Data(tt.replyKey)
			if raw == nil {
				t.Skipf("no %s in reply", tt.replyKey)
			}
			if err := json.Unmarshal(raw, tt.out); err != nil {
				t.Fatalf("decode: %v\n%s", err, raw)
			}
			again, err := json.Marshal(tt.out)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(again, &got); err != nil {
				t.Fatal(err)
			}
			if diffs := jsonDiff("", want, got); len(diffs) > 0 {
				t.Errorf("round trip changed %d values:\n%s", len(diffs), strings.Join(diffs, "\n"))
			}
		})
	}
}

func TestLiveTypedRoutes(t *testing.T) {
	c := liveClient(t)
	ctx := liveContext(t)
	containers, err := c.ListContainers(ctx, ListFilters{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range containers {
		got, err := c.GetContainer(ctx, s.ID())
		if err != nil || got.ID() != s.ID() {
			t.Fatalf("GetContainer(%s): %v", s.ID(), err)
		}
		if _, err := c.ContainerDiskUsage(ctx, s.ID()); err != nil {
			t.Errorf("ContainerDiskUsage(%s): %v", s.ID(), err)
		}
		if s.Status == StatusRunning {
			if _, err := c.ContainerStats(ctx, s.ID()); err != nil {
				t.Errorf("ContainerStats(%s): %v", s.ID(), err)
			}
		}
	}
	if _, err := c.GetContainer(ctx, "go-container-no-such-container"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetContainer of a missing container gave %v", err)
	}
	networks, err := c.ListNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range networks {
		if _, err := c.GetNetwork(ctx, n.ID); err != nil {
			t.Errorf("GetNetwork(%s): %v", n.ID, err)
		}
	}
	volumes, err := c.ListVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range volumes {
		if _, err := c.InspectVolume(ctx, v.Name); err != nil {
			t.Errorf("InspectVolume(%s): %v", v.Name, err)
		}
		if _, err := c.VolumeDiskUsage(ctx, v.Name); err != nil {
			t.Errorf("VolumeDiskUsage(%s): %v", v.Name, err)
		}
	}
	if _, err := c.InspectVolume(ctx, "go-container-no-such-volume"); err == nil {
		t.Error("InspectVolume of a missing volume succeeded")
	} else {
		t.Logf("missing volume: %v", err)
	}
	if _, err := c.SystemDiskUsage(ctx); err != nil {
		t.Error(err)
	}
	t.Logf("%d containers, %d networks, %d volumes", len(containers), len(networks), len(volumes))
}

// jsonDiff lists the paths where two decoded JSON values differ.
func jsonDiff(path string, want, got any) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: want object, got %v", path, got)}
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		var sorted []string
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		var out []string
		for _, k := range sorted {
			wv, wok := w[k]
			gv, gok := g[k]
			switch {
			case !gok:
				out = append(out, fmt.Sprintf("%s.%s: missing in Go output (server sent %v)", path, k, wv))
			case !wok:
				out = append(out, fmt.Sprintf("%s.%s: extra in Go output (%v)", path, k, gv))
			default:
				out = append(out, jsonDiff(path+"."+k, wv, gv)...)
			}
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return []string{fmt.Sprintf("%s: want %v, got %v", path, want, got)}
		}
		var out []string
		for i := range w {
			out = append(out, jsonDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
		}
		return out
	default:
		if !reflect.DeepEqual(want, got) {
			return []string{fmt.Sprintf("%s: want %v, got %v", path, want, got)}
		}
		return nil
	}
}

// checkClose closes c and reports a failure to t. It is safe to call from
// other goroutines, and it is suitable for defer.
func checkClose(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

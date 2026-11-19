package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/testenv"
)

// The tests in this file create and delete volumes and networks. They run
// only when both GO_CONTAINER_TEST_LIVE and GO_CONTAINER_TEST_WRITE are set.

func TestLiveVolumeCreateDelete(t *testing.T) {
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)
	name := "go-client-test-" + randomSuffix(t)

	v, err := c.CreateVolume(ctx, name, &CreateVolumeOptions{
		DriverOptions: map[string]string{"size": "64M"},
		Labels:        map[string]string{"test": "go-client"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The 1.7.1 server reports a missing volume as invalidArgument.
		err := c.DeleteVolume(cleanupContext(t), name)
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("cleanup: %v", err)
		}
	})
	if v.Name != name || v.Driver != DefaultVolumeDriver || v.Labels["test"] != "go-client" {
		t.Errorf("CreateVolume = %+v", v)
	}

	// The 1.7.1 server reports a duplicate name as invalidArgument.
	_, err = c.CreateVolume(ctx, name, nil)
	if !errors.Is(err, ErrExists) && !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("second CreateVolume error = %v, want ErrExists or ErrInvalidArgument", err)
	}

	got, err := c.InspectVolume(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != v.Source {
		t.Errorf("InspectVolume source = %q, want %q", got.Source, v.Source)
	}

	if err := c.DeleteVolume(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InspectVolume(ctx, name); err == nil {
		t.Error("InspectVolume after DeleteVolume succeeded")
	}
}

func TestLiveNetworkCreateDelete(t *testing.T) {
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)
	name := "go-client-test-" + randomSuffix(t)

	config := NewNetworkConfiguration(name, NetworkNAT)
	config.Labels = map[string]string{"test": "go-client"}
	n, err := c.CreateNetwork(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.DeleteNetwork(cleanupContext(t), name); err != nil && !errors.Is(err, ErrNotFound) {
			t.Errorf("cleanup: %v", err)
		}
	})
	if n.ID != name || n.Configuration.Labels["test"] != "go-client" || !n.Status.IPv4Subnet.IsValid() {
		t.Errorf("CreateNetwork = %+v", n)
	}
	if n.IsBuiltin() {
		t.Error("new network is builtin")
	}

	got, err := c.GetNetwork(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.IPv4Subnet != n.Status.IPv4Subnet {
		t.Errorf("GetNetwork subnet = %v, want %v", got.Status.IPv4Subnet, n.Status.IPv4Subnet)
	}

	builtin, err := c.BuiltinNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteNetwork(ctx, builtin.ID); err == nil {
		t.Errorf("DeleteNetwork(%s) succeeded on the builtin network", builtin.ID)
	}

	if err := c.DeleteNetwork(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetNetwork(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetNetwork after DeleteNetwork error = %v, want ErrNotFound", err)
	}
}

// TestLiveInstallKernelMissingFile checks the installKernel keys without
// changing the default kernel. The server must reject a missing file
// because the file does not exist, not because of the path form.
func TestLiveInstallKernelMissingFile(t *testing.T) {
	c := liveClient(t)
	err := c.InstallKernel(liveContext(t), "/nonexistent/go-client-test/vmlinux", LinuxArm64, false)
	if err == nil {
		t.Fatal("InstallKernel with a missing file succeeded")
	}
	if strings.Contains(err.Error(), "URL type") {
		t.Errorf("server did not read the path as a file URL: %v", err)
	}
	t.Log(err)
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// cleanupContext returns a context for use in a cleanup function, after the
// test context is done.
func cleanupContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

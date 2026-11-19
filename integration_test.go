//go:build integration

package container_test

// These tests run against a real container API server. Run them with:
//
//	go test -tags integration .
//
// The server must be running (`container system start`). The tests create
// resources with names that start with "go-container-it-" and remove them
// again. They do not call routes that change the whole system, such as
// DeleteContentExcept, CleanUpOrphanedBlobs, or InstallKernel.
//
// Tests that boot a container VM run only when GO_CONTAINER_TEST_VM=1.
// GitHub's hosted macOS runners cannot start nested VMs, so CI skips
// them. Run them on an Apple silicon Mac.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jen20/go-container"
	"github.com/jen20/go-container/internal/testenv"
)

// baseImage is a small image that the tests pull. If the local store
// already has it, the tests leave it there.
const baseImage = "docker.io/library/alpine:3.22"

var (
	c *container.Client

	imageOnce   sync.Once
	image       *container.ImageDescription
	imageErr    error
	imagePulled bool // the tests pulled baseImage and must remove it
)

func TestMain(m *testing.M) {
	c = container.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, err := c.Ping(ctx)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: the container API server is not reachable: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if imagePulled {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := c.DeleteSnapshot(ctx, image, nil); err != nil {
			fmt.Fprintf(os.Stderr, "integration: delete snapshot of %s: %v\n", baseImage, err)
		}
		if err := c.DeleteImage(ctx, baseImage, false); err != nil {
			fmt.Fprintf(os.Stderr, "integration: delete %s: %v\n", baseImage, err)
		}
		cancel()
	}
	if err := c.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: close: %v\n", err)
	}
	os.Exit(code)
}

// testContext returns a context for one test that ends after d.
func testContext(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

// cleanup registers fn to run after the test with its own context, because
// the test context is done by then.
func cleanup(t *testing.T, what string, fn func(ctx context.Context) error) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := fn(ctx); err != nil {
			t.Errorf("clean up %s: %v", what, err)
		}
	})
}

// uniqueName returns a resource name that no other test run uses.
func uniqueName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "go-container-it-" + hex.EncodeToString(b)
}

// requireImage returns baseImage, and pulls and unpacks it on first use.
func requireImage(t *testing.T) *container.ImageDescription {
	t.Helper()
	imageOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := c.GetImage(ctx, baseImage)
		switch {
		case err == nil:
		case errors.Is(err, container.ErrNotFound):
			imagePulled = true
		default:
			imageErr = err
			return
		}
		if image, imageErr = c.FetchImage(ctx, baseImage, &container.PullOptions{Platform: &container.LinuxArm64Platform}); imageErr != nil {
			return
		}
		_, imageErr = c.GetCreateSnapshot(ctx, image, container.LinuxArm64Platform, nil)
	})
	if imageErr != nil {
		t.Fatalf("prepare %s: %v", baseImage, imageErr)
	}
	return image
}

func TestPing(t *testing.T) {
	h, err := c.Ping(testContext(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h.AppRoot, "file://") || !strings.HasPrefix(h.InstallRoot, "file://") {
		t.Errorf("roots are not file URLs: %q, %q", h.AppRoot, h.InstallRoot)
	}
	t.Logf("API server %s (%s)", h.APIServerVersion, h.APIServerCommit)
}

func TestSystemConfig(t *testing.T) {
	cfg, err := c.SystemConfig(testContext(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry.Domain != container.DefaultRegistryDomain {
		t.Errorf("registry domain = %q, want %q", cfg.Registry.Domain, container.DefaultRegistryDomain)
	}
}

func TestDiskUsage(t *testing.T) {
	ctx := testContext(t, time.Minute)
	if _, err := c.SystemDiskUsage(ctx); err != nil {
		t.Fatalf("SystemDiskUsage: %v", err)
	}
	if _, err := c.ImageDiskUsage(ctx, nil); err != nil {
		t.Fatalf("ImageDiskUsage: %v", err)
	}
	if _, err := c.ContentSize(ctx); err != nil {
		t.Fatalf("ContentSize: %v", err)
	}
}

func TestDefaultKernel(t *testing.T) {
	k, err := c.DefaultKernel(testContext(t, 30*time.Second), container.LinuxArm64)
	if errors.Is(err, container.ErrNotFound) {
		t.Skip("no default kernel is installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Path, "file://") {
		t.Errorf("kernel path %q is not a file URL", k.Path)
	}
}

func TestContainerNotFound(t *testing.T) {
	ctx := testContext(t, 30*time.Second)
	if _, err := c.ListContainers(ctx, container.ListFilters{}); err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if _, err := c.GetContainer(ctx, uniqueName(t)); !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("GetContainer of a missing container: err = %v, want ErrNotFound", err)
	}
}

func TestNetworkLifecycle(t *testing.T) {
	ctx := testContext(t, 2*time.Minute)
	if _, err := c.BuiltinNetwork(ctx); err != nil {
		t.Fatalf("BuiltinNetwork: %v", err)
	}
	name := uniqueName(t)
	n, err := c.CreateNetwork(ctx, container.NewNetworkConfiguration(name, container.NetworkNAT))
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	cleanup(t, "network "+name, func(ctx context.Context) error {
		if deleted {
			return nil
		}
		return c.DeleteNetwork(ctx, name)
	})
	if n.ID != name || n.IsBuiltin() {
		t.Errorf("created network: ID = %q, builtin = %v", n.ID, n.IsBuiltin())
	}
	if _, err := c.GetNetwork(ctx, name); err != nil {
		t.Fatalf("GetNetwork: %v", err)
	}
	if err := c.DeleteNetwork(ctx, name); err != nil {
		t.Fatalf("DeleteNetwork: %v", err)
	}
	deleted = true
	if _, err := c.GetNetwork(ctx, name); !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("GetNetwork after delete: err = %v, want ErrNotFound", err)
	}
}

func TestVolumeLifecycle(t *testing.T) {
	ctx := testContext(t, 2*time.Minute)
	name := uniqueName(t)
	v, err := c.CreateVolume(ctx, name, &container.CreateVolumeOptions{
		DriverOptions: map[string]string{"size": "64M"},
		Labels:        map[string]string{"test": "integration"},
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	cleanup(t, "volume "+name, func(ctx context.Context) error {
		if deleted {
			return nil
		}
		return c.DeleteVolume(ctx, name)
	})
	if v.Name != name || v.Labels["test"] != "integration" || v.IsAnonymous() {
		t.Errorf("created volume = %+v", v)
	}
	if _, err := c.InspectVolume(ctx, name); err != nil {
		t.Fatalf("InspectVolume: %v", err)
	}
	list, err := c.ListVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(list, func(v container.VolumeConfiguration) bool { return v.Name == name }) {
		t.Errorf("ListVolumes does not include %s", name)
	}
	if _, err := c.VolumeDiskUsage(ctx, name); err != nil {
		t.Fatalf("VolumeDiskUsage: %v", err)
	}
	if err := c.DeleteVolume(ctx, name); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	deleted = true
	// The 1.7.1 server reports a missing volume as an invalid argument.
	if _, err := c.InspectVolume(ctx, name); !errors.Is(err, container.ErrInvalidArgument) && !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("InspectVolume after delete: err = %v", err)
	}
}

func TestImageInspection(t *testing.T) {
	img := requireImage(t)
	ctx := testContext(t, 2*time.Minute)
	if img.Reference != baseImage {
		t.Errorf("reference = %q, want %q", img.Reference, baseImage)
	}
	short, err := container.DenormalizeReference(img.Reference, "")
	if err != nil || short != "alpine:3.22" {
		t.Errorf("DenormalizeReference = %q, %v", short, err)
	}
	if got, err := c.GetImage(ctx, "alpine:3.22"); err != nil || got.Descriptor.Digest != img.Descriptor.Digest {
		t.Errorf("GetImage of the short name: %v, %v", got, err)
	}
	platforms, err := c.ImagePlatforms(ctx, baseImage, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(platforms, container.LinuxArm64Platform.Equal) {
		t.Errorf("platforms %v do not include linux/arm64", platforms)
	}
	cfg, err := c.ImageConfig(ctx, img, container.LinuxArm64Platform)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OS != "linux" || cfg.Architecture != "arm64" {
		t.Errorf("image config is for %s/%s", cfg.OS, cfg.Architecture)
	}
	if size, err := c.ImageSize(ctx, img); err != nil || size <= 0 {
		t.Errorf("ImageSize = %d, %v", size, err)
	}
	fs, err := c.GetSnapshot(ctx, img, container.LinuxArm64Platform)
	if err != nil {
		t.Fatal(err)
	}
	if fs.Type.Kind != container.FSBlock {
		t.Errorf("snapshot kind = %q, want block", fs.Type.Kind)
	}
}

func TestImagePullProgress(t *testing.T) {
	requireImage(t)
	ctx := testContext(t, 5*time.Minute)
	var mu sync.Mutex
	var p container.Progress
	img, err := c.PullImage(ctx, baseImage, &container.PullOptions{
		Platform: &container.LinuxArm64Platform,
		Progress: func(events []container.ProgressEvent) {
			mu.Lock()
			defer mu.Unlock()
			p.Apply(events)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if img.Reference != baseImage {
		t.Errorf("reference = %q", img.Reference)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("progress: %+v", p)
}

func TestImageTagSaveLoad(t *testing.T) {
	img := requireImage(t)
	ctx := testContext(t, 5*time.Minute)
	tag := "localhost/" + uniqueName(t) + ":test"
	tagged, err := c.TagImage(ctx, img.Reference, tag)
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, "image "+tag, func(ctx context.Context) error {
		err := c.DeleteImage(ctx, tag, false)
		if errors.Is(err, container.ErrNotFound) {
			return nil
		}
		return err
	})
	if tagged.Reference != tag || tagged.Descriptor.Digest != img.Descriptor.Digest {
		t.Errorf("tagged image = %+v", tagged)
	}

	archive := filepath.Join(t.TempDir(), "image.tar")
	if err := c.SaveImages(ctx, []string{tag}, archive, &container.LinuxArm64Platform); err != nil {
		t.Fatalf("SaveImages: %v", err)
	}
	if err := c.DeleteImage(ctx, tag, false); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}
	if _, err := c.GetImage(ctx, tag); !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("GetImage after delete: err = %v, want ErrNotFound", err)
	}
	res, err := c.LoadImages(ctx, archive, false)
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	if len(res.RejectedMembers) > 0 {
		t.Errorf("rejected members: %v", res.RejectedMembers)
	}
	if !slices.ContainsFunc(res.Images, func(i container.ImageDescription) bool { return i.Reference == tag }) {
		t.Errorf("loaded images %v do not include %s", res.Images, tag)
	}
}

func TestContentIngest(t *testing.T) {
	ctx := testContext(t, 2*time.Minute)
	// Random content, so that the blob cannot belong to an image.
	blob := make([]byte, 4096)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}
	var digest string
	ingested, err := c.Ingest(ctx, func(s *container.IngestSession) error {
		var err error
		digest, _, err = s.WriteBlob(bytes.NewReader(blob))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	cleanup(t, "blob "+digest, func(ctx context.Context) error {
		if removed {
			return nil
		}
		_, _, err := c.DeleteContent(ctx, []string{digest})
		return err
	})
	if hexDigest := strings.TrimPrefix(digest, "sha256:"); !slices.Contains(ingested, hexDigest) {
		t.Fatalf("ingested %v, want %s", ingested, hexDigest)
	}
	path, err := c.ContentPath(ctx, digest)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, blob) {
		t.Fatalf("blob at %s differs: %v", path, err)
	}
	deleted, _, err := c.DeleteContent(ctx, []string{digest})
	if err != nil {
		t.Fatal(err)
	}
	removed = true
	if len(deleted) != 1 {
		t.Errorf("DeleteContent removed %v", deleted)
	}
	if _, err := c.ContentPath(ctx, digest); !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("ContentPath after delete: err = %v, want ErrNotFound", err)
	}
}

func TestIngestCancel(t *testing.T) {
	ctx := testContext(t, time.Minute)
	errStop := errors.New("stop")
	_, err := c.Ingest(ctx, func(s *container.IngestSession) error {
		if _, _, err := s.WriteBlob(strings.NewReader("discarded")); err != nil {
			return err
		}
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("Ingest: err = %v, want the error from fn", err)
	}
}

// createContainer creates a stopped container from baseImage that runs
// args, and removes it after the test.
func createContainer(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	requireImage(t)
	id := uniqueName(t)
	if _, err := c.CreateContainerFromImage(ctx, id, baseImage, &container.ImageContainerOptions{
		Entrypoint: args[0],
		Arguments:  args[1:],
		Labels:     map[string]string{"test": "integration"},
	}); err != nil {
		t.Fatal(err)
	}
	cleanup(t, "container "+id, func(ctx context.Context) error {
		return c.DeleteContainer(ctx, id, true)
	})
	return id
}

// pipe returns a pipe whose write end goes to the server, and a function
// that returns everything written to it once the server closes its copy.
func pipe(t *testing.T) (w *os.File, read func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close pipe: %v", err)
		}
	})
	return w, func() string {
		select {
		case s := <-done:
			return s
		case <-time.After(30 * time.Second):
			t.Fatal("timed out reading process output")
			return ""
		}
	}
}

// closeFile closes our copy of a file that the server holds.
func closeFile(t *testing.T, f *os.File) {
	t.Helper()
	if err := f.Close(); err != nil {
		t.Errorf("close %s: %v", f.Name(), err)
	}
}

func TestContainerRunToExit(t *testing.T) {
	testenv.RequireVM(t)
	ctx := testContext(t, 5*time.Minute)
	id := createContainer(t, ctx, "/bin/sh", "-c", "echo hello from alpine; exit 3")

	w, output := pipe(t)
	proc, err := c.BootstrapContainer(ctx, id, container.Stdio{Stdout: w}, nil)
	closeFile(t, w)
	if err != nil {
		t.Fatal(err)
	}
	status, err := proc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Code != 3 {
		t.Errorf("exit code = %d, want 3", status.Code)
	}
	if got := output(); got != "hello from alpine\n" {
		t.Errorf("output = %q", got)
	}
}

func TestContainerExecAndCopy(t *testing.T) {
	testenv.RequireVM(t)
	ctx := testContext(t, 5*time.Minute)
	id := createContainer(t, ctx, "/bin/sleep", "300")

	proc, err := c.BootstrapContainer(ctx, id, container.Stdio{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	snap, err := c.GetContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != container.StatusRunning {
		t.Fatalf("status = %q, want running", snap.Status)
	}
	if snap.Configuration.Labels["test"] != "integration" {
		t.Errorf("labels = %v", snap.Configuration.Labels)
	}
	if _, err := c.ContainerStats(ctx, id); err != nil {
		t.Errorf("ContainerStats: %v", err)
	}

	exec := func(args ...string) (string, int32) {
		t.Helper()
		w, output := pipe(t)
		p, err := c.CreateProcess(ctx, id, uniqueName(t),
			container.NewProcessConfiguration(args[0], args[1:], []string{"PATH=/usr/bin:/bin"}),
			container.Stdio{Stdout: w})
		closeFile(t, w)
		if err != nil {
			t.Fatal(err)
		}
		status, err := p.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return output(), status.Code
	}
	if out, code := exec("/bin/cat", "/etc/alpine-release"); code != 0 || !strings.HasPrefix(out, "3.22") {
		t.Errorf("cat /etc/alpine-release: %q, exit code %d", out, code)
	}

	src := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(src, []byte("copied in\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.CopyIn(ctx, id, src, "/tmp/in.txt", 0o644, false); err != nil {
		t.Fatalf("CopyIn: %v", err)
	}
	if out, code := exec("/bin/cat", "/tmp/in.txt"); code != 0 || out != "copied in\n" {
		t.Errorf("cat /tmp/in.txt: %q, exit code %d", out, code)
	}
	dst := filepath.Join(t.TempDir(), "out.txt")
	if err := c.CopyOut(ctx, id, "/tmp/in.txt", dst, false); err != nil {
		t.Fatalf("CopyOut: %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "copied in\n" {
		t.Errorf("copied out %q, %v", got, err)
	}

	output, boot, err := c.ContainerLogs(ctx, id)
	if err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	closeFile(t, output)
	closeFile(t, boot)

	if err := c.StopContainer(ctx, id, container.DefaultStopOptions); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	snap, err = c.GetContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != container.StatusStopped {
		t.Errorf("status after stop = %q, want stopped", snap.Status)
	}
}

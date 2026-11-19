package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/testenv"
)

func TestLiveImages(t *testing.T) {
	c := liveClient(t)
	ctx := liveContext(t)
	img, err := c.GetImage(ctx, "debian:trixie-slim")
	if errors.Is(err, ErrNotFound) {
		t.Skip("debian:trixie-slim is not in the local image store")
	}
	if err != nil {
		t.Fatal(err)
	}
	if img.Reference != testImage {
		t.Fatalf("short name found %s", img.Reference)
	}
	index, err := c.ImageIndex(ctx, img)
	if err != nil {
		t.Fatal(err)
	}
	platforms := index.RunnablePlatforms()
	found := false
	for _, p := range platforms {
		found = found || p.Equal(LinuxArm64Platform)
	}
	if !found {
		t.Fatalf("no linux/arm64 in %v", platforms)
	}
	if got, err := c.ImagePlatforms(ctx, "debian:trixie-slim", false); err != nil || len(got) != len(platforms) {
		t.Errorf("ImagePlatforms: %v, %v", got, err)
	}
	m, err := c.ImageManifest(ctx, img, Platform{OS: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Layers) == 0 {
		t.Error("manifest has no layers")
	}
	cfg, err := c.ImageConfig(ctx, img, LinuxArm64Platform)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Config == nil || len(cfg.Config.Cmd) == 0 || cfg.Architecture != "arm64" {
		t.Errorf("image config %+v", cfg)
	}
	// A platform that the index does not list is unsupported. (A platform
	// that it lists but that was not pulled gives ErrNotFound instead.)
	if _, err := c.ImageManifest(ctx, img, Platform{OS: "plan9", Architecture: "arm64"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("missing platform gave %v", err)
	}
	fs, err := c.GetSnapshot(ctx, img, LinuxArm64Platform)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if fs.Type.Kind != FSBlock || fs.Source == "" {
		t.Errorf("snapshot %+v", fs)
	}
	if _, err := c.ContentPath(ctx, "sha256:"+strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing content gave %v", err)
	}
	if _, err := c.GetImage(ctx, "go-container-no-such-image"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing image gave %v", err)
	}
}

// progressLog collects progress events from any goroutine.
type progressLog struct {
	mu     sync.Mutex
	events int
	state  Progress
}

func (p *progressLog) fn(events []ProgressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events += len(events)
	p.state.Apply(events)
}

func (p *progressLog) get() (int, Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.events, p.state
}

func TestLivePullAndRunFromImage(t *testing.T) {
	testenv.RequireWrite(t)
	testenv.RequireVM(t)
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	const ref = "alpine:latest"
	const full = "docker.io/library/alpine:latest"

	_, err := c.GetImage(ctx, ref)
	hadImage := err == nil
	if !hadImage {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := c.DeleteImage(ctx, full, true); err != nil {
				t.Errorf("DeleteImage: %v", err)
			}
		})
	}

	var pull progressLog
	img, err := c.PullImage(ctx, ref, &PullOptions{Platform: &LinuxArm64Platform, Progress: pull.fn})
	if err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if img.Reference != full {
		t.Errorf("pulled %s", img.Reference)
	}
	n, state := pull.get()
	t.Logf("pull: %d events, final %+v (image was local before: %v)", n, state, hadImage)
	if !hadImage && (n == 0 || state.TotalSize == 0) {
		t.Errorf("a fresh pull reported no progress: %d events, %+v", n, state)
	}

	b := make([]byte, 4)
	rand.Read(b)
	suffix := hex.EncodeToString(b)
	tag := "docker.io/library/go-container-test:" + suffix
	tagged, err := c.TagImage(ctx, full, tag)
	if err != nil {
		t.Fatalf("TagImage: %v", err)
	}
	if tagged.Reference != tag || tagged.Descriptor.Digest != img.Descriptor.Digest {
		t.Errorf("tagged %+v", tagged)
	}
	if err := c.DeleteImage(ctx, tag, false); err != nil {
		t.Errorf("DeleteImage(tag): %v", err)
	}
	if _, err := c.GetImage(ctx, tag); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted tag is still there: %v", err)
	}

	id := "go-container-test-" + suffix
	var unpack progressLog
	config, err := c.CreateContainerFromImage(ctx, id, ref, &ImageContainerOptions{
		Arguments:     []string{"/bin/sh", "-c", `echo "cwd=$(pwd) uid=$(id -u) mode=$MODE"`},
		Env:           []string{"MODE=test"},
		CPUs:          1,
		MemoryInBytes: 512 * 1024 * 1024,
		Progress:      unpack.fn,
	})
	if err != nil {
		t.Fatalf("CreateContainerFromImage: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := c.DeleteContainer(ctx, id, true); err != nil {
			t.Errorf("DeleteContainer: %v", err)
		}
	})
	n, state = unpack.get()
	t.Logf("create: %d progress events, final %+v; process %+v", n, state, config.InitProcess)

	stdout, readOut := pipe(t)
	proc, err := c.BootstrapContainer(ctx, id, Stdio{Stdout: stdout}, nil)
	checkClose(t, stdout)
	if err != nil {
		t.Fatalf("BootstrapContainer: %v", err)
	}
	st, err := proc.Run(ctx)
	if err != nil || st.Code != 0 {
		t.Fatalf("Run: %+v %v", st, err)
	}
	if got := strings.TrimSpace(readOut()); got != "cwd=/ uid=0 mode=test" {
		t.Errorf("output %q", got)
	}
}

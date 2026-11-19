package container

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jen20/go-container/internal/testenv"
)

// The tests in this file need the image in testImage in the local image
// store. TestLiveImageReads only reads. The other tests need
// GO_CONTAINER_TEST_WRITE, because they add references to the image store
// and remove them again.

func liveTestImage(t *testing.T, c *Client) *ImageDescription {
	t.Helper()
	img, err := c.GetImage(liveContext(t), testImage)
	if errors.Is(err, ErrNotFound) {
		t.Skipf("%s is not in the local image store", testImage)
	}
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestLiveImageReads(t *testing.T) {
	c := liveClient(t)
	ctx := liveContext(t)
	img := liveTestImage(t, c)

	found, missing, err := c.GetImages(ctx, []string{"debian:trixie-slim", "go-container-no-such-image"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Reference != testImage || !slices.Equal(missing, []string{"go-container-no-such-image"}) {
		t.Errorf("GetImages = %v, %v", found, missing)
	}

	size, err := c.ImageSize(ctx, img)
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.ImageManifest(ctx, img, LinuxArm64Platform)
	if err != nil {
		t.Fatal(err)
	}
	if size <= m.Config.Size {
		t.Errorf("ImageSize = %d, config alone is %d", size, m.Config.Size)
	}

	all, err := c.ListImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := c.ImageDiskUsage(ctx, []string{testImage})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total != len(all) || usage.Active != 1 || usage.SizeInBytes == 0 || usage.Reclaimable > usage.SizeInBytes {
		t.Errorf("ImageDiskUsage = %+v with %d images", usage, len(all))
	}
	none, err := c.ImageDiskUsage(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if none.Active != 0 || none.Reclaimable != none.SizeInBytes {
		t.Errorf("ImageDiskUsage(nil) = %+v", none)
	}
}

func TestLiveSaveLoad(t *testing.T) {
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)
	img := liveTestImage(t, c)

	// Save under a new tag, so that loading the archive adds a reference
	// that the test can remove.
	ref := "docker.io/library/go-client-test:" + randomSuffix(t)
	tagged, err := c.TagImage(ctx, img.Reference, ref)
	if err != nil {
		t.Fatal(err)
	}
	removeImage := func() {
		err := c.DeleteImage(cleanupContext(t), ref, false)
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteImage(%s): %v", ref, err)
		}
	}
	t.Cleanup(removeImage)

	archive := filepath.Join(t.TempDir(), "image.tar")
	if err := c.SaveImages(ctx, []string{ref}, archive, &LinuxArm64Platform); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(archive); err != nil || fi.Size() == 0 {
		t.Fatalf("archive: %v, %v", fi, err)
	}
	err = c.SaveImages(ctx, []string{ref, "go-container-no-such-image"}, archive, nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("SaveImages with a missing image gave %v, want ErrInvalidArgument", err)
	}

	removeImage()
	if _, err := c.GetImage(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetImage after DeleteImage gave %v", err)
	}
	result, err := c.LoadImages(ctx, archive, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Images) != 1 || result.Images[0].Reference != ref || len(result.RejectedMembers) != 0 {
		t.Errorf("LoadImages = %+v", result)
	}
	// The archive has a new index with one platform, so compare the
	// manifest of that platform.
	saved, err := c.ImageManifest(ctx, tagged, LinuxArm64Platform)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := c.ImageManifest(ctx, &result.Images[0], LinuxArm64Platform)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Digest != saved.Config.Digest {
		t.Errorf("loaded config %s, saved %s", loaded.Config.Digest, saved.Config.Digest)
	}
}

// TestLivePushRejected checks the imagePush keys without a registry. The
// push has no platform, so the server first reads the content of every
// platform in the index. The test image has only linux/arm64 locally, so the
// server must refuse the push before it connects to the registry. A push of
// one platform to a port where nothing listens does not fail quickly,
// because the server retries, as `container image push --platform` does.
func TestLivePushRejected(t *testing.T) {
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)
	img := liveTestImage(t, c)

	ref := "localhost:1/go-client-test:" + randomSuffix(t)
	tagged, err := c.TagImage(ctx, img.Reference, ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.DeleteImage(cleanupContext(t), ref, false); err != nil {
			t.Errorf("DeleteImage(%s): %v", ref, err)
		}
	})
	var log progressLog
	err = c.PushImage(ctx, tagged, &PushOptions{Insecure: true, Progress: log.fn})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("push with missing platform content gave %v, want ErrNotFound", err)
	}
	t.Log(err)

	if err := c.PushImage(ctx, &ImageDescription{Reference: "debian"}, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("push without a domain gave %v, want ErrInvalidArgument", err)
	}
}

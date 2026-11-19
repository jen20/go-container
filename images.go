package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jen20/go-container/internal/xpc"
)

// ListImages returns the images in the local image store.
func (c *Client) ListImages(ctx context.Context) ([]ImageDescription, error) {
	var out []ImageDescription
	ok, err := c.images.requestJSON(ctx, "imageList", nil, "imageDescriptions", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeEmpty, Message: "imageDescriptions not received"}
	}
	return out, nil
}

// GetImage finds a local image the way the container CLI does. It first
// looks for a locally built image whose name annotation matches the
// reference, then for an image whose reference equals the reference or its
// normalized form. "debian:trixie-slim" finds
// "docker.io/library/debian:trixie-slim". It returns an error that matches
// ErrNotFound if there is no such image.
func (c *Client) GetImage(ctx context.Context, reference string) (*ImageDescription, error) {
	cfg, err := c.SystemConfig(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	return searchImages(list, reference, &cfg)
}

// searchImages finds reference in all, as GetImage does. It returns an
// error that matches ErrNotFound if no image matches.
func searchImages(all []ImageDescription, reference string, cfg *SystemConfig) (*ImageDescription, error) {
	r, err := ParseReference(reference)
	if err != nil {
		return nil, err
	}
	r.Normalize()
	withTag := r.String()
	var built []*ImageDescription
	for i := range all {
		if all[i].Descriptor.Annotations[AnnotationImageName] == withTag {
			built = append(built, &all[i])
		}
	}
	if len(built) == 1 {
		return built[0], nil
	}
	for _, img := range built {
		if img.Reference == withTag {
			return img, nil
		}
	}
	normalized, err := cfg.NormalizeReference(reference)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Reference == reference || all[i].Reference == normalized {
			return &all[i], nil
		}
	}
	return nil, &Error{Code: CodeNotFound, Message: fmt.Sprintf(
		"image %s (%s) not found locally; you may wish to pull it with `container image pull %s`", reference, normalized, reference)}
}

// PullOptions are the options of PullImage.
type PullOptions struct {
	// Platform pulls only this platform. Nil pulls all platforms.
	Platform *Platform
	// Insecure uses plain HTTP for the registry.
	Insecure bool
	// MaxConcurrentDownloads limits parallel layer downloads. Zero means 3.
	MaxConcurrentDownloads int
	// Progress receives download progress. It can be nil.
	Progress ProgressFunc
}

// PullImage pulls an image from its registry into the local image store and
// returns it. The reference is normalized first.
func (c *Client) PullImage(ctx context.Context, reference string, opts *PullOptions) (*ImageDescription, error) {
	if opts == nil {
		opts = &PullOptions{}
	}
	downloads := opts.MaxConcurrentDownloads
	if downloads == 0 {
		downloads = 3
	}
	if downloads < 0 {
		return nil, invalidArgument("maximum number of concurrent downloads must be greater than 0, got %d", downloads)
	}
	cfg, err := c.SystemConfig(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := cfg.NormalizeReference(reference)
	if err != nil {
		return nil, err
	}
	values := xpc.Dictionary{
		"imageReference":         normalized,
		"insecureFlag":           opts.Insecure,
		"maxConcurrentDownloads": int64(downloads),
	}
	if err := setPlatform(values, opts.Platform); err != nil {
		return nil, err
	}
	return c.imageDescriptionRequest(ctx, "imagePull", values, opts.Progress)
}

// FetchImage returns a local image, and pulls it if it is not local. If
// opts.Platform is set, a local image must have that platform.
func (c *Client) FetchImage(ctx context.Context, reference string, opts *PullOptions) (*ImageDescription, error) {
	img, err := c.GetImage(ctx, reference)
	if err == nil {
		if opts != nil && opts.Platform != nil {
			if _, err := c.ImageConfig(ctx, img, *opts.Platform); err != nil {
				return nil, err
			}
		}
		return img, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return c.PullImage(ctx, reference, opts)
}

// ImagePlatforms returns the platforms that an image can run on. It reads a
// local image if there is one, and asks the registry otherwise.
func (c *Client) ImagePlatforms(ctx context.Context, reference string, insecure bool) ([]Platform, error) {
	if img, err := c.GetImage(ctx, reference); err == nil {
		index, err := c.ImageIndex(ctx, img)
		if err == nil {
			return index.RunnablePlatforms(), nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	cfg, err := c.SystemConfig(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := cfg.NormalizeReference(reference)
	if err != nil {
		return nil, err
	}
	var out []Platform
	ok, err := c.images.requestJSON(ctx, "imagePlatforms", xpc.Dictionary{"imageReference": normalized, "insecureFlag": insecure}, "ociPlatforms", &out)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeEmpty, Message: "platforms not received"}
	}
	return out, nil
}

// TagImage adds a new reference to a local image and returns the new image.
func (c *Client) TagImage(ctx context.Context, reference, newReference string) (*ImageDescription, error) {
	return c.imageDescriptionRequest(ctx, "imageTag", xpc.Dictionary{"imageReference": reference, "imageNewReference": newReference}, nil)
}

// DeleteImage removes a reference from the local image store. With
// garbageCollect, the server also removes content that no image uses.
func (c *Client) DeleteImage(ctx context.Context, reference string, garbageCollect bool) error {
	return c.images.send(ctx, "imageDelete", xpc.Dictionary{"imageReference": reference, "garbageCollect": garbageCollect}, 0)
}

// UnpackImage unpacks an image into a snapshot for one platform, or for all
// platforms if platform is nil.
func (c *Client) UnpackImage(ctx context.Context, img *ImageDescription, platform *Platform, progress ProgressFunc) (err error) {
	values, err := imageValues(img, platform)
	if err != nil {
		return err
	}
	pr, err := startProgress(progress, values)
	if err != nil {
		return err
	}
	defer func() { err = joinErrors(err, pr.stop()) }()
	return c.images.send(ctx, "imageUnpack", values, 0)
}

// GetSnapshot returns the unpacked root filesystem of an image for a
// platform. It returns an error that matches ErrNotFound if the image is
// not unpacked for that platform.
func (c *Client) GetSnapshot(ctx context.Context, img *ImageDescription, platform Platform) (*Filesystem, error) {
	values, err := imageValues(img, &platform)
	if err != nil {
		return nil, err
	}
	var fs Filesystem
	ok, err := c.images.requestJSON(ctx, "snapshotGet", values, "filesystem", &fs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeEmpty, Message: "filesystem not received"}
	}
	return &fs, nil
}

// GetCreateSnapshot returns the snapshot of an image for a platform, and
// unpacks the image first if needed.
func (c *Client) GetCreateSnapshot(ctx context.Context, img *ImageDescription, platform Platform, progress ProgressFunc) (*Filesystem, error) {
	fs, err := c.GetSnapshot(ctx, img, platform)
	if !errors.Is(err, ErrNotFound) {
		return fs, err
	}
	if err := c.UnpackImage(ctx, img, &platform, progress); err != nil {
		return nil, err
	}
	return c.GetSnapshot(ctx, img, platform)
}

// DeleteSnapshot removes the unpacked snapshot of an image for a platform,
// or for all platforms if platform is nil.
func (c *Client) DeleteSnapshot(ctx context.Context, img *ImageDescription, platform *Platform) error {
	values, err := imageValues(img, platform)
	if err != nil {
		return err
	}
	return c.images.send(ctx, "snapshotDelete", values, 0)
}

// ContentPath returns the path of a blob in the local content store. It
// returns an error that matches ErrNotFound if the store has no such blob.
func (c *Client) ContentPath(ctx context.Context, digest string) (_ string, err error) {
	reply, err := c.images.request(ctx, "contentGet", xpc.Dictionary{"digest": digest})
	if err != nil {
		return "", err
	}
	defer closeJoin(&err, reply)
	path := reply.String("contentPath")
	if path == "" {
		return "", &Error{Code: CodeNotFound, Message: "content with digest " + digest}
	}
	return path, nil
}

// readContent decodes a JSON blob from the local content store.
func (c *Client) readContent(ctx context.Context, digest string, out any) error {
	path, err := c.ContentPath(ctx, digest)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("client: decode content %s: %w", digest, err)
	}
	return nil
}

// ImageIndex returns the OCI index of a local image.
func (c *Client) ImageIndex(ctx context.Context, img *ImageDescription) (*Index, error) {
	var index Index
	if err := c.readContent(ctx, img.Descriptor.Digest, &index); err != nil {
		return nil, err
	}
	return &index, nil
}

// ImageManifest returns the manifest of a local image for a platform.
func (c *Client) ImageManifest(ctx context.Context, img *ImageDescription, platform Platform) (*Manifest, error) {
	index, err := c.ImageIndex(ctx, img)
	if err != nil {
		return nil, err
	}
	for _, d := range index.Manifests {
		if d.Platform != nil && d.Platform.Equal(platform) {
			var m Manifest
			if err := c.readContent(ctx, d.Digest, &m); err != nil {
				return nil, err
			}
			return &m, nil
		}
	}
	return nil, &Error{Code: CodeUnsupported, Message: "platform " + platform.String()}
}

// ImageConfig returns the OCI configuration of a local image for a platform.
func (c *Client) ImageConfig(ctx context.Context, img *ImageDescription, platform Platform) (*Image, error) {
	m, err := c.ImageManifest(ctx, img, platform)
	if err != nil {
		return nil, err
	}
	var out Image
	if err := c.readContent(ctx, m.Config.Digest, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// imageDescriptionRequest sends an image request with optional progress and
// decodes the imageDescription in the reply.
func (c *Client) imageDescriptionRequest(ctx context.Context, route string, values xpc.Dictionary, progress ProgressFunc) (*ImageDescription, error) {
	pr, err := startProgress(progress, values)
	if err != nil {
		return nil, err
	}
	var out ImageDescription
	ok, err := c.images.requestJSONTimeout(ctx, route, values, "imageDescription", &out, 0)
	if err := joinErrors(err, pr.stop()); err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Code: CodeEmpty, Message: "imageDescription not received"}
	}
	return &out, nil
}

func imageValues(img *ImageDescription, platform *Platform) (xpc.Dictionary, error) {
	data, err := encodeJSON(img)
	if err != nil {
		return nil, err
	}
	values := xpc.Dictionary{"imageDescription": data}
	return values, setPlatform(values, platform)
}

func setPlatform(values xpc.Dictionary, platform *Platform) error {
	if platform == nil {
		return nil
	}
	data, err := encodeJSON(platform)
	if err != nil {
		return err
	}
	values["ociPlatform"] = data
	return nil
}

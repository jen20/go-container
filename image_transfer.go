package container

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/jen20/go-container/internal/xpc"
)

// DenormalizeReference returns the short form of an image reference, as the
// container CLI shows it. It removes registryDomain, or
// DefaultRegistryDomain if registryDomain is empty, and on Docker Hub also
// removes "library/". For example, "docker.io/library/debian:trixie-slim"
// becomes "debian:trixie-slim". A reference on another registry does not
// change.
func DenormalizeReference(reference, registryDomain string) (string, error) {
	if registryDomain == "" {
		registryDomain = DefaultRegistryDomain
	}
	r, err := ParseReference(reference)
	if err != nil {
		return "", err
	}
	if r.Domain != registryDomain {
		return reference, nil
	}
	short := r.Path
	switch {
	case r.Tag != "":
		short += ":" + r.Tag
	case r.Digest != "":
		short += "@" + r.Digest
	}
	if registryDomain == dockerRegistryHost || registryDomain == legacyDockerRegistryHost {
		short = strings.TrimPrefix(short, defaultDockerRepo+"/")
	}
	return short, nil
}

// GetImages finds several local images in one list request, the way
// GetImage finds one. It returns the images that it found, in the order of
// names, and the names that match no image or are not valid references.
func (c *Client) GetImages(ctx context.Context, names []string) (found []ImageDescription, missing []string, err error) {
	cfg, err := c.SystemConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	all, err := c.ListImages(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range names {
		img, err := searchImages(all, name, &cfg)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		found = append(found, *img)
	}
	return found, missing, nil
}

// ImageSize returns the size in bytes of an image for the first platform in
// its index that has a local manifest: the index entry, the configuration,
// and the layers. Attestation manifests do not count. It returns 0 if no
// platform has a local manifest.
func (c *Client) ImageSize(ctx context.Context, img *ImageDescription) (int64, error) {
	index, err := c.ImageIndex(ctx, img)
	if err != nil {
		return 0, err
	}
	for _, d := range index.Manifests {
		if d.Annotations[annotationReferenceType] == attestationManifest || d.Platform == nil {
			continue
		}
		m, err := c.ImageManifest(ctx, img, *d.Platform)
		if err != nil {
			// Skip a platform whose manifest cannot be read.
			continue
		}
		size := d.Size + m.Config.Size
		for _, l := range m.Layers {
			size += l.Size
		}
		return size, nil
	}
	return 0, nil
}

// PushOptions are the options of PushImage.
type PushOptions struct {
	// Platform pushes only this platform. Nil pushes all platforms.
	Platform *Platform
	// Insecure uses plain HTTP for the registry.
	Insecure bool
	// Progress receives upload progress. It can be nil.
	Progress ProgressFunc
}

// PushImage pushes a local image to the registry in its reference. The
// reference must have a domain, as the references in the local image store
// do.
func (c *Client) PushImage(ctx context.Context, img *ImageDescription, opts *PushOptions) (err error) {
	if opts == nil {
		opts = &PushOptions{}
	}
	r, err := ParseReference(img.Reference)
	if err != nil {
		return err
	}
	if r.Domain == "" {
		return invalidArgument("could not extract host from reference %s", img.Reference)
	}
	values := xpc.Dictionary{
		"imageReference": img.Reference,
		"insecureFlag":   opts.Insecure,
	}
	if err := setPlatform(values, opts.Platform); err != nil {
		return err
	}
	pr, err := startProgress(opts.Progress, values)
	if err != nil {
		return err
	}
	defer func() { err = joinErrors(err, pr.stop()) }()
	return c.images.send(ctx, "imagePush", values, 0)
}

// SaveImages writes local images to an OCI archive at path. A relative path
// is relative to the current directory. If platform is not nil, the archive
// has only that platform. It returns an error with CodeInvalidArgument if a
// reference matches no local image.
func (c *Client) SaveImages(ctx context.Context, references []string, path string, platform *Platform) error {
	images, missing, err := c.GetImages(ctx, references)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return invalidArgument("one or more image references are invalid: %s", strings.Join(missing, ", "))
	}
	data, err := encodeJSON(emptyIfNil(images))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	values := xpc.Dictionary{"imageDescriptions": data, "filePath": abs}
	if err := setPlatform(values, platform); err != nil {
		return err
	}
	return c.images.send(ctx, "imageSave", values, 0)
}

// ImageLoadResult is the result of LoadImages.
type ImageLoadResult struct {
	// Images are the images that the archive added to the local image
	// store.
	Images []ImageDescription
	// RejectedMembers are the archive members that the server did not
	// extract, because of an invalid path or a symbolic link that leaves
	// the archive.
	RejectedMembers []string
}

// LoadImages adds the images in an OCI archive at path to the local image
// store. A relative path is relative to the current directory. With force,
// the server loads the archive even if some members are rejected.
func (c *Client) LoadImages(ctx context.Context, path string, force bool) (_ *ImageLoadResult, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	reply, err := c.images.requestTimeout(ctx, "imageLoad", xpc.Dictionary{"filePath": abs, "forceLoad": force}, 0)
	if err != nil {
		return nil, err
	}
	defer closeJoin(&err, reply)
	var out ImageLoadResult
	if err := decodeReply(reply, "imageLoad", "imageDescriptions", &out.Images); err != nil {
		return nil, err
	}
	if err := decodeReply(reply, "imageLoad", "rejectedMembers", &out.RejectedMembers); err != nil {
		return nil, err
	}
	return &out, nil
}

// CleanUpOrphanedBlobs removes the content that no image uses. It returns
// the digests of the removed blobs and the space freed, in bytes.
func (c *Client) CleanUpOrphanedBlobs(ctx context.Context) (_ []string, _ uint64, err error) {
	reply, err := c.images.requestTimeout(ctx, "imageCleanupOrphanedBlobs", nil, 0)
	if err != nil {
		return nil, 0, err
	}
	defer closeJoin(&err, reply)
	var digests []string
	if err := decodeReply(reply, "imageCleanupOrphanedBlobs", "digests", &digests); err != nil {
		return nil, 0, err
	}
	return digests, reply.Uint64("imageSize"), nil
}

// ImageDiskUsage returns the disk usage of the local images. An image is
// active if its reference is in activeReferences, which are usually the
// image references of the existing containers. Reclaimable is the size of
// the images that are not active.
func (c *Client) ImageDiskUsage(ctx context.Context, activeReferences []string) (_ *ResourceUsage, err error) {
	data, err := encodeJSON(emptyIfNil(activeReferences))
	if err != nil {
		return nil, err
	}
	reply, err := c.images.requestTimeout(ctx, "imageDiskUsage", xpc.Dictionary{"activeImageReferences": data}, 0)
	if err != nil {
		return nil, err
	}
	defer closeJoin(&err, reply)
	return &ResourceUsage{
		Total:       int(reply.Int64("totalCount")),
		Active:      int(reply.Int64("activeCount")),
		SizeInBytes: reply.Uint64("imageSize"),
		Reclaimable: reply.Uint64("reclaimableSize"),
	}, nil
}

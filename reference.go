package container

import (
	"fmt"
	"regexp"
	"strings"
)

// This file parses and normalizes image references with the same rules as
// the container CLI.

// DefaultRegistryDomain is the registry that short image names refer to. It
// is the default of the registry.domain setting of the container system.
const DefaultRegistryDomain = "docker.io"

const (
	referenceNameMax  = 255
	referenceTagMax   = 128
	referenceTotalMax = referenceNameMax + 1 + referenceTagMax

	dockerRegistryHost       = "registry-1.docker.io"
	legacyDockerRegistryHost = "docker.io"
	defaultDockerRepo        = "library"
	defaultTag               = "latest"
)

var (
	identifierRegexp = regexp.MustCompile(`^[a-f0-9]{64}$`)
	domainRegexp     = func() *regexp.Regexp {
		component := `(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])`
		name := component + `(?:\.` + component + `)*`
		host := `(?:` + name + `|\[(?:[a-fA-F0-9:]+)\])`
		return regexp.MustCompile(`^` + host + `(?::[0-9]+)?$`)
	}()
	pathTagRegexp = regexp.MustCompile(`^(?P<path>(?:[a-z0-9]+(?:[._]|__|-|/)?)*[a-z0-9]+)` +
		`(?::(?P<tag>[\w][\w.-]{0,127}))?(?:@(?P<digest>sha256:[0-9a-fA-F]{64}))?$`)
)

// Reference is a parsed image reference, such as
// "docker.io/library/debian:trixie-slim".
type Reference struct {
	// Domain is the registry host, with an optional port. It is empty
	// when the reference has no domain.
	Domain string
	Path   string
	// Tag and Digest are empty when absent. When a reference has both,
	// ParseReference keeps only the digest.
	Tag    string
	Digest string
}

// ParseReference parses an image reference.
func ParseReference(s string) (Reference, error) {
	if len(s) > referenceTotalMax {
		return Reference{}, invalidArgument("reference length %d greater than %d", len(s), referenceTotalMax)
	}
	if identifierRegexp.MatchString(s) {
		return Reference{}, invalidArgument("cannot specify 64 byte hex string as reference")
	}
	domain, rest := splitReferenceDomain(s)
	if domain != "" && !domainRegexp.MatchString(domain) {
		return Reference{}, invalidArgument("invalid domain %s for reference %s", domain, s)
	}
	m := pathTagRegexp.FindStringSubmatch(rest)
	if m == nil {
		return Reference{}, invalidArgument("invalid format for image reference")
	}
	r := Reference{Domain: domain, Path: m[pathTagRegexp.SubexpIndex("path")]}
	if len(r.Name()) > referenceNameMax {
		return Reference{}, invalidArgument("repo length %d greater than %d", len(r.Name()), referenceNameMax)
	}
	if d := m[pathTagRegexp.SubexpIndex("digest")]; d != "" {
		r.Digest = d
	} else {
		r.Tag = m[pathTagRegexp.SubexpIndex("tag")]
	}
	return r, nil
}

// splitReferenceDomain splits off the first path component if it looks like
// a host: it contains "." or ":", or starts with "localhost".
func splitReferenceDomain(s string) (domain, rest string) {
	first, path, ok := strings.Cut(s, "/")
	if !ok {
		if strings.HasPrefix(s, "localhost") {
			return s, ""
		}
		return "", s
	}
	if strings.HasPrefix(first, "localhost") || strings.ContainsAny(first, ".:") {
		return first, path
	}
	return "", s
}

// Name returns the domain and path.
func (r Reference) Name() string {
	if r.Domain != "" {
		return r.Domain + "/" + r.Path
	}
	return r.Path
}

// String returns the reference in its text form.
func (r Reference) String() string {
	switch {
	case r.Tag != "":
		return r.Name() + ":" + r.Tag
	case r.Digest != "":
		return r.Name() + "@" + r.Digest
	}
	return r.Name()
}

// Normalize adds "library/" to a one-component Docker Hub path and adds the
// tag "latest" when the reference has no tag and no digest.
func (r *Reference) Normalize() {
	if (r.Domain == dockerRegistryHost || r.Domain == legacyDockerRegistryHost) && !strings.Contains(r.Path, "/") {
		r.Path = defaultDockerRepo + "/" + r.Path
	}
	if r.Tag == "" && r.Digest == "" {
		r.Tag = defaultTag
	}
}

// NormalizeReference returns the full form of an image reference, as the
// container CLI stores it. A reference without a domain gets registryDomain,
// or DefaultRegistryDomain if registryDomain is empty. For example,
// "debian:trixie-slim" becomes "docker.io/library/debian:trixie-slim".
func NormalizeReference(reference, registryDomain string) (string, error) {
	if registryDomain == "" {
		registryDomain = DefaultRegistryDomain
	}
	r, err := ParseReference(reference)
	if err != nil {
		return "", err
	}
	if r.Domain == "" {
		if r, err = ParseReference(registryDomain + "/" + reference); err != nil {
			return "", err
		}
	}
	r.Normalize()
	return r.String(), nil
}

func invalidArgument(format string, args ...any) error {
	return &Error{Code: CodeInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

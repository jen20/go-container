package container

import (
	"errors"
	"strings"
	"testing"
)

func TestDenormalizeReference(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	tests := []struct{ in, domain, want string }{
		{"docker.io/library/debian:trixie-slim", "", "debian:trixie-slim"},
		{"docker.io/library/debian:latest", "", "debian:latest"},
		{"docker.io/apple/container:1.0", "", "apple/container:1.0"},
		{"docker.io/library/alpine@" + digest, "", "alpine@" + digest},
		{"docker.io/library/debian", "", "debian"},
		{"ghcr.io/apple/containerization/vminit:0.54.0", "", "ghcr.io/apple/containerization/vminit:0.54.0"},
		{"registry-1.docker.io/library/alpine:3", "", "registry-1.docker.io/library/alpine:3"},
		{"registry-1.docker.io/library/alpine:3", "registry-1.docker.io", "alpine:3"},
		{"registry.example.com/library/app:1", "registry.example.com", "library/app:1"},
		{"debian:trixie-slim", "", "debian:trixie-slim"},
	}
	for _, tt := range tests {
		got, err := DenormalizeReference(tt.in, tt.domain)
		if err != nil || got != tt.want {
			t.Errorf("DenormalizeReference(%q, %q) = %q, %v; want %q", tt.in, tt.domain, got, err, tt.want)
		}
		if tt.domain != "" || !strings.HasPrefix(tt.in, DefaultRegistryDomain+"/") {
			continue
		}
		// A short form must normalize back to the input, after the default
		// tag.
		back, err := NormalizeReference(got, tt.domain)
		want := tt.in
		if !strings.ContainsAny(strings.TrimPrefix(tt.in, DefaultRegistryDomain), ":@") {
			want += ":latest"
		}
		if err != nil || back != want {
			t.Errorf("NormalizeReference(%q) = %q, %v; want %q", got, back, err, want)
		}
	}
	if _, err := DenormalizeReference("UPPER", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("DenormalizeReference(UPPER) gave %v, want ErrInvalidArgument", err)
	}
}

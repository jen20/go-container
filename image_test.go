package container

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeReference(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	tests := []struct{ in, domain, want string }{
		{"debian", "", "docker.io/library/debian:latest"},
		{"debian:trixie-slim", "", "docker.io/library/debian:trixie-slim"},
		{"library/debian", "", "docker.io/library/debian:latest"},
		{"apple/container", "", "docker.io/apple/container:latest"},
		{"ghcr.io/apple/containerization/vminit:0.54.0", "", "ghcr.io/apple/containerization/vminit:0.54.0"},
		{"registry-1.docker.io/alpine", "", "registry-1.docker.io/library/alpine:latest"},
		{"localhost:5000/app", "", "localhost:5000/app:latest"},
		{"alpine@" + digest, "", "docker.io/library/alpine@" + digest},
		{"alpine:3@" + digest, "", "docker.io/library/alpine@" + digest},
		{"app", "registry.example.com", "registry.example.com/app:latest"},
	}
	for _, tt := range tests {
		got, err := NormalizeReference(tt.in, tt.domain)
		if err != nil || got != tt.want {
			t.Errorf("NormalizeReference(%q, %q) = %q, %v; want %q", tt.in, tt.domain, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "UPPER", strings.Repeat("ab", 32), "a//b", "bad_domain.com:x/app"} {
		if _, err := NormalizeReference(bad, ""); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("NormalizeReference(%q) gave %v, want ErrInvalidArgument", bad, err)
		}
	}
}

package container

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jen20/go-container/internal/xpc"
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

func TestPlatformEqual(t *testing.T) {
	arm := Platform{OS: "linux", Architecture: "arm64"}
	tests := []struct {
		a, b Platform
		want bool
	}{
		{arm, Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}, true},
		{arm, Platform{OS: "linux", Architecture: "aarch64"}, true},
		{arm, Platform{OS: "linux", Architecture: "amd64"}, false},
		{arm, Platform{OS: "darwin", Architecture: "arm64"}, false},
		{Platform{OS: "linux", Architecture: "arm", Variant: "v7"}, Platform{OS: "linux", Architecture: "arm", Variant: "v6"}, false},
	}
	for _, tt := range tests {
		if got := tt.a.Equal(tt.b); got != tt.want {
			t.Errorf("%v.Equal(%v) = %v", tt.a, tt.b, got)
		}
	}
	if s := LinuxArm64Platform.String(); s != "linux/arm64" {
		t.Errorf("String() = %q", s)
	}
}

func TestProcessFromImage(t *testing.T) {
	ic := &ImageConfig{
		Entrypoint: []string{"/entry.sh"},
		Cmd:        []string{"serve", "--port", "80"},
		Env:        []string{"PATH=/usr/bin", "MODE=prod", "BARE"},
		WorkingDir: "/app",
		User:       "www-data",
	}
	p, err := processFromImage(ic, &ImageContainerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Executable != "/entry.sh" || !reflect.DeepEqual(p.Arguments, []string{"serve", "--port", "80"}) {
		t.Errorf("command %s %v", p.Executable, p.Arguments)
	}
	if p.WorkingDirectory != "/app" || p.User != (User{Name: "www-data"}) {
		t.Errorf("cwd %q user %v", p.WorkingDirectory, p.User)
	}
	if !reflect.DeepEqual(p.Environment, []string{"PATH=/usr/bin", "MODE=prod"}) {
		t.Errorf("env %v", p.Environment)
	}

	t.Setenv("GO_CONTAINER_HOST_VAR", "from-host")
	p, _ = processFromImage(ic, &ImageContainerOptions{
		Arguments: []string{"check"},
		Env:       []string{"MODE=dev", "GO_CONTAINER_HOST_VAR", "GO_CONTAINER_UNSET_VAR"},
		User:      "1000:1000",
	})
	if p.Executable != "/entry.sh" || !reflect.DeepEqual(p.Arguments, []string{"check"}) {
		t.Errorf("arguments override: %s %v", p.Executable, p.Arguments)
	}
	if !reflect.DeepEqual(p.Environment, []string{"PATH=/usr/bin", "MODE=dev", "GO_CONTAINER_HOST_VAR=from-host"}) {
		t.Errorf("merged env %v", p.Environment)
	}
	if p.User != (User{Name: "1000:1000"}) {
		t.Errorf("user %v", p.User)
	}

	p, _ = processFromImage(ic, &ImageContainerOptions{Entrypoint: "/bin/sh"})
	if p.Executable != "/bin/sh" || len(p.Arguments) != 0 {
		t.Errorf("entrypoint override must drop Cmd: %s %v", p.Executable, p.Arguments)
	}

	p, _ = processFromImage(&ImageConfig{Entrypoint: []string{""}, Cmd: []string{"bash"}}, &ImageContainerOptions{})
	if p.Executable != "bash" {
		t.Errorf("empty entrypoint must be cleared: %s", p.Executable)
	}
	if p.WorkingDirectory != "/" || p.User != (User{}) {
		t.Errorf("defaults: cwd %q user %v", p.WorkingDirectory, p.User)
	}

	if _, err := processFromImage(&ImageConfig{}, &ImageContainerOptions{}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("no command gave %v", err)
	}
}

func TestParseProgress(t *testing.T) {
	events := parseProgress(xpc.Dictionary{
		"progressUpdateSetDescription": "Fetching image",
		"progressUpdateSetItemsName":   "blobs",
		"progressUpdateAddTotalSize":   int64(1000),
		"progressUpdateAddSize":        int64(250),
		"progressUpdateAddItems":       int64(0), // zero values are skipped
	})
	want := []ProgressEvent{
		{Kind: ProgressSetDescription, Text: "Fetching image"},
		{Kind: ProgressSetItemsName, Text: "blobs"},
		{Kind: ProgressAddSize, Value: 250},
		{Kind: ProgressAddTotalSize, Value: 1000},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events %+v", events)
	}
	var p Progress
	p.Apply(events)
	p.Apply([]ProgressEvent{{Kind: ProgressAddSize, Value: 250}, {Kind: ProgressSetTotalTasks, Value: 3}})
	if p.Description != "Fetching image" || p.Size != 500 || p.TotalSize != 1000 || p.TotalTasks != 3 {
		t.Fatalf("progress %+v", p)
	}
}

func TestSearchImages(t *testing.T) {
	all := []ImageDescription{
		{Reference: "docker.io/library/debian:trixie-slim"},
		{Reference: "docker.io/library/app:latest", Descriptor: Descriptor{Annotations: map[string]string{AnnotationImageName: "app:latest"}}},
		{Reference: "ghcr.io/example/tool:1"},
	}
	tests := []struct{ ref, want string }{
		{"debian:trixie-slim", "docker.io/library/debian:trixie-slim"},
		{"docker.io/library/debian:trixie-slim", "docker.io/library/debian:trixie-slim"},
		{"app", "docker.io/library/app:latest"},
		{"ghcr.io/example/tool:1", "ghcr.io/example/tool:1"},
		{"debian", ""},
	}
	for _, tt := range tests {
		img, err := searchImages(all, tt.ref, new(DefaultSystemConfig()))
		got := ""
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			t.Fatalf("%s: %v", tt.ref, err)
		default:
			got = img.Reference
		}
		if got != tt.want {
			t.Errorf("search %q found %q, want %q", tt.ref, got, tt.want)
		}
	}
}

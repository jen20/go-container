package systemconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeFile writes a config file in a new directory and returns its path.
func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadLayers(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	user := writeFile(t, "[container]\ncpus = 8\n[plugin.example]\ncustom = true\n")
	system := writeFile(t, "[container]\ncpus = 2\n[container.dns]\nnameservers = [\"8.8.8.8\"]\n")

	layers, err := Load(t.Context(), missing, user, system)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{
			"container": map[string]any{"cpus": int64(8)},
			"plugin":    map[string]any{"example": map[string]any{"custom": true}},
		},
		{"container": map[string]any{
			"cpus": int64(2),
			"dns":  map[string]any{"nameservers": []any{"8.8.8.8"}},
		}},
	}
	if !reflect.DeepEqual(layers, want) {
		t.Errorf("Load = %#v, want %#v", layers, want)
	}
}

func TestLoadErrors(t *testing.T) {
	malformed := writeFile(t, "[container\ncpus = 4")
	for _, path := range []string{malformed, t.TempDir()} {
		_, err := Load(t.Context(), path)
		if err == nil {
			t.Errorf("Load(%q) succeeded", path)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Load(ctx, malformed); !errors.Is(err, context.Canceled) {
		t.Errorf("Load with canceled context = %v", err)
	}
}

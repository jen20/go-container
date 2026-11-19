// Package systemconfig reads the TOML configuration files of Apple container.
package systemconfig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/BurntSushi/toml"
)

// Load reads the given files, highest precedence first, and returns one
// layer per existing file. It skips missing files and preserves all keys
// and their TOML types.
func Load(ctx context.Context, files ...string) ([]map[string]any, error) {
	var layers []map[string]any
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var layer map[string]any
		_, err := toml.DecodeFile(path, &layer)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to load configuration from %q: %w", path, err)
		}
		layers = append(layers, layer)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return layers, nil
}

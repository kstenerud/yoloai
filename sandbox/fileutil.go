package sandbox

import (
	"encoding/json"
	"os"

	"github.com/kstenerud/yoloai/config"
)

// ExpandPath composes tilde expansion with braced env var expansion.
func ExpandPath(p string) (string, error) { return config.ExpandPath(p) }

// ExpandTilde replaces a leading ~ with the user's home directory.
func ExpandTilde(p string) string { return config.ExpandTilde(p) }

// readJSONMap reads a JSON file into a map, returning an empty map if the file doesn't exist.
func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeJSONMap marshals a map and writes it as indented JSON to the given path.
func writeJSONMap(path string, m map[string]any) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

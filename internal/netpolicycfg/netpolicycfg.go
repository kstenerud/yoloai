// ABOUTME: Per-sandbox network-policy record (netpolicy.json), netpolicy's own
// ABOUTME: persistence domain — kept out of the substrate record store.Environment (D90).
package netpolicycfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// NetpolicyFile is the filename for the per-sandbox network-policy record.
const NetpolicyFile = "netpolicy.json"

const schemaVersion = 1

// Netpolicy holds a sandbox's resolved network policy: the network mode and the
// composed egress allowlist. D90 gives netpolicy its own persisted record rather
// than smuggling these into the substrate record (store.Environment); future
// fields (e.g. provenance) are added here additively. Mode/Allow use the same
// JSON keys they had in environment.json so the relocation is a pure move.
type Netpolicy struct {
	Version int      `json:"version"`
	Mode    string   `json:"network_mode,omitempty"`
	Allow   []string `json:"network_allow,omitempty"`
}

// Save writes netpolicy.json to the given sandbox directory.
func Save(sandboxDir string, np *Netpolicy) error {
	np.Version = schemaVersion

	data, err := json.MarshalIndent(np, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", NetpolicyFile, err)
	}

	path := filepath.Join(sandboxDir, NetpolicyFile)
	if err := fileutil.AtomicWriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", NetpolicyFile, err)
	}

	return nil
}

// Load reads netpolicy.json from the given sandbox directory. Returns a
// zero-value Netpolicy if the file does not exist (a sandbox with default,
// non-isolated networking writes no record — omitempty drops empty fields).
func Load(sandboxDir string) (*Netpolicy, error) {
	path := filepath.Join(sandboxDir, NetpolicyFile)

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir
	if err != nil {
		if os.IsNotExist(err) {
			return &Netpolicy{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", NetpolicyFile, err)
	}

	var np Netpolicy
	if err := json.Unmarshal(data, &np); err != nil {
		return nil, fmt.Errorf("parse %s: %w", NetpolicyFile, err)
	}
	return &np, nil
}

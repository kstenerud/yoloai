// ABOUTME: Helpers for patching runtime-config.json in-place. A single shared
// ABOUTME: read-unmarshal-mutate-marshal-write helper, with thin wrappers for
// ABOUTME: the three fields that need patching (vscode tunnel, debug, domains).
package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/orchestrator/invocation"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/store"
)

// loadContainerConfig reads and parses runtime-config.json from a sandbox dir.
// Callers that also need the raw bytes (e.g. to store ConfigJSON verbatim) read
// the file directly instead.
func loadContainerConfig(sandboxDir string) (runtimeconfig.ContainerConfig, error) {
	var cfg runtimeconfig.ContainerConfig
	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return cfg, fmt.Errorf("read runtime-config.json: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse runtime-config.json: %w", err)
	}
	return cfg, nil
}

func patchRuntimeConfig(sandboxDir string, mutate func(*runtimeconfig.ContainerConfig)) error {
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}
	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}
	mutate(&cfg)
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json: %w", err)
	}
	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json: %w", err)
	}
	return nil
}

// patchConfigVscodeTunnel reads runtime-config.json, enables the vscode_tunnel
// fields, and writes it back. Called when --vscode-tunnel is added to an
// existing sandbox via start/restart.
func patchConfigVscodeTunnel(sandboxDir, sandboxName string) error {
	return patchRuntimeConfig(sandboxDir, func(cfg *runtimeconfig.ContainerConfig) {
		cfg.VscodeTunnel = true
		cfg.VscodeTunnelName = invocation.SanitizeTunnelName(sandboxName)
	})
}

// patchConfigDebug reads runtime-config.json, sets the debug field, and writes it back.
func patchConfigDebug(sandboxDir string, debug bool) error {
	return patchRuntimeConfig(sandboxDir, func(cfg *runtimeconfig.ContainerConfig) { cfg.Debug = debug })
}

// PatchConfigAllowedDomains reads runtime-config.json, updates the allowed_domains
// field, and writes it back. Used by network-allow to persist domain changes.
func PatchConfigAllowedDomains(sandboxDir string, domains []string) error {
	return patchRuntimeConfig(sandboxDir, func(cfg *runtimeconfig.ContainerConfig) { cfg.AllowedDomains = domains })
}

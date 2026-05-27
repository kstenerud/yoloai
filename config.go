// ABOUTME: SystemClient.Config() sub-handle: config get/set/reset/effective as
// ABOUTME: library orchestration; CLI consumes the typed results.

package yoloai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// ErrConfigKeyNotFound is returned by ConfigAdmin.Get when the
// requested key isn't present in any config layer (global or
// profile defaults). Use errors.Is to detect.
var ErrConfigKeyNotFound = errors.New("config key not found")

// ConfigAdmin is the SystemClient sub-handle for global and
// profile-default configuration values (`yoloai config get/set/reset`).
//
// Two storage layers, routed by config.IsGlobalKey:
//   - global keys (tmux_conf, model_aliases) → ~/.yoloai/config.yaml
//   - everything else → ~/.yoloai/defaults/config.yaml
//
// Set and Reset hide this routing from callers; embedders pass a
// dotted key and the handle picks the right file.
type ConfigAdmin struct {
	s *SystemClient
}

// Config returns the configuration-management sub-handle.
//
// Q-W resolution (Shape B, sub-handles): config get/set/reset
// cluster under one accessor, matching Profiles().
func (s *SystemClient) Config() *ConfigAdmin {
	return &ConfigAdmin{s: s}
}

// Effective returns the merged effective configuration as formatted
// YAML text (baked-in defaults overlaid with the user's
// ~/.yoloai/config.yaml and ~/.yoloai/defaults/config.yaml). This is
// what `yoloai config get` (no key) prints.
//
// The return is YAML rather than map[string]any because the formatted
// text carries section ordering and comments that the user-facing
// output relies on. Callers that want structured data can yaml-parse
// the result themselves.
func (a *ConfigAdmin) Effective(_ context.Context) (string, error) {
	return config.GetEffectiveConfig(a.s.layout)
}

// Get returns a single configuration value by dotted key (e.g.
// "backend", "tart.image", "env.MY_VAR"). Returns
// ErrConfigKeyNotFound when the key isn't present in any layer.
//
// The "found" signal is surfaced as a typed error rather than a
// (value, found, err) tuple so embedder code looks the same as for
// other "missing thing" cases (errors.Is(err, ErrConfigKeyNotFound)).
func (a *ConfigAdmin) Get(_ context.Context, key string) (string, error) {
	value, found, err := config.GetConfigValue(a.s.layout, key)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: %s", ErrConfigKeyNotFound, key)
	}
	return value, nil
}

// Set writes a configuration value. The dotted key picks the storage
// layer (global vs profile defaults); the target file is created
// with a sensible scaffold if it doesn't yet exist.
func (a *ConfigAdmin) Set(_ context.Context, key, value string) error {
	if config.IsGlobalKey(key) {
		if err := a.ensureGlobalConfig(); err != nil {
			return err
		}
		return config.UpdateGlobalConfigFields(a.s.layout, map[string]string{key: value})
	}
	if err := a.ensureProfileConfig(); err != nil {
		return err
	}
	return config.UpdateConfigFields(a.s.layout, map[string]string{key: value})
}

// Reset deletes a key from configuration, reverting it to the
// baked-in default. Works at any level — a scalar, a single map
// entry, or an entire section.
//
// Resetting a key that isn't set is not an error; the operation is
// idempotent.
func (a *ConfigAdmin) Reset(_ context.Context, key string) error {
	if config.IsGlobalKey(key) {
		return config.DeleteGlobalConfigField(a.s.layout, key)
	}
	return config.DeleteConfigField(a.s.layout, key)
}

// ensureGlobalConfig creates ~/.yoloai/config.yaml with an empty
// object if it doesn't yet exist. The empty `{}` scaffold lets
// subsequent UpdateGlobalConfigFields parse the file without dealing
// with the "file missing" branch.
func (a *ConfigAdmin) ensureGlobalConfig() error {
	configPath := a.s.layout.GlobalConfigPath()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		return nil
	}
	if err := fileutil.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	return fileutil.WriteFile(configPath, []byte("{}\n"), 0600)
}

// ensureProfileConfig creates ~/.yoloai/defaults/config.yaml with the
// commented scaffold if it doesn't yet exist.
func (a *ConfigAdmin) ensureProfileConfig() error {
	configPath := a.s.layout.DefaultsConfigPath()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		return nil
	}
	if err := fileutil.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	scaffold := config.GenerateScaffoldConfig(config.DefaultConfigYAML)
	return fileutil.WriteFile(configPath, []byte(scaffold), 0600)
}

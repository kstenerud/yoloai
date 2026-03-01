package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/runtime"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const defaultConfigYAML = `# yoloai base profile configuration
# See https://github.com/kstenerud/yoloai for documentation
# Run 'yoloai config set <key> <value>' to change settings.
#
# Available settings:
#   agent        Agent to use: aider, claude, codex, gemini, opencode
#   model        Model name or alias passed to the agent
#   backend      Runtime backend: docker, tart, seatbelt
#   tart.image   Custom base VM image (tart backend only)
#   env.<NAME>   Environment variable passed to container

{}
`

const defaultGlobalConfigYAML = `# yoloai global configuration
# These settings apply to all sandboxes regardless of profile.
# Run 'yoloai config set <key> <value>' to change settings.
#
# Available settings:
#   tmux_conf                Tmux configuration: default, default+host
#   model_aliases.<alias>    Custom model alias (overrides agent built-in aliases)

{}
`

// Manager is the central orchestrator for sandbox operations.
type Manager struct {
	runtime runtime.Runtime
	backend string
	logger  *slog.Logger
	input   io.Reader
	scanner *bufio.Scanner // shared scanner for multi-step interactive prompts
	output  io.Writer
}

// NewManager creates a Manager with the given runtime, backend name, logger,
// input reader for interactive prompts, and output writer for user-facing messages.
func NewManager(rt runtime.Runtime, backend string, logger *slog.Logger, input io.Reader, output io.Writer) *Manager {
	return &Manager{
		runtime: rt,
		backend: backend,
		logger:  logger,
		input:   input,
		scanner: bufio.NewScanner(input),
		output:  output,
	}
}

// readLine reads a single line from the shared scanner, returning early if ctx
// is cancelled. On EOF, returns ("", nil) so callers can treat it as a default.
func (m *Manager) readLine(ctx context.Context) (string, error) {
	ch := make(chan string, 1)
	go func() {
		if m.scanner.Scan() {
			ch <- m.scanner.Text()
		} else {
			ch <- ""
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line := <-ch:
		return line, nil
	}
}

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation.
func (m *Manager) EnsureSetup(ctx context.Context) error {
	if err := m.EnsureSetupNonInteractive(ctx); err != nil {
		return err
	}

	// Run new-user experience if setup_complete is false
	state, err := LoadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if !state.SetupComplete {
		if !m.isInteractive() {
			// Non-TTY: auto-configure without prompts (power-user behavior)
			if err := m.setTmuxConf("default+host"); err != nil {
				return fmt.Errorf("set tmux_conf: %w", err)
			}
			if err := SaveState(&State{SetupComplete: true}); err != nil {
				return fmt.Errorf("save state: %w", err)
			}
		} else {
			if err := m.runNewUserSetup(ctx); err != nil {
				if !errors.Is(err, errSetupPreview) {
					return err
				}
			}
		}
	}

	return nil
}

// EnsureSetupNonInteractive performs the non-interactive portion of first-run
// setup: migration, directory creation, resource seeding, image building,
// and default config writing. Does not run interactive prompts.
func (m *Manager) EnsureSetupNonInteractive(ctx context.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	yoloaiDir := filepath.Join(homeDir, ".yoloai")

	// Migrate old layout before anything else
	if err := MigrateIfNeeded(yoloaiDir); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Create directory structure
	for _, sub := range []string{"sandboxes", "profiles", "cache"} {
		dir := filepath.Join(yoloaiDir, sub)
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	baseProfileDir := filepath.Join(yoloaiDir, "profiles", "base")
	if err := os.MkdirAll(baseProfileDir, 0750); err != nil {
		return fmt.Errorf("create %s: %w", baseProfileDir, err)
	}

	// Seed resources and build/rebuild base image as needed
	if err := m.runtime.EnsureImage(ctx, baseProfileDir, m.output, m.logger, false); err != nil {
		return err
	}

	// Write default config.yaml on first run
	configPath := filepath.Join(baseProfileDir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0600); err != nil {
			return fmt.Errorf("write config.yaml: %w", err)
		}
		fmt.Fprintln(m.output, "Tip: enable shell completions with 'yoloai completion --help'") //nolint:errcheck // best-effort output
	}

	// Write default global config.yaml if missing
	globalConfigPath := filepath.Join(yoloaiDir, "config.yaml")
	if _, err := os.Stat(globalConfigPath); os.IsNotExist(err) {
		if err := os.WriteFile(globalConfigPath, []byte(defaultGlobalConfigYAML), 0600); err != nil {
			return fmt.Errorf("write global config.yaml: %w", err)
		}
	}

	// Migration: move tmux_conf from base profile config to global config
	if err := migrateGlobalSettings(configPath, globalConfigPath); err != nil {
		return fmt.Errorf("migrate global settings: %w", err)
	}

	// Write default state.yaml if missing
	statePath := filepath.Join(yoloaiDir, "state.yaml")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		if err := SaveState(&State{}); err != nil {
			return fmt.Errorf("write state.yaml: %w", err)
		}
	}

	return nil
}

// migrateGlobalSettings moves tmux_conf and model_aliases from base profile
// config to global config if they exist in the profile but not in global.
func migrateGlobalSettings(profileConfigPath, globalConfigPath string) error {
	profileData, err := os.ReadFile(profileConfigPath) //nolint:gosec // G304: constructed path
	if err != nil {
		return nil // nothing to migrate
	}

	globalData, err := os.ReadFile(globalConfigPath) //nolint:gosec // G304: constructed path
	if err != nil {
		return nil // can't migrate without target
	}

	var profileDoc, globalDoc yaml.Node
	if err := yaml.Unmarshal(profileData, &profileDoc); err != nil {
		return nil
	}
	if err := yaml.Unmarshal(globalData, &globalDoc); err != nil {
		return nil
	}

	if profileDoc.Kind != yaml.DocumentNode || len(profileDoc.Content) == 0 {
		return nil
	}
	profileRoot := profileDoc.Content[0]
	if profileRoot.Kind != yaml.MappingNode {
		return nil
	}

	if globalDoc.Kind != yaml.DocumentNode || len(globalDoc.Content) == 0 {
		return nil
	}
	globalRoot := globalDoc.Content[0]
	if globalRoot.Kind != yaml.MappingNode {
		return nil
	}

	migrated := false
	for _, key := range []string{"tmux_conf", "model_aliases"} {
		profileVal := findYAMLValue(profileRoot, key)
		if profileVal == nil {
			continue
		}
		globalVal := findYAMLValue(globalRoot, key)
		if globalVal != nil {
			continue // already in global config
		}

		// Copy to global
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		globalRoot.Content = append(globalRoot.Content, keyNode, profileVal)

		// Remove from profile
		deleteYAMLField(profileRoot, key)
		migrated = true
	}

	if !migrated {
		return nil
	}

	// Write both files
	globalOut, err := yaml.Marshal(&globalDoc)
	if err != nil {
		return fmt.Errorf("marshal global config: %w", err)
	}
	if err := os.WriteFile(globalConfigPath, globalOut, 0600); err != nil {
		return fmt.Errorf("write global config: %w", err)
	}

	profileOut, err := yaml.Marshal(&profileDoc)
	if err != nil {
		return fmt.Errorf("marshal profile config: %w", err)
	}
	if err := os.WriteFile(profileConfigPath, profileOut, 0600); err != nil {
		return fmt.Errorf("write profile config: %w", err)
	}

	return nil
}

// findYAMLValue returns the value node for a top-level key in a mapping.
func findYAMLValue(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

// isInteractive returns true if m.input is a TTY (terminal).
func (m *Manager) isInteractive() bool {
	if f, ok := m.input.(*os.File); ok {
		return term.IsTerminal(int(f.Fd())) //nolint:gosec // file descriptor fits in int on all supported platforms
	}
	return false
}

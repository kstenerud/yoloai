package sandbox

// ABOUTME: Interactive first-run setup: tmux config, default backend, default agent.
// ABOUTME: Multi-step prompts with platform-aware backend detection.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

// errSetupPreview signals that the user chose [p] to preview the merged
// tmux config and the setup should exit cleanly without setting setup_complete.
var errSetupPreview = errors.New("setup preview requested")

// SetupOptions allows callers to pre-answer setup prompts via flags.
// When a field is non-empty, its corresponding interactive prompt is skipped.
type SetupOptions struct {
	Agent    string // skip agent prompt, use this value
	Backend  string // skip backend prompt, use this value
	TmuxConf string // skip tmux prompt, use this value
}

// validTmuxConf lists the accepted values for the --tmux-conf flag.
var validTmuxConf = []string{"default", "default+host", "host", "none"}

// detectedOS and detectedArch are variables so tests can override them.
var (
	detectedOS   = func() string { return runtime.GOOS }
	detectedArch = func() string { return runtime.GOARCH }
)

// tmuxConfigClass describes the user's existing tmux configuration.
type tmuxConfigClass int

const (
	tmuxConfigNone  tmuxConfigClass = iota // no ~/.tmux.conf
	tmuxConfigSmall                        // ≤10 significant lines
	tmuxConfigLarge                        // >10 significant lines
)

const significantLineThreshold = 10

// setupOption describes a numbered choice in a setup prompt.
type setupOption struct {
	name  string
	blurb string
}

// classifyTmuxConfig reads ~/.tmux.conf and returns its classification
// and content. Returns tmuxConfigNone with empty content if the file
// doesn't exist.
func classifyTmuxConfig() (tmuxConfigClass, string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return tmuxConfigNone, ""
	}

	data, err := os.ReadFile(filepath.Join(homeDir, ".tmux.conf")) //nolint:gosec // G304: standard config path
	if err != nil {
		return tmuxConfigNone, ""
	}

	content := string(data)
	count := countSignificantLines(content)

	if count > significantLineThreshold {
		return tmuxConfigLarge, content
	}
	return tmuxConfigSmall, content
}

// countSignificantLines counts non-blank, non-comment lines.
// Note: scanner.Err() is not checked because strings.Reader never returns
// an I/O error — Scan only returns false on EOF.
func countSignificantLines(content string) int {
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		count++
	}
	return count
}

// availableBackends returns the backends available on this platform.
func availableBackends() []setupOption {
	opts := []setupOption{
		{"docker", "Linux containers; portable, lightweight, fast"},
		{"podman", "Linux containers; daemonless, rootless by default"},
	}
	if detectedOS() == "darwin" {
		opts = append(opts, setupOption{
			"seatbelt", "macOS sandbox; near-instant, uses host tools, less isolation",
		})
		if detectedArch() == "arm64" {
			opts = append(opts, setupOption{
				"tart", "macOS VMs; native macOS env, strong isolation, heavier",
			})
		}
	}
	return opts
}

// availableAgents returns the non-test agents available.
func availableAgents() []setupOption {
	var opts []setupOption
	for _, name := range agent.AllAgentNames() {
		if name == "test" || name == "shell" {
			continue
		}
		def := agent.GetAgent(name)
		opts = append(opts, setupOption{name, def.Description})
	}
	return opts
}

// RunSetup runs the interactive setup unconditionally, regardless of
// setup_complete. Used by `yoloai system setup` to let users redo their choices
// or migrate from old layouts. Always creates defaults/ before proceeding,
// bypassing the CheckDefaultsDir migration gate (this command IS the migration).
func (m *Manager) RunSetup(ctx context.Context, opts SetupOptions) error {
	// Create defaults/ unconditionally so the migration check in
	// EnsureSetupNonInteractive doesn't block users with setup_complete=true
	// but no defaults/ yet (the exact scenario this command is meant to fix).
	if err := ensureDefaultsDir(); err != nil {
		return err
	}
	if err := m.EnsureSetupNonInteractive(ctx); err != nil {
		return err
	}
	if err := m.runNewUserSetup(ctx, opts); err != nil {
		if errors.Is(err, errSetupPreview) {
			return nil
		}
		return err
	}
	return nil
}

// runNewUserSetup orchestrates the interactive first-run setup prompts.
// Steps: tmux config → default backend → default agent → mark complete.
// Returns errSetupPreview if the user chose [p] in the tmux prompt.
func (m *Manager) runNewUserSetup(ctx context.Context, opts SetupOptions) error {
	// Step 1: Tmux config
	if opts.TmuxConf != "" {
		if err := validateTmuxConf(opts.TmuxConf); err != nil {
			return err
		}
		if err := m.setTmuxConf(opts.TmuxConf); err != nil {
			return err
		}
	} else {
		class, userConfig := classifyTmuxConfig()

		switch class {
		case tmuxConfigLarge:
			// Power user — skip prompt, auto-configure default+host
			if err := m.setTmuxConf("default+host"); err != nil {
				return err
			}

		case tmuxConfigNone:
			if err := m.promptTmuxSetup(ctx, "", true); err != nil {
				return err
			}

		case tmuxConfigSmall:
			if err := m.promptTmuxSetup(ctx, userConfig, false); err != nil {
				return err
			}
		}
	}

	// Step 2: Default backend (skip if only one option)
	if opts.Backend != "" {
		if err := m.setBackendFromFlag(opts.Backend); err != nil {
			return err
		}
	} else {
		if err := m.promptBackendSetup(ctx); err != nil {
			return err
		}
	}

	// Step 3: Default agent (skip if only one option)
	if opts.Agent != "" {
		if err := m.setAgentFromFlag(opts.Agent); err != nil {
			return err
		}
	} else {
		if err := m.promptAgentSetup(ctx); err != nil {
			return err
		}
	}

	return m.setSetupComplete()
}

// validateTmuxConf checks that the value is one of the accepted tmux_conf modes.
func validateTmuxConf(value string) error {
	for _, v := range validTmuxConf {
		if value == v {
			return nil
		}
	}
	return fmt.Errorf("invalid --tmux-conf value %q (valid: %s)", value, strings.Join(validTmuxConf, ", "))
}

// setBackendFromFlag validates the backend name against available backends and sets it.
func (m *Manager) setBackendFromFlag(name string) error {
	for _, b := range availableBackends() {
		if b.name == name {
			return config.UpdateConfigFields(map[string]string{
				"container_backend": name,
			})
		}
	}
	available := make([]string, 0, len(availableBackends()))
	for _, b := range availableBackends() {
		available = append(available, b.name)
	}
	return fmt.Errorf("invalid --backend value %q (available: %s)", name, strings.Join(available, ", "))
}

// setAgentFromFlag validates the agent name against available agents and sets it.
func (m *Manager) setAgentFromFlag(name string) error {
	for _, a := range availableAgents() {
		if a.name == name {
			return config.UpdateConfigFields(map[string]string{
				"agent": name,
			})
		}
	}
	available := make([]string, 0, len(availableAgents()))
	for _, a := range availableAgents() {
		available = append(available, a.name)
	}
	return fmt.Errorf("invalid --agent value %q (available: %s)", name, strings.Join(available, ", "))
}

// promptTmuxSetup shows the tmux config prompt and handles the user's choice.
// Sets tmux_conf but does NOT mark setup_complete (caller handles that).
func (m *Manager) promptTmuxSetup(ctx context.Context, userConfig string, noConfig bool) error {
	fmt.Fprintln(m.output) //nolint:errcheck // best-effort output
	if noConfig {
		fmt.Fprintln(m.output, "yoloai uses tmux in sandboxes. No ~/.tmux.conf found, so we'll")           //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintln(m.output, "yoloai uses tmux in sandboxes. Your tmux config is minimal, so we'll")     //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output)                                                                             //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "Your config (~/.tmux.conf):")                                              //nolint:errcheck // best-effort output
		for _, line := range strings.Split(strings.TrimRight(userConfig, "\n"), "\n") {
			fmt.Fprintf(m.output, "  %s\n", line) //nolint:errcheck // best-effort output
		}
	}

	fmt.Fprintln(m.output) //nolint:errcheck // best-effort output

	if noConfig {
		fmt.Fprintln(m.output, "  [Y] Use yoloai defaults")                                //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [n] Use raw tmux (no config)")                           //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [p] Print yoloai defaults and exit (for manual review)") //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintln(m.output, "  [Y] Use yoloai defaults + your config (yours overrides on conflict)") //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [n] Use only your config as-is")                                      //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [p] Print merged config and exit (for manual review)")                //nolint:errcheck // best-effort output
	}

	fmt.Fprint(m.output, "\nChoice [Y/n/p]: ") //nolint:errcheck // best-effort output

	line, err := m.readLine(ctx)
	if err != nil {
		return err
	}
	answer := strings.TrimSpace(strings.ToLower(line))

	switch answer {
	case "", "y", "yes":
		if noConfig {
			return m.setTmuxConf("default")
		}
		return m.setTmuxConf("default+host")

	case "n", "no":
		if noConfig {
			return m.setTmuxConf("none")
		}
		return m.setTmuxConf("host")

	case "p":
		fmt.Fprintln(m.output)                                    //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "--- yoloai defaults ---")         //nolint:errcheck // best-effort output
		fmt.Fprint(m.output, string(dockerrt.EmbeddedTmuxConf())) //nolint:errcheck // best-effort output
		if !noConfig && userConfig != "" {
			fmt.Fprintln(m.output)                        //nolint:errcheck // best-effort output
			fmt.Fprintln(m.output, "--- your config ---") //nolint:errcheck // best-effort output
			fmt.Fprint(m.output, userConfig)              //nolint:errcheck // best-effort output
		}
		fmt.Fprintln(m.output) //nolint:errcheck // best-effort output
		return errSetupPreview

	default:
		// Treat unknown input as Y (default)
		if noConfig {
			return m.setTmuxConf("default")
		}
		return m.setTmuxConf("default+host")
	}
}

// promptBackendSetup asks which runtime backend to use as default.
// Skipped when only one backend is available on the platform (e.g. Linux).
func (m *Manager) promptBackendSetup(ctx context.Context) error {
	backends := availableBackends()
	if len(backends) <= 1 {
		return nil
	}

	fmt.Fprintln(m.output)                             //nolint:errcheck // best-effort output
	fmt.Fprintln(m.output, "Default runtime backend:") //nolint:errcheck // best-effort output
	fmt.Fprintln(m.output)                             //nolint:errcheck // best-effort output

	for i, b := range backends {
		fmt.Fprintf(m.output, "  [%d] %-10s %s\n", i+1, b.name, b.blurb) //nolint:errcheck // best-effort output
	}

	fmt.Fprint(m.output, "\nChoice [1]: ") //nolint:errcheck // best-effort output

	line, err := m.readLine(ctx)
	if err != nil {
		return err
	}
	answer := strings.TrimSpace(line)

	idx := 0 // default to first option
	if answer != "" {
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= len(backends) {
			idx = n - 1
		}
	}

	return config.UpdateConfigFields(map[string]string{
		"container_backend": backends[idx].name,
	})
}

// promptAgentSetup asks which agent to use as default.
// Skipped when only one non-test agent is available.
func (m *Manager) promptAgentSetup(ctx context.Context) error {
	agents := availableAgents()
	if len(agents) <= 1 {
		return nil
	}

	// Default to claude if available, otherwise first option.
	idx := 0
	for i, a := range agents {
		if a.name == "claude" {
			idx = i
			break
		}
	}

	fmt.Fprintln(m.output)                   //nolint:errcheck // best-effort output
	fmt.Fprintln(m.output, "Default agent:") //nolint:errcheck // best-effort output
	fmt.Fprintln(m.output)                   //nolint:errcheck // best-effort output

	for i, a := range agents {
		fmt.Fprintf(m.output, "  [%d] %-10s %s\n", i+1, a.name, a.blurb) //nolint:errcheck // best-effort output
	}

	fmt.Fprintf(m.output, "\nChoice [%d]: ", idx+1) //nolint:errcheck // best-effort output

	line, err := m.readLine(ctx)
	if err != nil {
		return err
	}
	answer := strings.TrimSpace(line)

	if answer != "" {
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= len(agents) {
			idx = n - 1
		}
	}

	return config.UpdateConfigFields(map[string]string{
		"agent": agents[idx].name,
	})
}

// setTmuxConf writes the tmux_conf setting to the global config.yaml.
// When the mode includes "default" (i.e. the baked-in tmux config is active),
// also writes a copy of the embedded tmux.conf to defaults/tmux.conf so the
// user can inspect and customize it without rebuilding the image.
func (m *Manager) setTmuxConf(value string) error {
	if err := config.UpdateGlobalConfigFields(map[string]string{
		"tmux_conf": value,
	}); err != nil {
		return err
	}

	// Write defaults/tmux.conf when the baked-in config is in use.
	// Skip for "host" (user chose their own ~/.tmux.conf) and "none" (no config).
	if value == "default" || value == "default+host" {
		destPath := filepath.Join(config.DefaultsDir(), "tmux.conf")
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			if writeErr := os.WriteFile(destPath, dockerrt.EmbeddedTmuxConf(), 0600); writeErr != nil {
				return fmt.Errorf("write defaults/tmux.conf: %w", writeErr)
			}
		}
	}

	return nil
}

// setSetupComplete marks setup as done and prints the completion message.
func (m *Manager) setSetupComplete() error {
	if err := config.SaveState(&config.State{SetupComplete: true}); err != nil {
		return err
	}
	fmt.Fprintln(m.output, "\nSetup complete. To re-run setup at any time: yoloai system setup") //nolint:errcheck // best-effort output
	return nil
}

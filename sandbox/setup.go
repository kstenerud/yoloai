package sandbox

// ABOUTME: Interactive first-run setup: tmux config, default backend, default agent.
// ABOUTME: Multi-step prompts with platform-aware backend detection.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	tmuxres "github.com/kstenerud/yoloai/internal/resources/tmux"
	yoloairuntime "github.com/kstenerud/yoloai/runtime"
)

// SetupOptions carries every answer the first-run setup wizard would
// have collected from the user. Q-F: this is pure data — all fields
// are inputs to ApplySetup, which is non-interactive. The interactive
// wizard lives in the CLI layer (internal/cli/system_setup.go) and
// fills these in by prompting the user, then calls ApplySetup.
type SetupOptions struct {
	// TmuxConf is the tmux config mode. REQUIRED.
	// One of: "default", "default+host", "host", "none".
	TmuxConf string
	// Backend is the default backend name (e.g. "docker", "tart").
	// May be empty only when there's exactly one (or zero) available
	// backends on the platform — ApplySetup auto-picks in that case.
	Backend string
	// Agent is the default agent name (e.g. "claude"). May be empty
	// only when there's exactly one (or zero) available agents —
	// ApplySetup auto-picks in that case.
	Agent string
}

// TmuxConfigClass tells the wizard which prompt copy to use.
type TmuxConfigClass int

const (
	// TmuxConfigNone — no ~/.tmux.conf on disk.
	TmuxConfigNone TmuxConfigClass = iota
	// TmuxConfigSmall — ≤10 significant lines; the wizard asks the
	// user whether to merge yoloai defaults with their config.
	TmuxConfigSmall
	// TmuxConfigLarge — >10 significant lines; treated as
	// power-user, auto-configured to "default+host" without a prompt.
	TmuxConfigLarge
)

// SetupChoice is one option in a wizard prompt (backend or agent).
type SetupChoice struct {
	Name  string
	Blurb string
}

// SetupStatus is the host inspection a setup wizard needs to render
// its prompts: classification of the user's ~/.tmux.conf, the
// embedded yoloai defaults (so a "preview" option can print them),
// and the lists of backends/agents available on this platform.
type SetupStatus struct {
	TmuxClass         TmuxConfigClass
	UserTmuxConfig    string // contents of ~/.tmux.conf if present; empty otherwise
	DefaultTmuxConfig string // yoloai's embedded tmux.conf (for the wizard's [p] option)
	AvailableBackends []SetupChoice
	AvailableAgents   []SetupChoice
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
// homeDir is the user's home directory; callers derive it from filepath.Dir(layout.DataDir).
func classifyTmuxConfig(homeDir string) (tmuxConfigClass, string) {
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

// availableBackends returns the backends offered as the user's default in
// the first-run setup wizard. Iterates runtime.Descriptors() and filters by
// (a) Platforms ∋ host GOOS, (b) backend is a primary choice — containerd
// is excluded because users don't pick it directly; --isolation vm /
// vm-enhanced auto-routes to it. The Apple Silicon constraint for tart is
// applied here rather than via Platforms (which is GOOS-granular) so the
// option list stays empty on Intel Macs.
func availableBackends() []setupOption {
	hostOS := detectedOS()
	hostArch := detectedArch()
	var opts []setupOption
	for _, desc := range yoloairuntime.Descriptors() {
		if desc.Name == "containerd" {
			continue
		}
		if !slices.Contains(desc.Platforms, hostOS) {
			continue
		}
		if desc.Name == "tart" && hostArch != "arm64" {
			continue
		}
		opts = append(opts, setupOption{name: desc.Name, blurb: desc.Description})
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

// SetupStatus inspects the host and returns the data a wizard needs
// to render its prompts. Pure host inspection — does not touch the
// layout or config files. Safe to call from a Manager with a nil
// runtime (used by yoloai.SystemClient.SetupStatus).
func (m *Manager) SetupStatus() *SetupStatus {
	class, userConfig := classifyTmuxConfig(filepath.Dir(m.layout.DataDir))
	return &SetupStatus{
		TmuxClass:         TmuxConfigClass(class),
		UserTmuxConfig:    userConfig,
		DefaultTmuxConfig: string(tmuxres.Embedded()),
		AvailableBackends: toSetupChoices(availableBackends()),
		AvailableAgents:   toSetupChoices(availableAgents()),
	}
}

// ApplySetup writes the user's setup answers to the config files
// under DataDir. Non-interactive: every prompt is the caller's
// responsibility. Returns *UsageError when a required field is
// missing or invalid:
//
//   - opts.TmuxConf is required and must be one of: "default",
//     "default+host", "host", "none".
//   - opts.Backend is required when len(AvailableBackends) > 1;
//     auto-picks when exactly one is available; ignored when zero.
//   - opts.Agent follows the same rule.
//
// Always creates the defaults/ directory and writes setup_complete=true
// on success, so calling ApplySetup is the documented way to redo
// (or repair) setup state.
func (m *Manager) ApplySetup(_ context.Context, opts SetupOptions) error {
	if err := validateTmuxConf(opts.TmuxConf); err != nil {
		return NewUsageError("%v", err)
	}
	// Q-F: ApplySetup is config-only; image build is a separate
	// concern (handled by `yoloai system build` or by EnsureSetup on
	// first sandbox creation). This is why ApplySetup doesn't need a
	// runtime: SystemClient.Setup constructs a Manager with rt=nil.
	if err := m.ensureLayoutScaffold(); err != nil {
		return err
	}
	if err := m.setTmuxConf(opts.TmuxConf); err != nil {
		return err
	}
	if err := m.applyBackendChoice(opts.Backend); err != nil {
		return err
	}
	if err := m.applyAgentChoice(opts.Agent); err != nil {
		return err
	}
	return m.setSetupComplete()
}

// applyBackendChoice writes the default backend from the wizard's
// answer, validating user-supplied values and auto-picking when only
// one is available. Returns *UsageError for required-but-missing or
// unknown-backend.
func (m *Manager) applyBackendChoice(name string) error {
	backends := availableBackends()
	switch {
	case name != "":
		return m.setBackendFromFlag(name)
	case len(backends) == 1:
		return m.setBackendFromFlag(backends[0].name)
	case len(backends) > 1:
		return NewUsageError("Backend is required: multiple are available on this host (%s)", joinChoiceNames(backends))
	}
	return nil // zero backends — nothing to write
}

// applyAgentChoice writes the default agent from the wizard's answer,
// validating user-supplied values and auto-picking when only one is
// available. Returns *UsageError for required-but-missing or
// unknown-agent.
func (m *Manager) applyAgentChoice(name string) error {
	agents := availableAgents()
	switch {
	case name != "":
		return m.setAgentFromFlag(name)
	case len(agents) == 1:
		return m.setAgentFromFlag(agents[0].name)
	case len(agents) > 1:
		return NewUsageError("Agent is required: multiple are available on this host (%s)", joinChoiceNames(agents))
	}
	return nil
}

// toSetupChoices maps the package-internal setupOption slice to the
// exported SetupChoice shape.
func toSetupChoices(opts []setupOption) []SetupChoice {
	choices := make([]SetupChoice, len(opts))
	for i, o := range opts {
		choices[i] = SetupChoice{Name: o.name, Blurb: o.blurb}
	}
	return choices
}

// joinChoiceNames renders a list of options for an error message:
// "docker, podman, tart".
func joinChoiceNames(opts []setupOption) string {
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.name
	}
	return strings.Join(names, ", ")
}

// validateTmuxConf checks that the value is one of the accepted tmux_conf modes.
func validateTmuxConf(value string) error {
	if slices.Contains(validTmuxConf, value) {
		return nil
	}
	return fmt.Errorf("invalid --tmux-conf value %q (valid: %s)", value, strings.Join(validTmuxConf, ", "))
}

// setBackendFromFlag validates the backend name against available backends and sets it.
func (m *Manager) setBackendFromFlag(name string) error {
	for _, b := range availableBackends() {
		if b.name == name {
			return config.UpdateConfigFields(m.layout, map[string]string{
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
			return config.UpdateConfigFields(m.layout, map[string]string{
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

// setTmuxConf writes the tmux_conf setting to the global config.yaml.
// When the mode includes "default" (i.e. the baked-in tmux config is active),
// also writes a copy of the embedded tmux.conf to defaults/tmux.conf so the
// user can inspect and customize it without rebuilding the image.
func (m *Manager) setTmuxConf(value string) error {
	if err := config.UpdateGlobalConfigFields(m.layout, map[string]string{
		"tmux_conf": value,
	}); err != nil {
		return err
	}

	// Write defaults/tmux.conf when the baked-in config is in use.
	// Skip for "host" (user chose their own ~/.tmux.conf) and "none" (no config).
	if value == "default" || value == "default+host" {
		destPath := filepath.Join(m.layout.DefaultsDir(), "tmux.conf")
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			if writeErr := fileutil.WriteFile(destPath, tmuxres.Embedded(), 0644); writeErr != nil { //nolint:gosec // G306: tmux.conf contains no secrets; 0644 required so uid 1001 (yoloai in Kata VMs) can read it
				return fmt.Errorf("write defaults/tmux.conf: %w", writeErr)
			}
		}
	}

	return nil
}

// setSetupComplete marks setup as done and prints the completion message.
func (m *Manager) setSetupComplete() error {
	if err := config.SaveState(m.layout, &config.State{SetupComplete: true}); err != nil {
		return err
	}
	fmt.Fprintln(m.output, "\nSetup complete. To re-run setup at any time: yoloai system setup") //nolint:errcheck // best-effort output
	return nil
}

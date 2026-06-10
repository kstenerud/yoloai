// ABOUTME: `yoloai system setup` — interactive setup wizard. Inspects the host
// ABOUTME: (tmux config, available backends/agents), prompts the user, then
// ABOUTME: writes the three answers via the public yoloai.System.Config()
// ABOUTME: set verb. All host-inspection / prompting / auto-pick is CLI policy.
package system

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	tmuxres "github.com/kstenerud/yoloai/internal/resources/tmux"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

// validTmuxConf lists the accepted values for the --tmux-conf flag and the
// values the wizard writes for tmux_conf.
var validTmuxConf = []string{"default", "default+host", "host", "none"}

// significantLineThreshold separates a "minimal" ~/.tmux.conf (the wizard
// offers to merge yoloai defaults) from a "power-user" one (auto-configured to
// default+host without a prompt).
const significantLineThreshold = 10

// tmuxClass classifies the user's existing ~/.tmux.conf.
type tmuxClass int

const (
	tmuxNone  tmuxClass = iota // no ~/.tmux.conf on disk
	tmuxSmall                  // ≤ significantLineThreshold significant lines
	tmuxLarge                  // > significantLineThreshold significant lines
)

// setupChoice is one numbered option (a backend or an agent) in a wizard prompt.
type setupChoice struct {
	Name  string
	Blurb string
}

// runSystemSetup is `yoloai system setup`'s entry point. It inspects the host
// itself, resolves each of the three answers (from a flag, an auto-pick, or an
// interactive prompt), then persists them via System.Config().Set.
//
// Returns nil and writes nothing if the user chooses [p] at the tmux prompt
// (preview-then-exit, intentional).
func runSystemSetup(cmd *cobra.Command) error {
	sc, err := cliutil.System()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.ErrOrStderr()

	presets := presetsForHost(runtime.GOOS)
	agents := availableAgents(sc)

	tmuxConf, previewed, err := resolveTmuxConf(ctx, cmd, reader, out)
	if err != nil {
		return err
	}
	if previewed {
		// User chose [p] — they wanted to inspect, not commit.
		return nil
	}

	presetID, err := resolvePreset(ctx, cmd, reader, out, presets, sc)
	if err != nil {
		return err
	}

	agentName, err := resolveChoice(ctx, reader, out, "agent", cliutil.FlagStr(cmd, "agent"), agents, "Default agent:", defaultAgentIdx(agents))
	if err != nil {
		return err
	}

	if err := sc.Config().Set(ctx, "tmux_conf", tmuxConf); err != nil {
		return err
	}
	if presetID != "" {
		if p, ok := findPreset(presets, presetID); ok {
			if err := applyPreset(ctx, sc.Config(), p); err != nil {
				return err
			}
		}
	}
	if agentName != "" {
		if err := sc.Config().Set(ctx, "agent", agentName); err != nil {
			return err
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), "\nSetup complete. To re-run setup at any time: yoloai system setup") //nolint:errcheck
	return nil
}

// envPreset is one default-environment option in the setup wizard. On macOS a
// pick implies up to three config keys — guest os, isolation tier, and
// container_backend — so the technology implies the os and we never ask
// "mac or linux?" separately. Selecting a preset writes its non-empty keys and
// RESETS the rest of presetManagedKeys, so switching presets never leaves a
// stale os/isolation/container_backend behind (an empty Set is ignored by
// mergeStringField; Reset actually clears — AC2).
type envPreset struct {
	ID        string             // user-facing id and the --backend flag value
	OS        string             // "os" value; "" = reset
	Isolation string             // "isolation" value; "" = reset
	Backend   string             // "container_backend" value; "" = reset
	Blurb     string             // novice-friendly one-liner
	Probe     yoloai.BackendType // backend whose install-state tags this preset; "" = always shown installed
	Alias     bool               // ID is a container-system alias (availability = its socket exists)
}

// presetManagedKeys are the config keys a preset owns. Every preset sets some
// and resets the rest, so this is the single source of "what a preset controls".
var presetManagedKeys = []string{"os", "isolation", "container_backend"}

// presetsForHost returns the default-environment presets offered on hostOS, in
// recommended-first order (the first is the highlighted default). The macOS
// order mirrors the blank-config auto-pick precedence — apple, then the
// docker-VM container systems (orbstack, docker-desktop; see
// yoloai.ContainerSystems), then podman — followed by the macOS-guest presets
// (tart, seatbelt), which are wizard-only and never auto-selected as a default.
func presetsForHost(hostOS string) []envPreset {
	switch hostOS {
	case "darwin":
		return []envPreset{
			{ID: "apple", Blurb: "Fastest, strongest isolation, macOS-native", Probe: "apple"},
			{ID: "orbstack", Backend: "orbstack", Blurb: "Docker via OrbStack — fast, low overhead", Alias: true},
			{ID: "docker-desktop", Backend: "docker-desktop", Blurb: "Docker via Docker Desktop", Alias: true},
			{ID: "podman", Backend: "podman", Blurb: "Docker-compatible, daemonless, rootless", Probe: "podman"},
			{ID: "tart", OS: "mac", Isolation: "vm", Blurb: "Full macOS VM — Xcode/Swift, heavier", Probe: "tart"},
			{ID: "seatbelt", OS: "mac", Blurb: "Lightweight macOS sandbox — near-instant", Probe: "seatbelt"},
		}
	case "linux":
		return []envPreset{
			{ID: "docker", Blurb: "Linux containers — portable, fast", Probe: "docker"},
			{ID: "podman", Backend: "podman", Blurb: "Daemonless, rootless", Probe: "podman"},
			{ID: "vm", Isolation: "vm", Blurb: "Hardware-VM isolation (containerd + Kata)", Probe: "containerd"},
		}
	default: // windows and others: docker / podman via WSL
		return []envPreset{
			{ID: "docker", Blurb: "Linux containers via WSL", Probe: "docker"},
			{ID: "podman", Backend: "podman", Blurb: "Daemonless, rootless (WSL)", Probe: "podman"},
		}
	}
}

// presetConfigOps splits a preset into the keys to Set (its non-empty values)
// and the keys to Reset (the rest of presetManagedKeys). Pure, so the
// set-vs-reset write policy is testable without touching disk.
func presetConfigOps(p envPreset) (set map[string]string, reset []string) {
	set = map[string]string{}
	if p.OS != "" {
		set["os"] = p.OS
	}
	if p.Isolation != "" {
		set["isolation"] = p.Isolation
	}
	if p.Backend != "" {
		set["container_backend"] = p.Backend
	}
	for _, k := range presetManagedKeys {
		if _, ok := set[k]; !ok {
			reset = append(reset, k)
		}
	}
	return set, reset
}

// applyPreset writes the preset's owned keys: Set for the ones it uses, Reset
// for the ones it doesn't (so an earlier preset's os/isolation/container_backend
// can't linger — AC2).
func applyPreset(ctx context.Context, cfg *yoloai.ConfigAdmin, p envPreset) error {
	set, reset := presetConfigOps(p)
	for _, k := range presetManagedKeys {
		if v, ok := set[k]; ok {
			if err := cfg.Set(ctx, k, v); err != nil {
				return err
			}
		}
	}
	for _, k := range reset {
		if err := cfg.Reset(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// resolvePreset returns the chosen preset id. A --backend flag selects a preset
// by id (validated against the host's presets); otherwise the wizard prompts
// with availability tags, defaulting to the first (recommended) preset.
func resolvePreset(ctx context.Context, cmd *cobra.Command, reader *bufio.Reader, out io.Writer, presets []envPreset, sc *yoloai.System) (string, error) {
	if flag := cliutil.FlagStr(cmd, "backend"); flag != "" {
		if _, ok := findPreset(presets, flag); !ok {
			return "", fmt.Errorf("invalid --backend value %q (available: %s)", flag, joinPresetIDs(presets))
		}
		return flag, nil
	}
	return wizardChoice(ctx, reader, out, "Default environment:", buildPresetChoices(ctx, sc, presets), 0)
}

// buildPresetChoices renders each preset as a numbered choice, tagging the first
// "(recommended)" and any whose backing tool isn't installed "(not installed)".
// All presets are shown regardless of install state; a not-installed pick is
// saved and falls back gracefully at launch.
func buildPresetChoices(ctx context.Context, sc *yoloai.System, presets []envPreset) []setupChoice {
	home := cliutil.Layout().HomeDir
	choices := make([]setupChoice, len(presets))
	for i, p := range presets {
		blurb := p.Blurb
		if i == 0 {
			blurb += " (recommended)"
		}
		if !presetInstalled(ctx, sc, p, home) {
			blurb += " (not installed)"
		}
		choices[i] = setupChoice{Name: p.ID, Blurb: blurb}
	}
	return choices
}

// presetInstalled reports whether a preset's backing technology is present on
// the host: an alias preset by its daemon socket on disk, a backend preset by
// the cheaper "installed" tier (binary present, daemon need not be running).
func presetInstalled(ctx context.Context, sc *yoloai.System, p envPreset, home string) bool {
	if p.Alias {
		ok, _ := containerSystemAvailable(yoloai.ContainerSystemSocket(yoloai.BackendType(p.ID), home))
		return ok
	}
	if p.Probe == "" {
		return true
	}
	return sc.BackendInstalled(ctx, p.Probe)
}

// findPreset returns the preset with the given id.
func findPreset(presets []envPreset, id string) (envPreset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return envPreset{}, false
}

// joinPresetIDs renders the preset ids for an error message.
func joinPresetIDs(presets []envPreset) string {
	ids := make([]string, len(presets))
	for i, p := range presets {
		ids[i] = p.ID
	}
	return strings.Join(ids, ", ")
}

// availableAgents returns the user-selectable agents (RealOnly excludes the
// test/shell/idle pseudo-agents).
func availableAgents(sc *yoloai.System) []setupChoice {
	var opts []setupChoice
	for _, a := range sc.AgentTypes(yoloai.AgentQuery{RealOnly: true}) {
		opts = append(opts, setupChoice{Name: string(a.Type), Blurb: a.Description})
	}
	return opts
}

// resolveTmuxConf returns the tmux_conf answer. With --tmux-conf set it
// validates and returns it; otherwise it classifies ~/.tmux.conf and runs the
// interactive prompt. The bool is true when the user chose [p] (preview-only).
func resolveTmuxConf(ctx context.Context, cmd *cobra.Command, reader *bufio.Reader, out io.Writer) (string, bool, error) {
	if flag := cliutil.FlagStr(cmd, "tmux-conf"); flag != "" {
		if !slices.Contains(validTmuxConf, flag) {
			return "", false, fmt.Errorf("invalid --tmux-conf value %q (valid: %s)", flag, strings.Join(validTmuxConf, ", "))
		}
		return flag, false, nil
	}
	class, userConfig := classifyTmuxConfig(cliutil.Layout().HomeDir)
	return wizardTmuxConf(ctx, reader, out, class, userConfig)
}

// resolveChoice returns the chosen name for a backend/agent step. A non-empty
// flag is validated against the available list; otherwise it auto-picks when
// exactly one is available, prompts when several are, and returns "" (nothing
// to write) when none are.
func resolveChoice(ctx context.Context, reader *bufio.Reader, out io.Writer, kind, flag string, choices []setupChoice, heading string, defaultIdx int) (string, error) {
	if flag != "" {
		if !containsChoice(choices, flag) {
			return "", fmt.Errorf("invalid --%s value %q (available: %s)", kind, flag, joinChoiceNames(choices))
		}
		return flag, nil
	}
	switch len(choices) {
	case 0:
		return "", nil
	case 1:
		return choices[0].Name, nil
	default:
		return wizardChoice(ctx, reader, out, heading, choices, defaultIdx)
	}
}

// classifyTmuxConfig reads ~/.tmux.conf and returns its classification and
// content. Returns tmuxNone with empty content if the file doesn't exist.
func classifyTmuxConfig(homeDir string) (tmuxClass, string) {
	data, err := os.ReadFile(filepath.Join(homeDir, ".tmux.conf")) //nolint:gosec // G304: standard config path
	if err != nil {
		return tmuxNone, ""
	}
	content := string(data)
	if countSignificantLines(content) > significantLineThreshold {
		return tmuxLarge, content
	}
	return tmuxSmall, content
}

// countSignificantLines counts non-blank, non-comment lines.
// Note: scanner.Err() is not checked because strings.Reader never returns an
// I/O error — Scan only returns false on EOF.
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

// wizardTmuxConf runs the tmux-config step. Returns (chosen mode, previewed,
// error). When previewed is true the caller should exit without writing config.
//
// Power-user shortcut: tmuxLarge auto-picks "default+host" without a prompt
// (the user has a substantial config they presumably want to keep).
func wizardTmuxConf(ctx context.Context, reader *bufio.Reader, out io.Writer, class tmuxClass, userConfig string) (string, bool, error) {
	if class == tmuxLarge {
		return "default+host", false, nil
	}
	noConfig := class == tmuxNone

	fmt.Fprintln(out) //nolint:errcheck
	if noConfig {
		fmt.Fprintln(out, "yoloai uses tmux in sandboxes. No ~/.tmux.conf found, so we'll")           //nolint:errcheck
		fmt.Fprintln(out, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck
	} else {
		fmt.Fprintln(out, "yoloai uses tmux in sandboxes. Your tmux config is minimal, so we'll")     //nolint:errcheck
		fmt.Fprintln(out, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck
		fmt.Fprintln(out)                                                                             //nolint:errcheck
		fmt.Fprintln(out, "Your config (~/.tmux.conf):")                                              //nolint:errcheck
		for _, line := range strings.Split(strings.TrimRight(userConfig, "\n"), "\n") {
			fmt.Fprintf(out, "  %s\n", line) //nolint:errcheck
		}
	}

	fmt.Fprintln(out) //nolint:errcheck
	if noConfig {
		fmt.Fprintln(out, "  [Y] Use yoloai defaults")                                //nolint:errcheck
		fmt.Fprintln(out, "  [n] Use raw tmux (no config)")                           //nolint:errcheck
		fmt.Fprintln(out, "  [p] Print yoloai defaults and exit (for manual review)") //nolint:errcheck
	} else {
		fmt.Fprintln(out, "  [Y] Use yoloai defaults + your config (yours overrides on conflict)") //nolint:errcheck
		fmt.Fprintln(out, "  [n] Use only your config as-is")                                      //nolint:errcheck
		fmt.Fprintln(out, "  [p] Print merged config and exit (for manual review)")                //nolint:errcheck
	}
	fmt.Fprint(out, "\nChoice [Y/n/p]: ") //nolint:errcheck

	line, err := readLineCtx(ctx, reader)
	if err != nil {
		return "", false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))

	switch answer {
	case "p":
		fmt.Fprintln(out)                            //nolint:errcheck
		fmt.Fprintln(out, "--- yoloai defaults ---") //nolint:errcheck
		fmt.Fprint(out, string(tmuxres.Embedded()))  //nolint:errcheck
		if !noConfig && userConfig != "" {
			fmt.Fprintln(out)                        //nolint:errcheck
			fmt.Fprintln(out, "--- your config ---") //nolint:errcheck
			fmt.Fprint(out, userConfig)              //nolint:errcheck
		}
		fmt.Fprintln(out) //nolint:errcheck
		return "", true, nil
	case "n", "no":
		if noConfig {
			return "none", false, nil
		}
		return "host", false, nil
	default: // "", "y", "yes", or anything else treated as default
		if noConfig {
			return "default", false, nil
		}
		return "default+host", false, nil
	}
}

// wizardChoice prompts for one of `choices` (1-indexed in the UI), defaulting
// to `defaultIdx`. Returns the chosen name. Used for both backend and agent.
func wizardChoice(ctx context.Context, reader *bufio.Reader, out io.Writer, heading string, choices []setupChoice, defaultIdx int) (string, error) {
	fmt.Fprintln(out)          //nolint:errcheck
	fmt.Fprintln(out, heading) //nolint:errcheck
	fmt.Fprintln(out)          //nolint:errcheck
	for i, c := range choices {
		fmt.Fprintf(out, "  [%d] %-14s %s\n", i+1, c.Name, c.Blurb) //nolint:errcheck
	}
	fmt.Fprintf(out, "\nChoice [%d]: ", defaultIdx+1) //nolint:errcheck

	line, err := readLineCtx(ctx, reader)
	if err != nil {
		return "", err
	}
	answer := strings.TrimSpace(line)
	idx := defaultIdx
	if answer != "" {
		if n, parseErr := strconv.Atoi(answer); parseErr == nil && n >= 1 && n <= len(choices) {
			idx = n - 1
		}
	}
	return choices[idx].Name, nil
}

// defaultAgentIdx returns the index of "claude" in choices when present, else 0.
func defaultAgentIdx(choices []setupChoice) int {
	for i, c := range choices {
		if c.Name == "claude" {
			return i
		}
	}
	return 0
}

// containsChoice reports whether name matches one of the choices.
func containsChoice(choices []setupChoice, name string) bool {
	for _, c := range choices {
		if c.Name == name {
			return true
		}
	}
	return false
}

// joinChoiceNames renders the choice names for an error message:
// "docker, podman, tart".
func joinChoiceNames(choices []setupChoice) string {
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

// readLineCtx reads a line from reader, returning early if ctx is cancelled. On
// EOF, returns ("", nil) so callers can treat it as a default answer.
func readLineCtx(ctx context.Context, reader *bufio.Reader) (string, error) {
	ch := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				ch <- line // may have a final line without newline
				return
			}
			errCh <- err
			return
		}
		ch <- line
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line := <-ch:
		return line, nil
	case err := <-errCh:
		return "", err
	}
}

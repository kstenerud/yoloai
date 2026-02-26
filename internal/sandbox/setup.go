package sandbox

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/docker"
)

// ErrSetupPreview signals that the user chose [p] to preview the merged
// tmux config and the setup should exit cleanly without setting setup_complete.
var ErrSetupPreview = errors.New("setup preview requested")

// tmuxConfigClass describes the user's existing tmux configuration.
type tmuxConfigClass int

const (
	tmuxConfigNone  tmuxConfigClass = iota // no ~/.tmux.conf
	tmuxConfigSmall                        // ≤10 significant lines
	tmuxConfigLarge                        // >10 significant lines
)

const significantLineThreshold = 10

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

// runNewUserSetup orchestrates the interactive first-run setup prompts.
// Returns ErrSetupPreview if the user chose [p].
func (m *Manager) runNewUserSetup() error {
	class, userConfig := classifyTmuxConfig()

	switch class {
	case tmuxConfigLarge:
		// Power user — skip prompt, auto-configure default+host
		if err := m.setTmuxConf("default+host"); err != nil {
			return err
		}
		return m.setSetupComplete()

	case tmuxConfigNone:
		return m.promptTmuxSetup("", true)

	case tmuxConfigSmall:
		return m.promptTmuxSetup(userConfig, false)
	}

	return nil
}

// promptTmuxSetup shows the tmux config prompt and handles the user's choice.
func (m *Manager) promptTmuxSetup(userConfig string, noConfig bool) error {
	fmt.Fprintln(m.output) //nolint:errcheck // best-effort output
	if noConfig {
		fmt.Fprintln(m.output, "yoloai uses tmux in sandboxes. No ~/.tmux.conf found, so we'll") //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintln(m.output, "yoloai uses tmux in sandboxes. Your tmux config is minimal, so we'll") //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output)          //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "Your config (~/.tmux.conf):") //nolint:errcheck // best-effort output
		for _, line := range strings.Split(strings.TrimRight(userConfig, "\n"), "\n") {
			fmt.Fprintf(m.output, "  %s\n", line) //nolint:errcheck // best-effort output
		}
	}

	fmt.Fprintln(m.output) //nolint:errcheck // best-effort output

	if noConfig {
		fmt.Fprintln(m.output, "  [Y] Use yoloai defaults")                                //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [n] Use raw tmux (no config)")                            //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [p] Print yoloai defaults and exit (for manual review)")  //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintln(m.output, "  [Y] Use yoloai defaults + your config (yours overrides on conflict)") //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [n] Use only your config as-is")                                      //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "  [p] Print merged config and exit (for manual review)")                 //nolint:errcheck // best-effort output
	}

	fmt.Fprint(m.output, "\nChoice [Y/n/p]: ") //nolint:errcheck // best-effort output

	scanner := bufio.NewScanner(m.input)
	answer := ""
	if scanner.Scan() {
		answer = strings.TrimSpace(strings.ToLower(scanner.Text()))
	}

	switch answer {
	case "", "y", "yes":
		if noConfig {
			if err := m.setTmuxConf("default"); err != nil {
				return err
			}
		} else {
			if err := m.setTmuxConf("default+host"); err != nil {
				return err
			}
		}
		return m.setSetupComplete()

	case "n", "no":
		if noConfig {
			if err := m.setTmuxConf("none"); err != nil {
				return err
			}
		} else {
			if err := m.setTmuxConf("host"); err != nil {
				return err
			}
		}
		return m.setSetupComplete()

	case "p":
		fmt.Fprintln(m.output) //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "--- yoloai defaults ---") //nolint:errcheck // best-effort output
		fmt.Fprint(m.output, string(docker.EmbeddedTmuxConf()))  //nolint:errcheck // best-effort output
		if !noConfig && userConfig != "" {
			fmt.Fprintln(m.output)                              //nolint:errcheck // best-effort output
			fmt.Fprintln(m.output, "--- your config ---")       //nolint:errcheck // best-effort output
			fmt.Fprint(m.output, userConfig)                    //nolint:errcheck // best-effort output
		}
		fmt.Fprintln(m.output) //nolint:errcheck // best-effort output
		return ErrSetupPreview

	default:
		// Treat unknown input as Y (default)
		if noConfig {
			if err := m.setTmuxConf("default"); err != nil {
				return err
			}
		} else {
			if err := m.setTmuxConf("default+host"); err != nil {
				return err
			}
		}
		return m.setSetupComplete()
	}
}

// setTmuxConf writes the tmux_conf setting to config.yaml.
func (m *Manager) setTmuxConf(value string) error {
	return updateConfigFields(map[string]string{
		"defaults.tmux_conf": value,
	})
}

// setSetupComplete marks setup as done and prints the completion message.
func (m *Manager) setSetupComplete() error {
	if err := updateConfigFields(map[string]string{
		"setup_complete": "true",
	}); err != nil {
		return err
	}
	fmt.Fprintln(m.output, "\nSetup complete. To re-run setup at any time: yoloai setup") //nolint:errcheck // best-effort output
	return nil
}

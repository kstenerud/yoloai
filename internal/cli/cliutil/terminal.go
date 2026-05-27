// ABOUTME: SetTerminalTitle — OSC-0 title write plus tmux window-rename for the
// ABOUTME: attached sandbox. Called by Attach paths around start/stop.

package cliutil

import (
	"fmt"
	"os"
	"os/exec"
)

// SetTerminalTitle sets the terminal title for the host terminal.
// It emits an OSC 0 escape sequence (works for non-tmux terminals)
// and, if running inside a host tmux session, also renames the tmux
// window so the title shows in the tmux status bar. When title is
// empty, it restores the previous state (clears OSC title and
// unsets per-window tmux overrides to revert to user defaults).
func SetTerminalTitle(title string) {
	fmt.Fprintf(os.Stdout, "\033]0;%s\007", title) //nolint:errcheck // best-effort terminal title

	// If inside a host tmux session, also set the window name.
	if os.Getenv("TMUX") == "" {
		return
	}
	if title != "" {
		// Disable automatic-rename (tmux tracking the foreground
		// process name) and allow-rename (programs sending escape
		// sequences to rename the window) so our title sticks while
		// the sandbox is attached.
		exec.Command("tmux", "set-option", "-w", "automatic-rename", "off").Run() //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "set-option", "-w", "allow-rename", "off").Run()     //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "rename-window", title).Run()                        //nolint:errcheck,gosec // best-effort
	} else {
		// Unset per-window overrides so the window reverts to the
		// user's session/global defaults after detach.
		exec.Command("tmux", "set-option", "-wu", "automatic-rename").Run() //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "set-option", "-wu", "allow-rename").Run()     //nolint:errcheck,gosec // best-effort
	}
}

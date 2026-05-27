// ABOUTME: `yoloai system setup` — interactive setup wizard. Asks the user
// ABOUTME: about tmux config / default backend / default agent, then writes
// ABOUTME: the answers via yoloai.SystemClient.Setup. Q-F: prompts live in
// ABOUTME: this CLI file so the library Setup is non-interactive.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

// runSystemSetup is `yoloai system setup`'s entry point. Inspects the
// host via SystemClient.SetupStatus, then fills SetupOptions from
// flags or interactive prompts, then calls SystemClient.Setup.
//
// Returns nil and writes nothing if the user chooses [p] at the tmux
// prompt (preview-then-exit, intentional).
func runSystemSetup(cmd *cobra.Command) error {
	sc := cliutil.NewSystemClient()
	ctx := cmd.Context()

	status, err := sc.SetupStatus(ctx)
	if err != nil {
		return err
	}

	agentFlag, _ := cmd.Flags().GetString("agent")
	backendFlag, _ := cmd.Flags().GetString("backend")
	tmuxConfFlag, _ := cmd.Flags().GetString("tmux-conf")

	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.ErrOrStderr()

	opts := yoloai.SetupOptions{
		Agent:    agentFlag,
		Backend:  backendFlag,
		TmuxConf: tmuxConfFlag,
	}

	if opts.TmuxConf == "" {
		var previewed bool
		opts.TmuxConf, previewed, err = wizardTmuxConf(ctx, reader, out, status)
		if err != nil {
			return err
		}
		if previewed {
			// User chose [p] — they wanted to inspect, not commit.
			// Exit cleanly without touching config.
			return nil
		}
	}

	if opts.Backend == "" && len(status.AvailableBackends) > 1 {
		opts.Backend, err = wizardChoice(ctx, reader, out, "Default runtime backend:", status.AvailableBackends, defaultBackendIdx(status.AvailableBackends))
		if err != nil {
			return err
		}
	}

	if opts.Agent == "" && len(status.AvailableAgents) > 1 {
		opts.Agent, err = wizardChoice(ctx, reader, out, "Default agent:", status.AvailableAgents, defaultAgentIdx(status.AvailableAgents))
		if err != nil {
			return err
		}
	}

	if err := sc.Setup(ctx, opts); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nSetup complete. To re-run setup at any time: yoloai system setup") //nolint:errcheck
	return nil
}

// wizardTmuxConf runs the tmux-config step of the wizard. Returns
// (chosen mode, previewed, error). When previewed is true the caller
// should exit without writing config.
//
// Power-user shortcut: TmuxConfigLarge auto-picks "default+host"
// without a prompt (the user has a substantial config they presumably
// want to keep).
func wizardTmuxConf(ctx context.Context, reader *bufio.Reader, out io.Writer, status *yoloai.SetupStatus) (string, bool, error) {
	if status.TmuxClass == yoloai.TmuxConfigLarge {
		return "default+host", false, nil
	}
	noConfig := status.TmuxClass == yoloai.TmuxConfigNone

	fmt.Fprintln(out) //nolint:errcheck
	if noConfig {
		fmt.Fprintln(out, "yoloai uses tmux in sandboxes. No ~/.tmux.conf found, so we'll")           //nolint:errcheck
		fmt.Fprintln(out, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck
	} else {
		fmt.Fprintln(out, "yoloai uses tmux in sandboxes. Your tmux config is minimal, so we'll")     //nolint:errcheck
		fmt.Fprintln(out, "include sensible defaults (mouse scroll, colors, vim-friendly settings).") //nolint:errcheck
		fmt.Fprintln(out)                                                                             //nolint:errcheck
		fmt.Fprintln(out, "Your config (~/.tmux.conf):")                                              //nolint:errcheck
		for _, line := range strings.Split(strings.TrimRight(status.UserTmuxConfig, "\n"), "\n") {
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
		fmt.Fprint(out, status.DefaultTmuxConfig)    //nolint:errcheck
		if !noConfig && status.UserTmuxConfig != "" {
			fmt.Fprintln(out)                        //nolint:errcheck
			fmt.Fprintln(out, "--- your config ---") //nolint:errcheck
			fmt.Fprint(out, status.UserTmuxConfig)   //nolint:errcheck
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

// wizardChoice prompts for one of `choices` (1-indexed in the UI),
// defaulting to `defaultIdx`. Returns the chosen name. Used for both
// backend and agent picks.
func wizardChoice(ctx context.Context, reader *bufio.Reader, out io.Writer, heading string, choices []yoloai.SetupChoice, defaultIdx int) (string, error) {
	fmt.Fprintln(out)          //nolint:errcheck
	fmt.Fprintln(out, heading) //nolint:errcheck
	fmt.Fprintln(out)          //nolint:errcheck
	for i, c := range choices {
		fmt.Fprintf(out, "  [%d] %-10s %s\n", i+1, c.Name, c.Blurb) //nolint:errcheck
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

// defaultBackendIdx returns the index of the preferred default
// backend in choices (currently always 0 — the registry order does
// the ranking).
func defaultBackendIdx(_ []yoloai.SetupChoice) int { return 0 }

// defaultAgentIdx returns the index of "claude" in choices when
// present, else 0.
func defaultAgentIdx(choices []yoloai.SetupChoice) int {
	for i, c := range choices {
		if c.Name == "claude" {
			return i
		}
	}
	return 0
}

// readLineCtx reads a line from reader, returning early if ctx is
// cancelled. On EOF, returns ("", nil) so callers can treat it as a
// default answer.
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

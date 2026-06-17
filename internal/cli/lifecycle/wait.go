// ABOUTME: Cobra "wait" command: blocks until a sandbox's agent reaches the
// ABOUTME: requested condition (idle or exit), then prints its final status.
package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

func NewWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "wait <name>",
		Short:   "Wait for a sandbox's agent to finish (idle or exit)",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ExactArgs(1),
		RunE:    runWaitCmd,
	}

	cmd.Flags().String("for", "idle", "Condition to wait for: 'idle' (agent stops working, waiting at the prompt) or 'exit' (agent session ends)")
	cmd.Flags().Duration("timeout", 0, "Maximum time to wait, e.g. 15m; 0 waits indefinitely")

	return cmd
}

func runWaitCmd(cmd *cobra.Command, args []string) error {
	name := args[0]
	if err := cliutil.ValidateName(name); err != nil {
		return err
	}

	cond, err := parseWaitCondition(cliutil.FlagStr(cmd, "for"))
	if err != nil {
		return err
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")

	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		info, err := sb.Wait(ctx, yoloai.SandboxWaitOptions{For: cond, Timeout: timeout})
		if err != nil {
			// On timeout, name where the sandbox stalled rather than a bare
			// "timed out" — the last-observed status is the useful signal.
			if info != nil && errors.Is(err, yoloai.ErrWaitTimeout) {
				return fmt.Errorf("%w (last status: %s)", err, info.Status)
			}
			return cliutil.SandboxErrorHint(name, err)
		}
		return writeWaitResult(cmd, info)
	})
}

// parseWaitCondition maps the --for flag to a library WaitCondition. The CLI
// defaults to idle (not the library's WaitForExit zero value): the agents this
// targets run interactively and never exit, so an exit default would hang.
func parseWaitCondition(s string) (yoloai.WaitCondition, error) {
	switch s {
	case "idle":
		return yoloai.WaitForIdle, nil
	case "exit":
		return yoloai.WaitForExit, nil
	default:
		return 0, yoerrors.NewUsageError("invalid --for value %q: must be 'idle' or 'exit'", s)
	}
}

// writeWaitResult emits the final SandboxInfo. JSON mode mirrors `sandbox info
// --json` (a SandboxInfo object) so callers can reuse one parser.
func writeWaitResult(cmd *cobra.Command, info *yoloai.SandboxInfo) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), info)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", info.Environment.Name, info.Status)
	return err
}

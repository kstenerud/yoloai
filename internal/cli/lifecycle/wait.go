// ABOUTME: Cobra "wait" command: blocks until a sandbox's agent reaches the
// ABOUTME: requested condition (idle or exit), then prints its final status,
// ABOUTME: propagating the agent's exit code (or 124 on timeout).
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"

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

	cmd.Flags().String("for", "idle", "Condition to wait for: 'idle' (agent stops working, waiting at the prompt) or 'exit' (agent session ends; yoloai then exits with the agent's own exit code)")
	cmd.Flags().Duration("timeout", 0, "Maximum time to wait, e.g. 15m; 0 waits indefinitely. On timeout, exits 124 (like timeout(1))")

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
			// os.Exit(124) mirrors timeout(1)'s convention rather than going
			// through the normal error path, which would print a spurious
			// "Error:" line for what is an expected, non-exceptional outcome.
			if info != nil && errors.Is(err, yoloai.ErrWaitTimeout) {
				fmt.Fprintf(cmd.ErrOrStderr(), "timed out waiting for sandbox %s (last status: %s)\n", name, info.Status) //nolint:errcheck
				os.Exit(124)
			}
			return cliutil.SandboxErrorHint(name, err)
		}
		if werr := writeWaitResult(cmd, info); werr != nil {
			return werr
		}
		if code := waitExitCode(info.Status, info.ExitCode); code != 0 {
			os.Exit(code)
		}
		return nil
	})
}

// waitExitCode maps a completed wait's result to the process exit code the way
// `exec` propagates a child command's status: a failed agent yields its own
// exit code (or 1 if the monitor recorded none), and every non-failure result
// (done, idle, or any other terminal state) yields 0.
func waitExitCode(status yoloai.Status, agentExit *int) int {
	if status != yoloai.StatusFailed {
		return 0
	}
	if agentExit != nil && *agentExit != 0 {
		return *agentExit
	}
	return 1
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

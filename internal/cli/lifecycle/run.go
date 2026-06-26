// ABOUTME: 'run' command — create and run an agent headlessly to completion.
// ABOUTME: Sets SandboxCreateOptions.Headless (prompt baked into the agent's own
// ABOUTME: -p mode), then starts and optionally waits/destroys. D100.
package lifecycle

import (
	"context"
	"fmt"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

func NewRunCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] <name> [workdir] -p <prompt> [-d <dir>...] [-- <agent-args>...]",
		Short: "Run an agent headlessly to completion",
		Long: "Create a sandbox and run the agent in its own headless mode (e.g. claude -p): " +
			"the prompt is baked into the launch command and the task ends when the agent exits. " +
			"A prompt is required; the workdir is optional. By default run returns once the agent " +
			"is launched — use --wait to block until it finishes, or --rm to also destroy the " +
			"sandbox afterwards (--rm implies --wait). For fire-and-forget, background it with '&'.",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunCmd(cmd, args, version)
		},
	}

	addCreateFlags(cmd)
	cmd.Flags().Bool("wait", false, "Block until the agent finishes (exit code reflects the agent's outcome)")
	cmd.Flags().Bool("rm", false, "Destroy the sandbox after the agent finishes (implies --wait)")

	return cmd
}

func runRunCmd(cmd *cobra.Command, args []string, version string) error {
	name, rawWorkdirArg, passthrough, profileFlag, err := parseRunCmdPositional(cmd, args)
	if err != nil {
		return err
	}

	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	if prompt == "" && promptFile == "" {
		return yoerrors.NewUsageError("yoloai run requires a prompt (--prompt or --prompt-file)")
	}

	opts, err := resolveCreateOptions(cmd, name, rawWorkdirArg, passthrough, profileFlag)
	if err != nil {
		return err
	}
	opts.Headless = true

	wait, _ := cmd.Flags().GetBool("wait")
	rm, _ := cmd.Flags().GetBool("rm")
	if rm {
		wait = true // --rm needs a foreground waiter to observe completion before destroying
	}

	if !cliutil.JSONEnabled(cmd) {
		cliutil.WarnIfLowDisk(cmd.ErrOrStderr(), cliutil.Layout().SandboxesDir())
	}

	c, err := newCreateClient(cmd, version)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup

	return executeRun(cmd, cmd.Context(), c, opts, wait, rm)
}

// parseRunCmdPositional splits run's positional args: <name> is required, [workdir]
// is optional (run supports a no-workdir agent that just makes API calls), and
// anything after `--` is passed through to the agent.
func parseRunCmdPositional(cmd *cobra.Command, args []string) (name, rawWorkdirArg string, passthrough []string, profileFlag string, err error) {
	dashIdx := cmd.ArgsLenAtDash()
	var positional []string
	if dashIdx < 0 {
		positional = args
	} else {
		positional = args[:dashIdx]
		passthrough = args[dashIdx:]
	}

	profileFlag = cliutil.ResolveProfile(cmd)

	if len(positional) < 1 {
		return "", "", nil, "", yoerrors.NewUsageError("sandbox name is required")
	}
	if len(positional) > 2 {
		return "", "", nil, "", yoerrors.NewUsageError("too many positional arguments (expected <name> [workdir])")
	}

	name = positional[0]
	if len(positional) >= 2 {
		rawWorkdirArg = positional[1]
	}
	return name, rawWorkdirArg, passthrough, profileFlag, nil
}

// executeRun provisions the sandbox headless, starts it, and — when wait — blocks
// until the agent exits, then optionally destroys it. Without wait it returns as
// soon as the agent is launched (the sandbox persists in StatusActive for later
// inspect/diff/apply). The exit code reflects the agent: a failed agent returns a
// non-nil error so the process exits non-zero.
func executeRun(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, opts yoloai.SandboxCreateOptions, wait, rm bool) error {
	sb, err := createSandboxWithDirtyRetry(cmd, ctx, c, opts)
	if err != nil {
		return err
	}
	if cliutil.BugReportFile != nil {
		cliutil.BugReportSandboxName = sb.Name()
	}
	if _, err := sb.Start(ctx, yoloai.SandboxStartOptions{Env: opts.Env}); err != nil {
		return err
	}

	if !wait {
		if cliutil.JSONEnabled(cmd) {
			meta, loadErr := loadCreatedMeta(c, sb.Name())
			if loadErr != nil {
				return loadErr
			}
			return cliutil.WriteJSON(cmd.OutOrStdout(), meta)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Sandbox %s running headlessly. Check with 'yoloai sandbox info %s'; wait with 'yoloai wait %s'.\n", //nolint:errcheck // best-effort output
			sb.Name(), sb.Name(), sb.Name())
		return nil
	}

	info, err := sb.Wait(ctx, yoloai.SandboxWaitOptions{For: yoloai.WaitForExit})
	if err != nil {
		return err
	}

	if rm {
		// --rm discards the sandbox regardless of unapplied work — the caller
		// opted into a throwaway run.
		if _, err := sb.Destroy(ctx, yoloai.SandboxDestroyOptions{AbandonUnappliedWork: true}); err != nil {
			return err
		}
	}

	if cliutil.JSONEnabled(cmd) {
		if err := cliutil.WriteJSON(cmd.OutOrStdout(), info); err != nil {
			return err
		}
	} else if info.Status != yoloai.StatusFailed {
		fmt.Fprintf(cmd.ErrOrStderr(), "Agent finished in sandbox %s (%s).\n", sb.Name(), info.Status) //nolint:errcheck // best-effort output
	}

	// The exit code reflects the agent: a failed agent makes `run` exit non-zero
	// (any returned error maps to exit 1), so `yoloai run … --wait && next` works.
	if info.Status == yoloai.StatusFailed {
		return fmt.Errorf("agent in sandbox %s exited with a non-zero status", sb.Name())
	}
	return nil
}

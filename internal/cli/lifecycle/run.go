// ABOUTME: 'run' command — create and run an agent headlessly to completion.
// ABOUTME: Sets SandboxCreateOptions.Headless (prompt baked into the agent's own
// ABOUTME: -p mode), then starts and optionally waits/destroys. D100.
package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// rollbackFailedStart destroys a sandbox whose start failed during a composite
// new/run, so re-running the exact same command isn't balked by the half-created
// sandbox left behind (a failed `yoloai new foo` must leave nothing, else the
// retry hits "already exists"). Best-effort: the user sees the original start
// error; a cleanup failure is only logged. Uses a detached, time-bounded context
// because Ctrl-C — the common trigger — has already cancelled ctx, and teardown
// needs a live context to reach the backend.
func rollbackFailedStart(ctx context.Context, sb *yoloai.Sandbox) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if _, err := sb.Destroy(cleanupCtx, yoloai.SandboxDestroyOptions{AbandonUnappliedWork: true}); err != nil {
		slog.Warn("could not roll back sandbox after failed start", "sandbox", sb.Name(), "err", err)
	}
}

func NewRunCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] <name> <workdir> -p <prompt> [-d <dir>...] [-- <agent-args>...]",
		Short: "Run an agent headlessly to completion",
		Long: "Create a sandbox and run the agent in its own headless mode (e.g. claude -p): " +
			"the prompt is baked into the launch command and the task ends when the agent exits. " +
			"A prompt and workdir are required. By default run returns once the agent is launched — " +
			"use --wait to block until it finishes, or --rm to also destroy the sandbox afterwards " +
			"(--rm implies --wait). For fire-and-forget, background it with '&'.",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunCmd(cmd, args, version)
		},
	}

	addCreateFlags(cmd)
	cmd.Flags().Bool("wait", false, "Block until the agent finishes (exit code reflects the agent's outcome)")
	cmd.Flags().Bool("rm", false, "Destroy the sandbox after the agent finishes (implies --wait)")
	cmd.Flags().Bool("tty", false, "Run the agent interactively (in a tmux pane you can attach to) instead of headless — useful for monitoring/debugging")

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
	// A workdir-less run (the agent just makes API calls) is the intended end
	// state, but it needs the no-Dirs[0]-workdir pipeline work tracked in DF49.
	// Until then, require a workdir like `new` does.
	if rawWorkdirArg == "" {
		return yoerrors.NewUsageError("workdir is required\n\nUsage: yoloai run [flags] <name> <workdir> --prompt <text>\n\nExample: yoloai run %s . --prompt \"fix the bug\"", name)
	}

	opts, err := resolveCreateOptions(cmd, name, rawWorkdirArg, passthrough, profileFlag)
	if err != nil {
		return err
	}
	// run requests headless by default; --tty forces the interactive flow. The
	// request may still be downgraded internally when headless is unsafe for the
	// agent without an API key (D101).
	tty, _ := cmd.Flags().GetBool("tty")
	opts.Headless = !tty

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

// parseRunCmdPositional splits run's positional args: <name> is required and
// [workdir] is the second positional, with anything after `--` passed through to
// the agent. The workdir is parsed as optional (the design allows a no-workdir
// run), but a true no-workdir mode requires breaking the "workdir is Dirs[0]"
// invariant across the create/diff pipeline (DF49), so until that lands the
// caller still requires a workdir — enforced in runRunCmd, not here, so the
// parser stays a pure split.
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

	// Read the effective launch mode: create may have downgraded a requested
	// headless to interactive when the agent's headless mode is unsafe without an
	// API key (D101). It drives the wait condition (a headless agent exits; an
	// interactive one finishes its turn and goes idle) and the fallback notice.
	headless := opts.Headless
	if meta, metaErr := sb.Metadata(); metaErr == nil {
		headless = meta.Headless
	}
	if opts.Headless && !headless && !cliutil.JSONEnabled(cmd) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: no usable credentials for %s headless mode — running interactively (attach with 'yoloai attach %s' to authenticate/monitor).\n", //nolint:errcheck // best-effort output
			opts.AgentType, sb.Name())
	}

	if _, err := sb.Start(ctx, yoloai.SandboxStartOptions{Env: opts.Env, Broker: opts.Broker, NoBroker: opts.NoBroker}); err != nil {
		rollbackFailedStart(ctx, sb)
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
		fmt.Fprintf(cmd.ErrOrStderr(), "Sandbox %s running. Check with 'yoloai sandbox info %s'; wait with 'yoloai wait %s'.\n", //nolint:errcheck // best-effort output
			sb.Name(), sb.Name(), sb.Name())
		return nil
	}

	return waitForRunResult(cmd, ctx, sb, headless, rm)
}

// waitForRunResult blocks until the agent completes, optionally destroys the
// sandbox (--rm), reports the outcome, and maps a failed agent to a non-zero
// exit. A headless agent exits when done (WaitForExit); an interactive one
// (the D101 TTY fallback) finishes its turn and goes idle without exiting
// (WaitForIdle).
func waitForRunResult(cmd *cobra.Command, ctx context.Context, sb *yoloai.Sandbox, headless, rm bool) error {
	waitFor := yoloai.WaitForExit
	if !headless {
		waitFor = yoloai.WaitForIdle
	}
	info, err := sb.Wait(ctx, yoloai.SandboxWaitOptions{For: waitFor})
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

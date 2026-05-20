package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

type attachOpts struct {
	resume bool
}

func newAttachCmd() *cobra.Command {
	opts := &attachOpts{}
	cmd := &cobra.Command{
		Use:     "attach <name>",
		Short:   "Attach to a sandbox's session (tmux)",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runAttach(cmd, args, opts) },
	}

	cmd.Flags().BoolVar(&opts.resume, "resume", false, "Restart agent with resume prompt before attaching")

	return cmd
}

// runAttach implements the attach command body.
func runAttach(cmd *cobra.Command, args []string, opts *attachOpts) error {
	if jsonEnabled(cmd) {
		return errJSONNotSupported("attach")
	}

	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}
	defer openCLIJSONLSink(name, cmd)()

	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		return attachInRuntime(cmd, ctx, rt, name, opts)
	})
}

// attachInRuntime resolves sandbox status and attaches to its tmux session.
func attachInRuntime(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, opts *attachOpts) error {
	info, err := sandbox.InspectSandbox(ctx, rt, name)
	if err != nil {
		return sandboxErrorHint(name, err)
	}

	containerName := sandbox.InstanceName(name)
	user := tmuxExecUser(info.Meta)

	if err := checkAttachStatus(info.Status, name, opts.resume); err != nil {
		return err
	}

	// --resume: restart agent before attaching
	if opts.resume && info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		if err := mgr.Start(ctx, name, sandbox.StartOptions{Resume: true}); err != nil {
			return err
		}
		if err := waitForTmux(ctx, rt, containerName, name, 300*time.Second, user); err != nil {
			return fmt.Errorf("waiting for tmux session: %w", err)
		}
	}

	slog.Debug("attaching to tmux session", "event", "sandbox.attach", "container", containerName) //nolint:gosec // G706: containerName comes from trusted sandbox metadata
	return attachToSandbox(ctx, rt, containerName, name, user)
}

// checkAttachStatus returns an error if the sandbox status does not allow attach.
func checkAttachStatus(status sandbox.Status, name string, resume bool) error {
	switch status {
	case sandbox.StatusActive, sandbox.StatusIdle, sandbox.StatusDone, sandbox.StatusFailed:
		return nil // OK — user can attach to see output
	case sandbox.StatusStopped:
		if resume {
			return nil
		}
	case sandbox.StatusRemoved, sandbox.StatusBroken, sandbox.StatusUnavailable:
		// fall through to the not-running error
	}
	return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
}

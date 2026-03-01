package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// Command group IDs for help output.
const (
	groupWorkflow  = "workflow"
	groupLifecycle = "lifecycle"
	groupInspect   = "inspect"
	groupAdmin     = "admin"
)

// registerCommands adds all subcommands to the root command.
func registerCommands(root *cobra.Command, version, commit, date string) {
	root.AddGroup(
		&cobra.Group{ID: groupWorkflow, Title: "Core Workflow:"},
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: groupInspect, Title: "Inspection:"},
		&cobra.Group{ID: groupAdmin, Title: "Admin:"},
	)

	root.AddCommand(
		// Workflow
		newNewCmd(version),
		newAttachCmd(),
		newDiffCmd(),
		newApplyCmd(),

		// Lifecycle
		newStartCmd(),
		newStopCmd(),
		newDestroyCmd(),
		newResetCmd(),

		// Inspection
		newSystemCmd(version, commit, date),
		newSandboxCmd(),
		newLsAliasCmd(),
		newLogAliasCmd(),

		// Admin
		newHelpCmd(),
		newConfigCmd(),
		newCompletionCmd(),
		newVersionCmd(version, commit, date),
	)
}

func newLsAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List sandboxes (shortcut for 'sandbox list')",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE:    runList,
	}
}

func newLogAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "log <name>",
		Short:   "Show sandbox log (shortcut for 'sandbox log')",
		GroupID: groupInspect,
		Args:    cobra.ArbitraryArgs,
		RunE:    runLog,
	}
	cmd.Flags().Bool("no-strip", false, "Show raw output with ANSI escape sequences")
	return cmd
}

func newNewCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "new [flags] <name> <workdir> [-d <dir>...] [-- <agent-args>...]",
		Short:   "Create and start a sandbox",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse positional args considering --
			dashIdx := cmd.ArgsLenAtDash()
			var positional, passthrough []string
			if dashIdx < 0 {
				positional = args
			} else {
				positional = args[:dashIdx]
				passthrough = args[dashIdx:]
			}

			if len(positional) < 1 {
				return sandbox.NewUsageError("sandbox name is required")
			}
			if len(positional) < 2 {
				return sandbox.NewUsageError("workdir is required\n\nUsage: yoloai new [flags] <name> <workdir> [-- <agent-args>...]\n\nExample: yoloai new %s .", positional[0])
			}
			if len(positional) > 2 {
				return sandbox.NewUsageError("too many positional arguments (expected <name> <workdir>)")
			}

			name := positional[0]
			workdirArg := positional[1]

			prompt, _ := cmd.Flags().GetString("prompt")
			promptFile, _ := cmd.Flags().GetString("prompt-file")
			model := resolveModel(cmd)
			agentName := resolveAgent(cmd)
			networkNone, _ := cmd.Flags().GetBool("network-none")
			ports, _ := cmd.Flags().GetStringArray("port")
			dirs, _ := cmd.Flags().GetStringArray("dir")
			replace, _ := cmd.Flags().GetBool("replace")
			noStart, _ := cmd.Flags().GetBool("no-start")
			attach, _ := cmd.Flags().GetBool("attach")
			yes, _ := cmd.Flags().GetBool("yes")

			debug, _ := cmd.Flags().GetBool("debug")

			backend := resolveBackend(cmd)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				sandboxName, err := mgr.Create(ctx, sandbox.CreateOptions{
					Name:        name,
					WorkdirArg:  workdirArg,
					AuxDirArgs:  dirs,
					Agent:       agentName,
					Model:       model,
					Prompt:      prompt,
					PromptFile:  promptFile,
					NetworkNone: networkNone,
					Ports:       ports,
					Replace:     replace,
					NoStart:     noStart,
					Attach:      attach,
					Yes:         yes,
					Passthrough: passthrough,
					Version:     version,
					Debug:       debug,
				})
				if err != nil {
					return err
				}

				if sandboxName == "" || !attach || noStart {
					return nil
				}

				// Wait for tmux session to be ready before attaching
				containerName := sandbox.InstanceName(sandboxName)
				if err := waitForTmux(ctx, rt, containerName, 30*time.Second); err != nil {
					return fmt.Errorf("waiting for tmux session: %w", err)
				}

				return attachToSandbox(ctx, rt, containerName)
			})
		},
	}

	cmd.Flags().StringP("prompt", "p", "", "Prompt text for the agent")
	cmd.Flags().StringP("prompt-file", "f", "", "File containing the prompt")
	cmd.Flags().StringP("model", "m", "", "Model name or alias")
	cmd.Flags().String("agent", "", "Agent to use (default from config or claude)")
	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().Bool("network-none", false, "Disable network access")
	cmd.Flags().StringArray("port", nil, "Port mapping (host:container)")
	cmd.Flags().StringArrayP("dir", "d", nil, "Auxiliary directory (repeatable, default read-only)")
	cmd.Flags().Bool("replace", false, "Replace existing sandbox")
	cmd.Flags().Bool("no-start", false, "Create but don't start the container")
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after creation")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmations")

	return cmd
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "completion [bash|zsh|fish|powershell]",
		Short:   "Generate shell completion script",
		GroupID: groupAdmin,
		Long: `Generate shell completion script for the specified shell.

To load completions:

Bash:
  source <(yoloai completion bash)

Zsh:
  source <(yoloai completion zsh)

Fish:
  yoloai completion fish | source

PowerShell:
  yoloai completion powershell | Out-String | Invoke-Expression`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return sandbox.NewUsageError("unsupported shell: %s (valid: bash, zsh, fish, powershell)", args[0])
			}
		},
	}
}

// waitForTmux polls until the tmux session is ready in the container.
// Returns early if the container stops running or the context is cancelled.
func waitForTmux(ctx context.Context, rt runtime.Runtime, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if context was cancelled (e.g. Ctrl+C)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check if container is still running
		info, err := rt.Inspect(ctx, containerName)
		if err != nil || !info.Running {
			return fmt.Errorf("container %s is not running", containerName)
		}

		// Check if tmux session exists
		_, err = rt.Exec(ctx, containerName, []string{"tmux", "has-session", "-t", "main"}, "yoloai")
		if err == nil {
			return nil
		}

		// Context-aware sleep
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("tmux session not ready after %s", timeout)
}

// attachToSandbox attaches to the tmux session in a running container.
func attachToSandbox(ctx context.Context, rt runtime.Runtime, containerName string) error {
	return rt.InteractiveExec(ctx, containerName, []string{"tmux", "attach", "-t", "main"}, "yoloai", "")
}

func newVersionCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version information",
		GroupID: groupAdmin,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "yoloai version %s (commit: %s, built: %s)\n", version, commit, date)
			return err
		},
	}
}

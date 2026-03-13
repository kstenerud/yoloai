package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

// Command group IDs for help output.
const (
	groupLifecycle    = "lifecycle"
	groupWorkflow     = "workflow"
	groupSandboxTools = "sandbox-tools"
	groupAdmin        = "admin"
)

// registerCommands adds all subcommands to the root command.
func registerCommands(root *cobra.Command, version, commit, date string) {
	root.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: groupWorkflow, Title: "Workflow:"},
		&cobra.Group{ID: groupSandboxTools, Title: "Sandbox Tools:"},
		&cobra.Group{ID: groupAdmin, Title: "Admin:"},
	)

	root.AddCommand(
		// Lifecycle
		newNewCmd(version),
		newCloneCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newDestroyCmd(),
		newResetCmd(),

		// Workflow
		newAttachCmd(),
		newDiffCmd(),
		newApplyCmd(),
		newFilesCmd(),
		newXCmd(),

		// Sandbox Tools
		newSandboxCmd(),
		newLsAliasCmd(),
		newLogAliasCmd(),
		newExecAliasCmd(),

		// Admin
		newSystemCmd(version, commit, date),
		newProfileCmd(),
		newHelpCmd(),
		newConfigCmd(),
		newVersionCmd(version, commit, date),
	)
}

func newLsAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List sandboxes (shortcut for 'sandbox list')",
		GroupID: groupSandboxTools,
		Args:    cobra.NoArgs,
		RunE:    runList,
	}
	addListFlags(cmd)
	return cmd
}

func newLogAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "log <name>",
		Short:   "Show sandbox log (shortcut for 'sandbox log')",
		GroupID: groupSandboxTools,
		Args:    cobra.ArbitraryArgs,
		RunE:    runLog,
	}
	cmd.Flags().Bool("raw", false, "Show raw output with ANSI escape sequences")
	return cmd
}

func newExecAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "exec <name> <command> [args...]",
		Short:   "Run a command inside a sandbox (shortcut for 'sandbox exec')",
		GroupID: groupSandboxTools,
		Args:    cobra.MinimumNArgs(1),
		RunE:    runExec,
	}
}

func newNewCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "new [flags] <name> [workdir] [-d <dir>...] [-- <agent-args>...]",
		Short:   "Create and start a sandbox",
		GroupID: groupLifecycle,
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

			profileFlag := resolveProfile(cmd)

			if len(positional) < 1 {
				return sandbox.NewUsageError("sandbox name is required")
			}
			if len(positional) < 2 && profileFlag == "" {
				return sandbox.NewUsageError("workdir is required (or use --profile)\n\nUsage: yoloai new [flags] <name> <workdir> [-- <agent-args>...]\n\nExample: yoloai new %s .", positional[0])
			}
			if len(positional) > 2 {
				return sandbox.NewUsageError("too many positional arguments (expected <name> [workdir])")
			}

			name := positional[0]
			var rawWorkdirArg string
			if len(positional) >= 2 {
				rawWorkdirArg = positional[1]
			}

			prompt, _ := cmd.Flags().GetString("prompt")
			promptFile, _ := cmd.Flags().GetString("prompt-file")
			model := resolveModel(cmd)
			agentName := resolveAgent(cmd)
			networkNone, _ := cmd.Flags().GetBool("network-none")
			networkIsolated, _ := cmd.Flags().GetBool("network-isolated")
			networkAllow, _ := cmd.Flags().GetStringSlice("network-allow")
			ports, _ := cmd.Flags().GetStringSlice("port")
			rawDirs, _ := cmd.Flags().GetStringSlice("dir")

			// --network-allow implies --network-isolated
			if len(networkAllow) > 0 {
				networkIsolated = true
			}

			replace, _ := cmd.Flags().GetBool("replace")
			force, _ := cmd.Flags().GetBool("force")
			if force {
				replace = true
			}
			noStart, _ := cmd.Flags().GetBool("no-start")
			attach, _ := cmd.Flags().GetBool("attach")

			if jsonEnabled(cmd) && attach {
				return fmt.Errorf("--json and --attach are incompatible")
			}
			if networkNone && len(ports) > 0 {
				return sandbox.NewUsageError("--port is incompatible with --network-none")
			}
			yes := effectiveYes(cmd)

			cpus, _ := cmd.Flags().GetString("cpus")
			memory, _ := cmd.Flags().GetString("memory")
			debug, _ := cmd.Flags().GetBool("debug")
			envSlice, _ := cmd.Flags().GetStringSlice("env")

			envMap := make(map[string]string, len(envSlice))
			for _, e := range envSlice {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					return sandbox.NewUsageError("invalid --env value %q: must be KEY=VAL", e)
				}
				envMap[k] = v
			}

			// Parse raw CLI dir args into DirSpec values
			var workdirSpec sandbox.DirSpec
			if rawWorkdirArg != "" {
				parsed, parseErr := sandbox.ParseDirArg(rawWorkdirArg)
				if parseErr != nil {
					return sandbox.NewUsageError("invalid workdir: %s", parseErr)
				}
				workdirSpec = sandbox.DirArgToSpec(parsed)
			}
			var auxDirSpecs []sandbox.DirSpec
			for _, rawDir := range rawDirs {
				parsed, parseErr := sandbox.ParseDirArg(rawDir)
				if parseErr != nil {
					return sandbox.NewUsageError("invalid directory %q: %s", rawDir, parseErr)
				}
				auxDirSpecs = append(auxDirSpecs, sandbox.DirArgToSpec(parsed))
			}

			// Resolve network mode
			networkMode := sandbox.NetworkModeDefault
			if networkNone {
				networkMode = sandbox.NetworkModeNone
			} else if networkIsolated {
				networkMode = sandbox.NetworkModeIsolated
			}

			// Set terminal title early so it shows the sandbox name during create
			if attach && !noStart {
				setTerminalTitle(name)
				defer setTerminalTitle("")
			}

			backend := resolveBackend(cmd)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgrOutput := cmd.ErrOrStderr()
				if jsonEnabled(cmd) {
					mgrOutput = io.Discard
				}
				mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), mgrOutput)
				sandboxName, err := mgr.Create(ctx, sandbox.CreateOptions{
					Name:         name,
					Workdir:      workdirSpec,
					AuxDirs:      auxDirSpecs,
					Agent:        agentName,
					Model:        model,
					Profile:      profileFlag,
					Prompt:       prompt,
					PromptFile:   promptFile,
					Network:      networkMode,
					NetworkAllow: networkAllow,
					Ports:        ports,
					Replace:      replace,
					Force:        force,
					NoStart:      noStart,
					Attach:       attach,
					Yes:          yes,
					Passthrough:  passthrough,
					Version:      version,
					Debug:        debug,
					CPUs:         cpus,
					Memory:       memory,
					Env:          envMap,
				})
				if err != nil {
					return err
				}

				if jsonEnabled(cmd) {
					if sandboxName == "" {
						return nil
					}
					meta, loadErr := sandbox.LoadMeta(sandbox.Dir(sandboxName))
					if loadErr != nil {
						return loadErr
					}
					return writeJSON(cmd.OutOrStdout(), meta)
				}

				if sandboxName == "" || !attach || noStart {
					return nil
				}

				// Wait for tmux session to be ready before attaching
				containerName := sandbox.InstanceName(sandboxName)
				if err := waitForTmux(ctx, rt, containerName, 30*time.Second); err != nil {
					return fmt.Errorf("waiting for tmux session: %w", err)
				}

				return attachToSandbox(ctx, rt, containerName, sandboxName)
			})
		},
	}

	cmd.Flags().StringP("prompt", "p", "", "Prompt text for the agent")
	cmd.Flags().StringP("prompt-file", "f", "", "File containing the prompt")
	cmd.Flags().StringP("model", "m", "", "Model name or alias")
	cmd.Flags().String("agent", "", "Agent to use (default from config or claude)")
	cmd.Flags().String("profile", "", "Profile to use (from ~/.yoloai/profiles/)")
	cmd.Flags().Bool("no-profile", false, "Use base image even if config sets a default profile")
	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().Bool("network-none", false, "Disable network access")
	cmd.Flags().Bool("network-isolated", false, "Allow only agent API traffic (iptables allowlist)")
	cmd.Flags().StringSlice("network-allow", nil, "Extra domain to allow when network-isolated (repeatable, implies --network-isolated)")
	cmd.Flags().StringSlice("port", nil, "Port mapping (host:container)")
	cmd.Flags().StringSliceP("dir", "d", nil, "Auxiliary directory (repeatable, default read-only)")
	cmd.Flags().Bool("replace", false, "Replace existing sandbox with same name")
	cmd.Flags().Bool("force", false, "Replace even if unapplied changes exist")
	cmd.Flags().Bool("no-start", false, "Create but don't start the container")
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after creation")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmations")
	cmd.Flags().Bool("debug", false, "Enable debug logging in sandbox entrypoint")
	cmd.Flags().String("cpus", "", "CPU limit (e.g., 4, 2.5)")
	cmd.Flags().String("memory", "", "Memory limit (e.g., 8g, 512m)")
	cmd.Flags().StringSlice("env", nil, "Environment variable (KEY=VAL, repeatable)")

	cmd.MarkFlagsMutuallyExclusive("network-none", "network-isolated")
	cmd.MarkFlagsMutuallyExclusive("profile", "no-profile")
	cmd.MarkFlagsMutuallyExclusive("no-start", "attach")

	return cmd
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate shell completion script for the specified shell.

To load completions:

Bash:
  source <(yoloai system completion bash)

Zsh:
  source <(yoloai system completion zsh)

Fish:
  yoloai system completion fish | source

PowerShell:
  yoloai system completion powershell | Out-String | Invoke-Expression`,
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

// setTerminalTitle sets the terminal title for the host terminal.
// It emits an OSC 0 escape sequence (works for non-tmux terminals) and,
// if running inside a host tmux session, also renames the tmux window
// so the title shows in the tmux status bar.
// When title is empty, it restores the previous state (clears OSC title
// and unsets per-window tmux overrides to revert to user defaults).
func setTerminalTitle(title string) {
	fmt.Fprintf(os.Stdout, "\033]0;%s\007", title) //nolint:errcheck // best-effort terminal title

	// If inside a host tmux session, also set the window name.
	if os.Getenv("TMUX") == "" {
		return
	}
	if title != "" {
		// Disable automatic-rename (tmux tracking the foreground process name)
		// and allow-rename (programs sending escape sequences to rename the
		// window) so our title sticks while the sandbox is attached.
		exec.Command("tmux", "set-option", "-w", "automatic-rename", "off").Run() //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "set-option", "-w", "allow-rename", "off").Run()     //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "rename-window", title).Run()                        //nolint:errcheck,gosec // best-effort
	} else {
		// Unset per-window overrides so the window reverts to the user's
		// session/global defaults after detach.
		exec.Command("tmux", "set-option", "-wu", "automatic-rename").Run() //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "set-option", "-wu", "allow-rename").Run()     //nolint:errcheck,gosec // best-effort
	}
}

// attachToSandbox attaches to the tmux session in a running container.
// It sets the terminal title to the sandbox name and restores it on detach.
func attachToSandbox(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string) error {
	setTerminalTitle(sandboxName)
	defer setTerminalTitle("")
	return rt.InteractiveExec(ctx, containerName, []string{"tmux", "attach", "-t", "main"}, "yoloai", "")
}

func newVersionCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version information",
		GroupID: groupAdmin,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
					"version": version,
					"commit":  commit,
					"date":    date,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "yoloai version %s (commit: %s, built: %s)\n", version, commit, date)
			return err
		},
	}
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	goruntime "runtime"
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
		newMCPCmd(),

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
	addLogFlags(cmd)
	return cmd
}

func addLogFlags(cmd *cobra.Command) {
	cmd.Flags().String("source", "", "comma-separated sources: cli,sandbox,monitor,hooks")
	cmd.Flags().String("level", "info", "minimum log level: debug|info|warn|error")
	cmd.Flags().String("since", "", "show entries since duration (5m) or local time (14:20:00)")
	cmd.Flags().Bool("raw", false, "emit raw JSONL (no formatting)")
	cmd.Flags().Bool("agent", false, "show agent output (ANSI stripped)")
	cmd.Flags().Bool("agent-raw", false, "show raw agent terminal stream")
	cmd.Flags().BoolP("follow", "f", false, "tail log live; auto-exits when sandbox is done")
	cmd.MarkFlagsMutuallyExclusive("agent", "agent-raw")
	cmd.MarkFlagsMutuallyExclusive("agent", "raw")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "raw")
	cmd.MarkFlagsMutuallyExclusive("agent", "source")
	cmd.MarkFlagsMutuallyExclusive("agent", "level")
	cmd.MarkFlagsMutuallyExclusive("agent", "since")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "source")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "level")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "since")
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
			isolation, _ := cmd.Flags().GetString("isolation")
			targetOS, _ := cmd.Flags().GetString("os")
			debug, _ := cmd.Flags().GetBool("debug")
			envSlice, _ := cmd.Flags().GetStringSlice("env")

			// Block unsupported isolation+os combinations early.
			if goruntime.GOOS == "darwin" && targetOS != "mac" && (isolation == "vm" || isolation == "vm-enhanced") {
				return sandbox.NewUsageError(
					"--isolation %s requires containerd, which is not available on macOS.\n"+
						"Use a Linux host for VM isolation, or use --os mac for macOS-native sandboxing:\n"+
						"  container   macOS sandbox-exec (seatbelt)\n"+
						"  vm          Full macOS VM (Tart)", isolation)
			}
			if targetOS == "mac" && (isolation == "container-enhanced" || isolation == "vm-enhanced") {
				return sandbox.NewUsageError(
					"--isolation %s is not available with --os mac.\n"+
						"Available isolation modes with --os mac:\n"+
						"  container   macOS sandbox-exec (seatbelt)\n"+
						"  vm          Full macOS VM (Tart)", isolation)
			}
			// Block container-enhanced (gVisor) on macOS due to known bug.
			if isolation == "container-enhanced" && targetOS != "mac" && goruntime.GOOS == "darwin" {
				return sandbox.NewUsageError(
					"--isolation container-enhanced (gVisor) is not supported on macOS due to a bug\n" +
						"that causes Claude Code to hang indefinitely during initialization.\n\n" +
						"Workaround: Omit --isolation (use default container isolation) or use\n" +
						"--os mac for lightweight macOS sandboxing.\n\n" +
						"For details, see: https://github.com/anthropics/claude-code/issues/35454")
			}

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
					Isolation:    isolation,
					Env:          envMap,
				})
				if err != nil {
					return err
				}

				// Register sandbox name so --bugreport can include sandbox sections
				// if a subsequent step (e.g. waitForTmux) fails.
				if sandboxName != "" && bugReportFile != nil {
					bugReportSandboxName = sandboxName
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

				// Load meta to determine correct tmux exec user
				meta, loadErr := sandbox.LoadMeta(sandbox.Dir(sandboxName))
				if loadErr != nil {
					return loadErr
				}
				user := tmuxExecUser(meta)

				// Wait for tmux session to be ready before attaching
				containerName := sandbox.InstanceName(sandboxName)
				if err := waitForTmux(ctx, rt, containerName, sandboxName, 30*time.Second, user); err != nil {
					return fmt.Errorf("waiting for tmux session: %w", err)
				}

				return attachToSandbox(ctx, rt, containerName, sandboxName, user)
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
	cmd.Flags().String("cpus", "", "CPU limit (e.g., 4, 2.5)")
	cmd.Flags().String("memory", "", "Memory limit (e.g., 8g, 512m)")
	cmd.Flags().String("isolation", "", "Isolation mode: container (default), container-enhanced (gVisor), vm (Kata+QEMU), vm-enhanced (Kata+Firecracker)")
	cmd.Flags().String("os", "", "Target OS: linux (default), mac")
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

// tmuxExecUser returns the user to use for tmux exec operations.
// Delegates to sandbox.ContainerUser which handles all cases:
//   - Podman --userns=keep-id: empty (use container default)
//   - gVisor: numeric host UID (gVisor resolves usernames from OCI manifest, not /etc/passwd)
//   - default: "yoloai"
func tmuxExecUser(meta *sandbox.Meta) string {
	return sandbox.ContainerUser(meta)
}

// readTmuxSocket returns the tmux socket path configured for a sandbox, or
// empty string if not set (backend does not use a custom socket).
func readTmuxSocket(sandboxName string) string {
	data, err := os.ReadFile(sandbox.RuntimeConfigFilePath(sandboxName)) //nolint:gosec // G304: path from trusted sandbox dir
	if err != nil {
		return ""
	}
	var cfg struct {
		TmuxSocket string `json:"tmux_socket"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.TmuxSocket
}

// waitForTmux polls until the tmux session is ready in the container.
// Returns early if the container stops running or the context is cancelled.
//
// Detection strategy (both are checked on each poll cycle):
//  1. sandbox.jsonl: read the container's structured log on the host and look
//     for the "sandbox.tmux_start" event. This is the primary check and works
//     even when docker exec is unreliable (e.g. gVisor on ARM64 where exec
//     into the container may behave differently).
//  2. docker exec: run "tmux has-session -t main" inside the container.
//     This is the fallback and covers backends that don't write sandbox.jsonl.
func waitForTmux(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string, timeout time.Duration, user string) error {
	jsonlPath := sandbox.SandboxJSONLPath(sandboxName)
	tmuxSocket := readTmuxSocket(sandboxName)
	deadline := time.Now().Add(timeout)
	var lastExecErr error
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

		// Primary: check sandbox.jsonl for the "sandbox.tmux_start" event.
		// The container writes this immediately after tmux new-session succeeds,
		// so it's visible to the host without requiring docker exec.
		if data, readErr := os.ReadFile(jsonlPath); readErr == nil { //nolint:gosec // G304: path from trusted sandbox dir
			if bytes.Contains(data, []byte(`"sandbox.tmux_start"`)) {
				return nil
			}
		}

		// Fallback: docker exec tmux has-session (unreliable under gVisor).
		tmuxArgs := []string{"tmux"}
		if tmuxSocket != "" {
			tmuxArgs = append(tmuxArgs, "-S", tmuxSocket)
		}
		tmuxArgs = append(tmuxArgs, "has-session", "-t", "main")
		_, err = rt.Exec(ctx, containerName, tmuxArgs, user)
		if err == nil {
			return nil
		}
		lastExecErr = err
		slog.Debug("waitForTmux: exec check failed", "event", "sandbox.wait_tmux.exec_fail", //nolint:gosec // G706: slog uses structured logging, not vulnerable to log injection
			"container", containerName, "error", err)

		// Context-aware sleep
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	slog.Debug("waitForTmux: timed out", "event", "sandbox.wait_tmux.timeout", //nolint:gosec // G706: slog uses structured logging, not vulnerable to log injection
		"container", containerName, "last_exec_err", lastExecErr)
	// Include container logs in the error to surface setup failures.
	if logs := rt.Logs(ctx, containerName, 50); logs != "" {
		return fmt.Errorf("tmux session not ready after %s\n\nContainer logs:\n%s", timeout, logs)
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
//
// PTY/terminal behaviour varies by backend and architecture:
//
//   - tart/seatbelt: run commands directly with the caller's terminal already
//     attached. No script wrapper needed or wanted — macOS BSD script does not
//     support the GNU -c flag used by the Linux wrapper.
//
//   - Standard Docker (all arch): docker exec -it calls TIOCSCTTY, so the
//     exec'd process gets a controlling terminal. The script wrapper creates a
//     fresh PTY + controlling terminal, which tmux uses cleanly.
//
//   - gVisor on ARM64: docker exec -it does NOT call TIOCSCTTY. The exec'd
//     process has no controlling terminal, so /dev/tty returns EACCES (gVisor
//     denies it). tmux falls back to stdin only when errno is ENXIO, not
//     EACCES. Fix: setsid creates a new session with no CTY, /dev/tty returns
//     ENXIO, tmux's ENXIO fallback activates and uses stdin (the PTY).
//
//   - gVisor on amd64/other: docker exec -it DOES call TIOCSCTTY (same as
//     standard Docker). setsid would strip the CTY and tmux exits immediately.
//     The script wrapper works correctly, same as standard Docker.
func attachToSandbox(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string, user string) error {
	setTerminalTitle(sandboxName)
	defer setTerminalTitle("")

	// Load metadata to check security mode
	meta, err := sandbox.LoadMeta(sandbox.Dir(sandboxName))
	if err != nil {
		return fmt.Errorf("load sandbox metadata: %w", err)
	}

	// Build tmux attach command
	var cmd []string
	sock := readTmuxSocket(sandboxName)

	switch {
	case meta.Isolation == "container-enhanced" && goruntime.GOARCH == "arm64":
		// gVisor on ARM64 requires setsid to work around missing TIOCSCTTY.
		cmd = []string{"setsid", "tmux"}
		if sock != "" {
			cmd = append(cmd, "-S", sock)
		}
		cmd = append(cmd, "attach", "-t", "main")
	case rt.Name() == "tart" || rt.Name() == "seatbelt":
		// tart/seatbelt run commands directly with the caller's terminal;
		// no script wrapper needed (and macOS BSD script doesn't support -c).
		cmd = []string{"tmux"}
		if sock != "" {
			cmd = append(cmd, "-S", sock)
		}
		cmd = append(cmd, "attach", "-t", "main")
	default:
		// Container backends (docker, podman, containerd): use script to
		// create a fresh PTY + controlling terminal.
		// script -q -e -c <cmd> /dev/null: quiet, propagate exit status, run cmd,
		// discard transcript.
		var tmuxArgs string
		if sock != "" {
			tmuxArgs = fmt.Sprintf("exec tmux -S %s attach -t main", sock)
		} else {
			tmuxArgs = "exec tmux attach -t main"
		}
		cmd = []string{"script", "-q", "-e", "-c", tmuxArgs, "/dev/null"}
	}

	return rt.InteractiveExec(ctx, containerName, cmd, user, "")
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

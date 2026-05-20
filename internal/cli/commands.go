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

	"github.com/creack/pty"
	"github.com/kstenerud/yoloai/config"
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
		newBaselineCmd(),
		newFilesCmd(),
		newXCmd(),

		// Sandbox Tools
		newSandboxCmd(),
		newLsAliasCmd(),
		newLogAliasCmd(),
		newExecAliasCmd(),
		newVscodeAliasCmd(),

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

func newVscodeAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "vscode <name>",
		Short:   "Open a sandbox in VS Code (shortcut for 'sandbox vscode')",
		GroupID: groupSandboxTools,
		Args:    cobra.ExactArgs(1),
		RunE:    newSandboxVscodeCmd().RunE,
	}
}

func newNewCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "new [flags] <name> [workdir] [-d <dir>...] [-- <agent-args>...]",
		Short:   "Create and start a sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewCmd(cmd, args, version)
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
	cmd.Flags().String("isolation", "", "Isolation mode: container (default), container-enhanced (gVisor), container-privileged (--privileged, use for Docker-in-Docker), vm (Kata+QEMU), vm-enhanced (Kata+Firecracker)")
	cmd.Flags().String("os", "", "Target OS: linux (default), mac")
	cmd.Flags().StringSlice("env", nil, "Environment variable (KEY=VAL, repeatable)")
	cmd.Flags().StringArray("runtime", []string{}, "Apple simulator runtime (ios, tvos, watchos, visionos). Repeatable. Example: --runtime ios --runtime tvos:26.1")
	cmd.Flags().Bool("vscode-tunnel", false, "Launch a VS Code Remote Tunnel alongside the agent (connect from VS Code on any machine)")
	cmd.Flags().String("archetype", "", fmt.Sprintf("Environment archetype (%s)", strings.Join(sandbox.ValidArchetypes(), "|")))

	cmd.MarkFlagsMutuallyExclusive("network-none", "network-isolated")
	cmd.MarkFlagsMutuallyExclusive("profile", "no-profile")
	cmd.MarkFlagsMutuallyExclusive("no-start", "attach")

	return cmd
}

func runNewCmd(cmd *cobra.Command, args []string, version string) error {
	name, rawWorkdirArg, passthrough, profileFlag, err := parseNewCmdPositional(cmd, args)
	if err != nil {
		return err
	}

	opts, err := resolveNewCmdOptions(cmd, version, name, rawWorkdirArg, passthrough, profileFlag)
	if err != nil {
		return err
	}

	if opts.Attach && !opts.NoStart {
		setTerminalTitle(name)
		defer setTerminalTitle("")
	}

	backend := resolveBackend(cmd)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		return executeNewCreate(cmd, ctx, rt, opts)
	})
}

// parseNewCmdPositional validates and splits positional args for the new command.
func parseNewCmdPositional(cmd *cobra.Command, args []string) (name, rawWorkdirArg string, passthrough []string, profileFlag string, err error) {
	dashIdx := cmd.ArgsLenAtDash()
	var positional []string
	if dashIdx < 0 {
		positional = args
	} else {
		positional = args[:dashIdx]
		passthrough = args[dashIdx:]
	}

	profileFlag = resolveProfile(cmd)

	if len(positional) < 1 {
		return "", "", nil, "", sandbox.NewUsageError("sandbox name is required")
	}
	if len(positional) < 2 && profileFlag == "" {
		return "", "", nil, "", sandbox.NewUsageError("workdir is required (or use --profile)\n\nUsage: yoloai new [flags] <name> <workdir> [-- <agent-args>...]\n\nExample: yoloai new %s .", positional[0])
	}
	if len(positional) > 2 {
		return "", "", nil, "", sandbox.NewUsageError("too many positional arguments (expected <name> [workdir])")
	}

	name = positional[0]
	if len(positional) >= 2 {
		rawWorkdirArg = positional[1]
	}
	return name, rawWorkdirArg, passthrough, profileFlag, nil
}

// resolveNewCmdOptions reads all flags and builds the sandbox.CreateOptions.
func resolveNewCmdOptions(cmd *cobra.Command, version, name, rawWorkdirArg string, passthrough []string, profileFlag string) (sandbox.CreateOptions, error) {
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	model := resolveModel(cmd)
	agentName := resolveAgent(cmd)
	networkNone, _ := cmd.Flags().GetBool("network-none")
	networkIsolated, _ := cmd.Flags().GetBool("network-isolated")
	networkAllow, _ := cmd.Flags().GetStringSlice("network-allow")
	ports, _ := cmd.Flags().GetStringSlice("port")
	rawDirs, _ := cmd.Flags().GetStringSlice("dir")

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
		return sandbox.CreateOptions{}, sandbox.NewUsageError("--json and --attach are incompatible")
	}
	if networkNone && len(ports) > 0 {
		return sandbox.CreateOptions{}, sandbox.NewUsageError("--port is incompatible with --network-none")
	}

	cpus, _ := cmd.Flags().GetString("cpus")
	memory, _ := cmd.Flags().GetString("memory")
	debug, _ := cmd.Flags().GetBool("debug")
	envSlice, _ := cmd.Flags().GetStringSlice("env")
	runtimes, _ := cmd.Flags().GetStringArray("runtime")
	vscodeTunnel, _ := cmd.Flags().GetBool("vscode-tunnel")
	archetypeFlag, _ := cmd.Flags().GetString("archetype")

	isolation, _, err := resolveNewIsolationOS(cmd)
	if err != nil {
		return sandbox.CreateOptions{}, err
	}

	envMap, err := parseEnvSlice(envSlice)
	if err != nil {
		return sandbox.CreateOptions{}, err
	}

	workdirSpec, auxDirSpecs, err := resolveNewDirSpecs(rawWorkdirArg, rawDirs)
	if err != nil {
		return sandbox.CreateOptions{}, err
	}

	networkMode := sandbox.NetworkModeDefault
	if networkNone {
		networkMode = sandbox.NetworkModeNone
	} else if networkIsolated {
		networkMode = sandbox.NetworkModeIsolated
	}

	return sandbox.CreateOptions{
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
		Yes:          effectiveYes(cmd),
		Passthrough:  passthrough,
		Version:      version,
		Debug:        debug,
		CPUs:         cpus,
		Memory:       memory,
		Isolation:    isolation,
		Env:          envMap,
		Runtimes:     runtimes,
		VscodeTunnel: vscodeTunnel,
		Archetype:    archetypeFlag,
	}, nil
}

// parseEnvSlice parses KEY=VAL env flag values into a map.
func parseEnvSlice(envSlice []string) (map[string]string, error) {
	envMap := make(map[string]string, len(envSlice))
	for _, e := range envSlice {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return nil, sandbox.NewUsageError("invalid --env value %q: must be KEY=VAL", e)
		}
		envMap[k] = v
	}
	return envMap, nil
}

// resolveNewDirSpecs parses rawWorkdirArg and rawDirs into DirSpec values.
func resolveNewDirSpecs(rawWorkdirArg string, rawDirs []string) (workdirSpec sandbox.DirSpec, auxDirSpecs []sandbox.DirSpec, err error) {
	if rawWorkdirArg != "" {
		parsed, parseErr := sandbox.ParseDirArg(rawWorkdirArg)
		if parseErr != nil {
			return sandbox.DirSpec{}, nil, sandbox.NewUsageError("invalid workdir: %s", parseErr)
		}
		workdirSpec = sandbox.DirArgToSpec(parsed)
	}
	for _, rawDir := range rawDirs {
		parsed, parseErr := sandbox.ParseDirArg(rawDir)
		if parseErr != nil {
			return sandbox.DirSpec{}, nil, sandbox.NewUsageError("invalid directory %q: %s", rawDir, parseErr)
		}
		auxDirSpecs = append(auxDirSpecs, sandbox.DirArgToSpec(parsed))
	}
	return workdirSpec, auxDirSpecs, nil
}

// executeNewCreate performs the actual sandbox creation and optional attach inside withRuntime.
func executeNewCreate(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, opts sandbox.CreateOptions) error {
	mgrOutput := cmd.ErrOrStderr()
	if jsonEnabled(cmd) {
		mgrOutput = io.Discard
	}
	mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), mgrOutput)
	sandboxName, err := mgr.Create(ctx, opts)
	if err != nil {
		return err
	}

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

	if sandboxName == "" || !opts.Attach || opts.NoStart {
		return nil
	}

	meta, loadErr := sandbox.LoadMeta(sandbox.Dir(sandboxName))
	if loadErr != nil {
		return loadErr
	}
	user := tmuxExecUser(meta)
	containerName := sandbox.InstanceName(sandboxName)
	if err := waitForTmux(ctx, rt, containerName, sandboxName, 300*time.Second, user); err != nil {
		return fmt.Errorf("waiting for tmux session: %w", err)
	}
	return attachToSandbox(ctx, rt, containerName, sandboxName, user)
}

// resolveNewIsolationOS resolves the --isolation and --os flags with config fallback
// and validates their combinations, returning an error for unsupported combos.
func resolveNewIsolationOS(cmd *cobra.Command) (isolation, targetOS string, err error) {
	cfg, _ := config.LoadDefaultsConfig()
	var cfgIsolation, cfgOS string
	if cfg != nil {
		cfgIsolation = cfg.Isolation
		cfgOS = cfg.OS
	}
	isolation = coalesce(flagStr(cmd, "isolation"), cfgIsolation)
	targetOS = coalesce(flagStr(cmd, "os"), cfgOS)

	if err := validateIsolationOSCombo(isolation, targetOS); err != nil {
		return "", "", err
	}
	return isolation, targetOS, nil
}

// validateIsolationOSCombo returns an error for unsupported isolation+OS combinations.
func validateIsolationOSCombo(isolation, targetOS string) error {
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
	if isolation == "container-enhanced" && targetOS != "mac" && goruntime.GOOS == "darwin" {
		return sandbox.NewUsageError(
			"--isolation container-enhanced (gVisor) is not supported on macOS due to a bug\n" +
				"that causes Claude Code to hang indefinitely during initialization.\n\n" +
				"Workaround: Omit --isolation (use default container isolation) or use\n" +
				"--os mac for lightweight macOS sandboxing.\n\n" +
				"For details, see: https://github.com/anthropics/claude-code/issues/35454")
	}
	if isolation == "container-privileged" && goruntime.GOOS == "darwin" {
		return sandbox.NewUsageError(
			"--isolation %s is Linux-only (Docker or Podman required).\n"+
				"macOS backends (Seatbelt, Tart) do not support this mode.\n"+
				"Use a Linux host or omit --isolation for the default mode.", isolation)
	}
	if isolation == "container-privileged" && targetOS == "mac" {
		return sandbox.NewUsageError(
			"--isolation %s is not available with --os mac.\n"+
				"Available isolation modes with --os mac:\n"+
				"  container   macOS sandbox-exec (seatbelt)\n"+
				"  vm          Full macOS VM (Tart)", isolation)
	}
	return nil
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

// waitForTmux polls until the agent has been launched inside the container.
// Returns early if the container stops running or the context is cancelled.
//
// Detection strategy (both are checked on each poll cycle):
//  1. sandbox.jsonl: read the container's structured log on the host and look
//     for the "sandbox.agent_launch" event (agent started) or
//     "sandbox.agent_not_found" (binary missing — still need to attach to show
//     the error). This is the primary check and works even when docker exec is
//     unreliable (e.g. gVisor on ARM64). Checking agent_launch rather than
//     tmux_start ensures we wait for lifecycle commands (onCreateCommand,
//     postStartCommand) to complete before attaching.
//  2. docker exec: run "tmux has-session -t main" inside the container.
//     This is the fallback for backends that don't write sandbox.jsonl.
func waitForTmux(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string, timeout time.Duration, user string) error {
	jsonlPath := sandbox.SandboxJSONLPath(sandboxName)
	tmuxSocket := readTmuxSocket(sandboxName)
	deadline := time.Now().Add(timeout)
	var lastExecErr error
	for time.Now().Before(deadline) {
		ready, err := pollTmuxReady(ctx, rt, containerName, jsonlPath, tmuxSocket, user)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		lastExecErr = err
		if err := sleepOrCancel(ctx, 500*time.Millisecond); err != nil {
			return err
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

// pollTmuxReady performs one readiness check cycle. Returns (true, nil) when ready,
// (false, nil) to keep polling, or (false, err) on a hard error.
func pollTmuxReady(ctx context.Context, rt runtime.Runtime, containerName, jsonlPath, tmuxSocket, user string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	info, err := rt.Inspect(ctx, containerName)
	if err != nil || !info.Running {
		return false, fmt.Errorf("container %s is not running", containerName)
	}

	// Primary: check sandbox.jsonl for agent_launch or agent_not_found.
	// agent_launch is written by launch_agent() after lifecycle commands
	// finish, so attaching at this point means the user sees the agent
	// (or the "not found" error) immediately.
	// When sandbox.jsonl is readable, skip the exec fallback: the tmux
	// session is created before lifecycle commands run, so has-session
	// succeeds long before the agent is actually launched.
	if data, readErr := os.ReadFile(jsonlPath); readErr == nil { //nolint:gosec // G304: path from trusted sandbox dir
		if bytes.Contains(data, []byte(`"sandbox.agent_launch"`)) ||
			bytes.Contains(data, []byte(`"sandbox.agent_not_found"`)) {
			return true, nil
		}
		// Log exists but agent not yet launched — signal keep polling.
		return false, nil
	}

	// Fallback: docker exec tmux has-session.
	// Only reached when sandbox.jsonl is unreadable (old container images
	// that predate structured logging). The tmux session existing is
	// sufficient in that case because those images launch the agent
	// synchronously before writing any log.
	tmuxArgs := buildTmuxHasSessionArgs(tmuxSocket)
	_, execErr := rt.Exec(ctx, containerName, tmuxArgs, user)
	if execErr == nil {
		return true, nil
	}
	slog.Debug("waitForTmux: exec check failed", "event", "sandbox.wait_tmux.exec_fail", //nolint:gosec // G706: slog uses structured logging, not vulnerable to log injection
		"container", containerName, "err", execErr)
	return false, nil
}

// buildTmuxHasSessionArgs constructs the tmux has-session argument list.
func buildTmuxHasSessionArgs(tmuxSocket string) []string {
	args := []string{"tmux"}
	if tmuxSocket != "" {
		args = append(args, "-S", tmuxSocket)
	}
	return append(args, "has-session", "-t", "main")
}

// sleepOrCancel waits for the given duration or returns ctx.Err() if cancelled.
func sleepOrCancel(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
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
// The backend-specific attach command is built by rt.AttachCommand, which
// knows the correct PTY and terminal strategies for each runtime.
func attachToSandbox(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string, user string) error {
	setTerminalTitle(sandboxName)
	defer setTerminalTitle("")

	meta, err := sandbox.LoadMeta(sandbox.Dir(sandboxName))
	if err != nil {
		return fmt.Errorf("load sandbox metadata: %w", err)
	}

	sock := readTmuxSocket(sandboxName)
	// pty.Getsize returns (rows, cols, err) — named accordingly.
	rows, cols, _ := pty.Getsize(os.Stdin)
	cmd := rt.AttachCommand(sock, rows, cols, meta.Isolation)

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

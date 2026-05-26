// ABOUTME: 'new' command — create and start a sandbox in one step. Wires CLI
// ABOUTME: flags to sandbox.CreateOptions, validates isolation/OS combos, and
// ABOUTME: handles the optional auto-attach after creation.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/archetype"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

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
	cmd.Flags().String("archetype", "", fmt.Sprintf("Environment archetype (%s)", strings.Join(archetype.ValidArchetypes(), "|")))

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
		meta, loadErr := store.LoadMeta(cliLayout().SandboxDir(sandboxName))
		if loadErr != nil {
			return loadErr
		}
		return writeJSON(cmd.OutOrStdout(), meta)
	}

	if sandboxName == "" || !opts.Attach || opts.NoStart {
		return nil
	}

	meta, loadErr := store.LoadMeta(cliLayout().SandboxDir(sandboxName))
	if loadErr != nil {
		return loadErr
	}
	user := tmuxExecUser(meta)
	containerName := store.InstanceName(sandboxName)
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

// validateIsolationOSCombo returns an error for unsupported isolation+OS
// combinations. Thin wrapper over runtime.IsolationAvailability: the runtime
// package owns the rules and their messages, the CLI just turns the verdict
// into a UsageError.
func validateIsolationOSCombo(isolation, targetOS string) error {
	available, reason, help := runtime.IsolationAvailability(isolation, targetOS, goruntime.GOOS)
	if available {
		return nil
	}
	if help != "" {
		return sandbox.NewUsageError("%s\n%s", reason, help)
	}
	return sandbox.NewUsageError("%s", reason)
}

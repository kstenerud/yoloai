// ABOUTME: 'new' command — create and start a sandbox in one step. Wires CLI
// ABOUTME: flags to yoloai.SandboxCreateOptions, validates isolation/OS combos, refuses
// ABOUTME: a dirty workdir unless --allow-dirty, and handles optional auto-attach after creation.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

func NewNewCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "new [flags] <name> [workdir] [-d <dir>...] [-- <agent-args>...]",
		Short:   "Create and start a sandbox",
		GroupID: cliutil.GroupLifecycle,
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
	cmd.Flags().Bool("abandon-unapplied", false, "Replace even when the existing sandbox has unapplied changes (implies --replace)")
	cmd.Flags().Bool("no-start", false, "Create but don't start the container")
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after creation")
	cmd.Flags().Bool("allow-dirty", false, "Proceed even if the workdir has uncommitted changes (they will be visible to the agent)")
	cmd.Flags().String("cpus", "", "CPU limit (e.g., 4, 2.5)")
	cmd.Flags().String("memory", "", "Memory limit (e.g., 8g, 512m)")
	cmd.Flags().String("isolation", "", "Isolation mode: container (default), container-enhanced (gVisor), container-privileged (--privileged, use for Docker-in-Docker), vm (Kata+QEMU), vm-enhanced (Kata+Firecracker)")
	cmd.Flags().String("os", "", "Target OS: linux (default), mac")
	cmd.Flags().StringSlice("env", nil, "Environment variable (KEY=VAL, repeatable)")
	cmd.Flags().StringArray("runtime", []string{}, "Apple simulator runtime (ios, tvos, watchos, visionos). Repeatable. Example: --runtime ios --runtime tvos:26.1")
	cmd.Flags().Bool("vscode-tunnel", false, "Launch a VS Code Remote Tunnel alongside the agent (connect from VS Code on any machine)")
	cmd.Flags().String("archetype", "", fmt.Sprintf("Environment archetype (%s)", strings.Join(yoloai.Archetypes(), "|")))

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

	opts, attach, noStart, err := resolveNewCmdOptions(cmd, name, rawWorkdirArg, passthrough, profileFlag)
	if err != nil {
		return err
	}

	// Courtesy free-space check before allocating ~hundreds of MB
	// (workdir copy + overlay) and possibly fetching a multi-GB base
	// image. Stat errors are swallowed; the warning is non-blocking.
	if !cliutil.JSONEnabled(cmd) {
		cliutil.WarnIfLowDisk(cmd.ErrOrStderr(), cliutil.Layout().SandboxesDir())
	}

	if attach && !noStart {
		cliutil.SetTerminalTitle(name)
		defer cliutil.SetTerminalTitle("")
	}

	backend := cliutil.ResolveBackend(cmd)

	// new.go's one quirk vs other Client-using commands: in JSON mode we
	// want the Engine's progress output suppressed so it doesn't pollute
	// the JSON document on stdout. WithClient hardcodes cmd.ErrOrStderr,
	// so we construct the Client by hand here to override Output.
	mgrOutput := cmd.ErrOrStderr()
	if cliutil.JSONEnabled(cmd) {
		mgrOutput = io.Discard
	}
	l := cliutil.Layout()
	c, err := yoloai.NewClient(cmd.Context(), yoloai.ClientCreateOptions{
		DataDir:     l.DataDir,
		HomeDir:     l.HomeDir,
		BackendType: yoloai.BackendType(backend),
		Input:       cmd.InOrStdin(),
		Output:      mgrOutput,
		Version:     version,
		Env:         cliutil.EdgeEnv(),
	})
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	return executeNewCreate(cmd, cmd.Context(), c, opts, attach, noStart)
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

	profileFlag = cliutil.ResolveProfile(cmd)

	if len(positional) < 1 {
		return "", "", nil, "", yoerrors.NewUsageError("sandbox name is required")
	}
	if len(positional) < 2 && profileFlag == "" {
		return "", "", nil, "", yoerrors.NewUsageError("workdir is required (or use --profile)\n\nUsage: yoloai new [flags] <name> <workdir> [-- <agent-args>...]\n\nExample: yoloai new %s .", positional[0])
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

// resolveNewCmdOptions reads all flags and builds the public yoloai.SandboxCreateOptions.
// attach and noStart are returned separately — they gate the post-create handoff
// (start + attach), not creation itself (Create only provisions).
func resolveNewCmdOptions(cmd *cobra.Command, name, rawWorkdirArg string, passthrough []string, profileFlag string) (yoloai.SandboxCreateOptions, bool, bool, error) {
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	model := cliutil.ResolveModel(cmd)
	agentName := cliutil.ResolveAgent(cmd)
	networkNone, _ := cmd.Flags().GetBool("network-none")
	networkIsolated, _ := cmd.Flags().GetBool("network-isolated")
	networkAllow, _ := cmd.Flags().GetStringSlice("network-allow")
	rawPorts, _ := cmd.Flags().GetStringSlice("port")
	rawDirs, _ := cmd.Flags().GetStringSlice("dir")

	if len(networkAllow) > 0 {
		networkIsolated = true
	}

	replace, _ := cmd.Flags().GetBool("replace")
	abandonUnapplied, _ := cmd.Flags().GetBool("abandon-unapplied")
	if abandonUnapplied {
		replace = true
	}
	noStart, _ := cmd.Flags().GetBool("no-start")
	attach, _ := cmd.Flags().GetBool("attach")

	if cliutil.JSONEnabled(cmd) && attach {
		return yoloai.SandboxCreateOptions{}, false, false, yoerrors.NewUsageError("--json and --attach are incompatible")
	}
	if networkNone && len(rawPorts) > 0 {
		return yoloai.SandboxCreateOptions{}, false, false, yoerrors.NewUsageError("--port is incompatible with --network-none")
	}

	ports, err := parsePortFlags(rawPorts)
	if err != nil {
		return yoloai.SandboxCreateOptions{}, false, false, err
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
		return yoloai.SandboxCreateOptions{}, false, false, err
	}

	envMap, err := parseEnvSlice(envSlice)
	if err != nil {
		return yoloai.SandboxCreateOptions{}, false, false, err
	}

	workdirSpec, auxDirSpecs, err := resolveNewDirSpecs(rawWorkdirArg, rawDirs)
	if err != nil {
		return yoloai.SandboxCreateOptions{}, false, false, err
	}

	networkMode := yoloai.NetworkModeDefault
	if networkNone {
		networkMode = yoloai.NetworkModeNone
	} else if networkIsolated {
		networkMode = yoloai.NetworkModeIsolated
	}

	return yoloai.SandboxCreateOptions{
		Name:                 name,
		Workdir:              workdirSpec,
		AuxDirs:              auxDirSpecs,
		AgentType:            yoloai.AgentType(agentName),
		Model:                model,
		Profile:              profileFlag,
		Prompt:               prompt,
		PromptFile:           promptFile,
		Network:              networkMode,
		NetworkAllow:         networkAllow,
		Ports:                ports,
		Replace:              replace,
		AbandonUnappliedWork: abandonUnapplied,
		Passthrough:          passthrough,
		Debug:                debug,
		CPUs:                 cpus,
		Memory:               memory,
		Isolation:            isolation,
		Env:                  envMap,
		Runtimes:             runtimes,
		VscodeTunnel:         vscodeTunnel,
		Archetype:            archetypeFlag,
		// A dirty workdir never auto-proceeds here. executeNewCreate surfaces the
		// warning and requires --allow-dirty to widen the scope — we never prompt
		// to widen it, so --yes (gone from this command) can't paper over it.
		AllowDirtyWorkdir: false,
	}, attach, noStart, nil
}

// parsePortFlags parses --port "host:container" strings into typed PortMappings
// at the CLI boundary (Q-Y: the public surface takes []PortMapping). Protocol
// is tcp — the only mode the backend pipeline supports today.
func parsePortFlags(rawPorts []string) ([]yoloai.PortMapping, error) {
	if len(rawPorts) == 0 {
		return nil, nil
	}
	ports := make([]yoloai.PortMapping, 0, len(rawPorts))
	for _, p := range rawPorts {
		host, container, ok := strings.Cut(p, ":")
		if !ok {
			return nil, yoerrors.NewUsageError("invalid port format %q (expected host:container)", p)
		}
		hostPort, err := strconv.Atoi(host)
		if err != nil {
			return nil, yoerrors.NewUsageError("invalid host port %q in mapping %q", host, p)
		}
		containerPort, err := strconv.Atoi(container)
		if err != nil {
			return nil, yoerrors.NewUsageError("invalid container port %q in mapping %q", container, p)
		}
		ports = append(ports, yoloai.PortMapping{HostPort: hostPort, ContainerPort: containerPort, Protocol: "tcp"})
	}
	return ports, nil
}

// parseEnvSlice parses KEY=VAL env flag values into a map.
func parseEnvSlice(envSlice []string) (map[string]string, error) {
	envMap := make(map[string]string, len(envSlice))
	for _, e := range envSlice {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return nil, yoerrors.NewUsageError("invalid --env value %q: must be KEY=VAL", e)
		}
		envMap[k] = v
	}
	return envMap, nil
}

// resolveNewDirSpecs parses rawWorkdirArg and rawDirs into DirSpec values.
func resolveNewDirSpecs(rawWorkdirArg string, rawDirs []string) (workdirSpec yoloai.DirSpec, auxDirSpecs []yoloai.DirSpec, err error) {
	layout := cliutil.Layout()
	homeDir := layout.HomeDir
	interpEnv := layout.Env().EnvForConfigInterpolation()
	if rawWorkdirArg != "" {
		parsed, parseErr := cliutil.ParseDirArg(rawWorkdirArg, homeDir, interpEnv)
		if parseErr != nil {
			return yoloai.DirSpec{}, nil, yoerrors.NewUsageError("invalid workdir: %s", parseErr)
		}
		workdirSpec = *parsed
	}
	for _, rawDir := range rawDirs {
		parsed, parseErr := cliutil.ParseAuxDirArg(rawDir, homeDir, interpEnv)
		if parseErr != nil {
			// ParseAuxDirArg returns *UsageError for the :copy/:overlay
			// rejection cases (already user-actionable); pass it through.
			// Other parse errors get the "invalid directory" prefix.
			var usage *yoerrors.UsageError
			if errors.As(parseErr, &usage) {
				return yoloai.DirSpec{}, nil, parseErr
			}
			return yoloai.DirSpec{}, nil, yoerrors.NewUsageError("invalid directory %q: %s", rawDir, parseErr)
		}
		auxDirSpecs = append(auxDirSpecs, *parsed)
	}
	return workdirSpec, auxDirSpecs, nil
}

// executeNewCreate provisions the sandbox via Client.CreateSandbox, starts it
// (unless --no-start), and — when attach — hands off to Sandbox.Attach for the
// interactive session. If Create refuses a dirty workdir (*DirtyWorkdirError) it
// always prints the warning, then proceeds only when --allow-dirty was given;
// otherwise it returns the refusal. It never prompts: widening the destructive
// scope to include a dirty workdir is opt-in via --allow-dirty alone.
func executeNewCreate(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, opts yoloai.SandboxCreateOptions, attach, noStart bool) error {
	sb, err := c.CreateSandbox(ctx, opts)

	var dirty *yoloai.DirtyWorkdirError
	if errors.As(err, &dirty) {
		printDirtyWarning(cmd, dirty)
		allowDirty, _ := cmd.Flags().GetBool("allow-dirty")
		if !allowDirty {
			fmt.Fprintln(cmd.ErrOrStderr(), "Re-run with --allow-dirty to proceed.") //nolint:errcheck // best-effort output
			return dirty
		}
		opts.AllowDirtyWorkdir = true
		sb, err = c.CreateSandbox(ctx, opts)
	}
	if err != nil {
		return err
	}

	if cliutil.BugReportFile != nil {
		cliutil.BugReportSandboxName = sb.Name()
	}

	// CreateSandbox only provisions; launch the agent now unless --no-start.
	// The launch output (on stderr, or discarded in --json mode) precedes the
	// creation summary, matching the old create-starts-by-default flow.
	if !noStart {
		if _, err := sb.Start(ctx, yoloai.SandboxStartOptions{Env: opts.Env}); err != nil {
			return err
		}
	}

	if cliutil.JSONEnabled(cmd) {
		meta, loadErr := loadCreatedMeta(c, sb.Name())
		if loadErr != nil {
			return loadErr
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), meta)
	}

	// Print the creation summary (presentation is the CLI's job, not the
	// library's). Goes to stderr — the stream the Engine's creation output
	// used — keeping human output cohesive there (stdout is reserved for --json).
	if meta, loadErr := loadCreatedMeta(c, sb.Name()); loadErr == nil {
		printCreateSummary(cmd.ErrOrStderr(), meta, opts.Prompt != "", opts.VscodeTunnel)
	}

	// First successful create runs EnsureSetup; show the one-time onboarding
	// tip here (the tip is CLI presentation, not the library's concern).
	cliutil.MaybeShowFirstRunTip(cmd.ErrOrStderr())

	if !attach {
		return nil
	}
	return cliutil.WithTerminal(func(io yoloai.IOStreams) error {
		return sb.Agent().Attach(ctx, io)
	})
}

// loadCreatedMeta reads a just-created sandbox's metadata through the in-scope
// client. Factored out so executeNewCreate's JSON and human-summary branches
// share one resolve-handle-then-read step.
func loadCreatedMeta(c *yoloai.Client, name string) (*yoloai.Environment, error) {
	sb, err := c.Sandbox(name)
	if err != nil {
		return nil, err
	}
	return sb.Metadata()
}

// printCreateSummary renders the post-create summary + next-step hints from the
// created sandbox's metadata. The library returns the sandbox; the CLI owns this
// presentation (F8).
func printCreateSummary(out io.Writer, meta *yoloai.Environment, hasPrompt, vscodeTunnel bool) {
	fmt.Fprintf(out, "Sandbox %s created\n", meta.Name)  //nolint:errcheck // best-effort output
	fmt.Fprintf(out, "  Agent:    %s\n", meta.AgentType) //nolint:errcheck // best-effort output
	if meta.Profile != "" {
		fmt.Fprintf(out, "  Profile:  %s\n", meta.Profile) //nolint:errcheck // best-effort output
	}
	fmt.Fprintf(out, "  Workdir:  %s (%s)\n", meta.Workdir.HostPath, meta.Workdir.Mode) //nolint:errcheck // best-effort output
	for _, d := range meta.Directories {
		mode := d.Mode
		if mode == "" {
			mode = "ro"
		}
		if d.MountPath != "" {
			fmt.Fprintf(out, "  Dir:      %s → %s (%s)\n", d.HostPath, d.MountPath, mode) //nolint:errcheck // best-effort output
		} else {
			fmt.Fprintf(out, "  Dir:      %s (%s)\n", d.HostPath, mode) //nolint:errcheck // best-effort output
		}
	}
	switch meta.NetworkMode {
	case "none":
		fmt.Fprintln(out, "  Network:  none") //nolint:errcheck // best-effort output
	case "isolated":
		fmt.Fprintf(out, "  Network:  isolated (%d allowed domains)\n", len(meta.NetworkAllow)) //nolint:errcheck // best-effort output
	}
	if len(meta.Ports) > 0 {
		fmt.Fprintf(out, "  Ports:    %s\n", strings.Join(meta.Ports, ", ")) //nolint:errcheck // best-effort output
	}
	fmt.Fprintln(out) //nolint:errcheck // best-effort output

	if hasPrompt {
		fmt.Fprintf(out, "Run 'yoloai attach %s' to interact (Ctrl-b d to detach)\n", meta.Name) //nolint:errcheck // best-effort output
		fmt.Fprintf(out, "    'yoloai diff %s' when done\n", meta.Name)                          //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintf(out, "Run 'yoloai attach %s' to start working (Ctrl-b d to detach)\n", meta.Name) //nolint:errcheck // best-effort output
	}
	if vscodeTunnel {
		fmt.Fprintln(out, "\nVS Code tunnel starting in the 'vscode-tunnel' tmux window.")          //nolint:errcheck // best-effort output
		fmt.Fprintln(out, "Run 'yoloai help vscode-tunnel' for setup and connection instructions.") //nolint:errcheck // best-effort output
	}
}

// printDirtyWarning renders the uncommitted-changes warning. It never prompts:
// proceeding past a dirty workdir widens the destructive scope (the agent sees
// changes that could be modified or lost), and scope is widened only by the
// explicit --allow-dirty flag, never by an interactive answer.
func printDirtyWarning(cmd *cobra.Command, dirty *yoloai.DirtyWorkdirError) {
	out := cmd.ErrOrStderr()
	for _, d := range dirty.Dirs {
		fmt.Fprintf(out, "WARNING: %s has uncommitted changes (%s)\n", d.Path, d.Status) //nolint:errcheck // best-effort output
	}
	fmt.Fprintln(out, "These changes will be visible to the agent and could be modified or lost.") //nolint:errcheck // best-effort output
}

// resolveNewIsolationOS resolves the --isolation and --os flags with config fallback
// and validates their combinations, returning an error for unsupported combos.
func resolveNewIsolationOS(cmd *cobra.Command) (isolation yoloai.IsolationMode, targetOS string, err error) {
	cfg, _ := config.LoadDefaultsConfig(cliutil.Layout())
	var cfgIsolation, cfgOS string
	if cfg != nil {
		cfgIsolation = cfg.Isolation
		cfgOS = cfg.OS
	}
	isolation = yoloai.IsolationMode(cliutil.Coalesce(cliutil.FlagStr(cmd, "isolation"), cfgIsolation))
	targetOS = cliutil.Coalesce(cliutil.FlagStr(cmd, "os"), cfgOS)

	if err := validateIsolationOSCombo(isolation, targetOS); err != nil {
		return "", "", err
	}
	return isolation, targetOS, nil
}

// validateIsolationOSCombo returns an error for unsupported isolation+OS
// combinations. Thin wrapper over runtime.IsolationAvailability: the runtime
// package owns the rules and their messages, the CLI just turns the verdict
// into a UsageError.
func validateIsolationOSCombo(isolation yoloai.IsolationMode, targetOS string) error {
	macMajor, containerInstalled := yoloai.AppleVMHostSignals()
	available, reason, help := yoloai.IsolationAvailability(isolation, targetOS, goruntime.GOOS, macMajor, containerInstalled)
	if available {
		return nil
	}
	if help != "" {
		return yoerrors.NewUsageError("%s\n%s", reason, help)
	}
	return yoerrors.NewUsageError("%s", reason)
}

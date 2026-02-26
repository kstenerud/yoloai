package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/kstenerud/yoloai/internal/agent"
)

// CreateOptions holds all parameters for sandbox creation.
type CreateOptions struct {
	Name        string
	WorkdirArg  string   // raw workdir argument (path with optional :copy/:rw/:force suffixes)
	Agent       string   // agent name (e.g., "claude", "test")
	Model       string   // model name or alias (e.g., "sonnet", "claude-sonnet-4-latest")
	Prompt      string   // prompt text (from --prompt)
	PromptFile  string   // prompt file path (from --prompt-file)
	NetworkNone bool     // --network-none flag
	Ports       []string // --port flags (e.g., ["3000:3000"])
	Replace     bool     // --replace flag
	NoStart     bool     // --no-start flag
	Yes         bool     // --yes flag (skip confirmations)
	Passthrough []string // args after -- passed to agent
	Version     string   // yoloAI version for meta.json
	Attach      bool     // --attach flag (auto-attach after creation)
}

// sandboxState holds resolved state computed during preparation.
type sandboxState struct {
	name        string
	sandboxDir  string
	workdir     *DirArg
	workCopyDir string
	agent       *agent.Definition
	model       string
	hasPrompt   bool
	networkMode string
	ports       []string
	meta        *Meta
	configJSON  []byte
}

// containerConfig is the serializable form of /yoloai/config.json.
type containerConfig struct {
	HostUID        int    `json:"host_uid"`
	HostGID        int    `json:"host_gid"`
	AgentCommand   string `json:"agent_command"`
	StartupDelay   int    `json:"startup_delay"`
	ReadyPattern   string `json:"ready_pattern"`
	SubmitSequence string `json:"submit_sequence"`
}

// Create creates and optionally starts a new sandbox.
// Returns the sandbox name on success (empty if user cancelled or no-start).
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (string, error) {
	if err := m.EnsureSetup(ctx); err != nil {
		return "", err
	}

	state, err := m.prepareSandboxState(ctx, opts)
	if err != nil {
		return "", err
	}
	if state == nil {
		return "", nil // user cancelled
	}

	if opts.NoStart {
		m.printCreationOutput(state, false)
		return "", nil
	}

	if err := m.launchContainer(ctx, state); err != nil {
		return "", err
	}

	m.printCreationOutput(state, opts.Attach)
	return state.name, nil
}

// prepareSandboxState handles validation, safety checks, directory
// creation, workdir copy, git baseline, and meta/config writing.
func (m *Manager) prepareSandboxState(ctx context.Context, opts CreateOptions) (*sandboxState, error) {
	// Parse workdir
	workdir, err := ParseDirArg(opts.WorkdirArg)
	if err != nil {
		return nil, NewUsageError("invalid workdir: %s", err)
	}
	if workdir.Mode == "" {
		workdir.Mode = "copy"
	}

	// Validate
	if opts.Name == "" {
		return nil, NewUsageError("sandbox name is required")
	}

	agentDef := agent.GetAgent(opts.Agent)
	if agentDef == nil {
		return nil, NewUsageError("unknown agent: %s", opts.Agent)
	}

	sandboxDir := Dir(opts.Name)
	if _, err := os.Stat(sandboxDir); err == nil && !opts.Replace {
		return nil, fmt.Errorf("sandbox %q already exists (use --replace to recreate): %w", opts.Name, ErrSandboxExists)
	}

	if opts.Prompt != "" && opts.PromptFile != "" {
		return nil, NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	if _, err := os.Stat(workdir.Path); err != nil {
		return nil, NewUsageError("workdir does not exist: %s", workdir.Path)
	}

	hasAPIKey := hasAnyAPIKey(agentDef)
	hasAuth := hasAnyAuthFile(agentDef)
	if !hasAPIKey && !hasAuth {
		return nil, fmt.Errorf("no authentication found: set %s or provide OAuth credentials (%s): %w",
			strings.Join(agentDef.APIKeyEnvVars, "/"),
			describeSeedAuthFiles(agentDef),
			ErrMissingAPIKey)
	}

	// Safety checks
	if IsDangerousDir(workdir.Path) {
		if workdir.Force {
			fmt.Fprintf(m.output, "WARNING: mounting dangerous directory %s\n", workdir.Path) //nolint:errcheck // best-effort output
		} else {
			return nil, NewUsageError("refusing to mount dangerous directory %s (use :force to override)", workdir.Path)
		}
	}

	if err := CheckPathOverlap([]string{workdir.Path}); err != nil {
		return nil, NewUsageError("%s", err)
	}

	// --replace: destroy existing sandbox
	if opts.Replace {
		if _, err := os.Stat(sandboxDir); err == nil {
			if err := m.Destroy(ctx, opts.Name, true); err != nil {
				return nil, fmt.Errorf("replace existing sandbox: %w", err)
			}
		}
	}

	// Dirty repo warning
	dirtyMsg, err := CheckDirtyRepo(workdir.Path)
	if err != nil {
		return nil, fmt.Errorf("check repo status: %w", err)
	}
	if dirtyMsg != "" && !opts.Yes {
		fmt.Fprintf(m.output, "WARNING: %s has uncommitted changes (%s)\n", workdir.Path, dirtyMsg)         //nolint:errcheck // best-effort output
		fmt.Fprintln(m.output, "These changes will be visible to the agent and could be modified or lost.") //nolint:errcheck // best-effort output
		if !Confirm("Continue? [y/N] ", os.Stdin, m.output) {
			return nil, nil // user cancelled
		}
	}

	// Create directory structure
	workCopyDir := WorkDir(opts.Name, workdir.Path)
	for _, dir := range []string{
		sandboxDir,
		filepath.Join(sandboxDir, "work"),
		filepath.Join(sandboxDir, "agent-state"),
		filepath.Join(sandboxDir, "home-seed"),
	} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Copy seed files into agent-state (config, OAuth credentials, etc.)
	if _, err := copySeedFiles(agentDef, sandboxDir, hasAPIKey); err != nil {
		return nil, fmt.Errorf("copy seed files: %w", err)
	}

	// Ensure container-required settings (e.g., skip bypass permissions prompt)
	if err := ensureContainerSettings(agentDef, sandboxDir); err != nil {
		return nil, fmt.Errorf("ensure container settings: %w", err)
	}

	// Fix install method in seeded .claude.json (host has "native", container uses npm)
	if err := ensureHomeSeedConfig(agentDef, sandboxDir); err != nil {
		return nil, fmt.Errorf("ensure home seed config: %w", err)
	}

	// Copy workdir
	if workdir.Mode == "copy" {
		if err := copyDir(workdir.Path, workCopyDir); err != nil {
			return nil, fmt.Errorf("copy workdir: %w", err)
		}
	} else {
		if err := os.MkdirAll(workCopyDir, 0750); err != nil {
			return nil, fmt.Errorf("create work dir: %w", err)
		}
	}

	// Strip git metadata from copy before creating fresh baseline
	if workdir.Mode == "copy" {
		if err := removeGitDirs(workCopyDir); err != nil {
			return nil, fmt.Errorf("remove git metadata: %w", err)
		}
	}

	// Git baseline
	var baselineSHA string
	if workdir.Mode == "copy" {
		sha, err := gitBaseline(workCopyDir)
		if err != nil {
			return nil, fmt.Errorf("git baseline: %w", err)
		}
		baselineSHA = sha
	} else {
		sha, _ := gitHeadSHA(workdir.Path)
		baselineSHA = sha
	}

	// Read prompt
	promptText, err := readPrompt(opts.Prompt, opts.PromptFile)
	if err != nil {
		return nil, err
	}
	hasPrompt := promptText != ""

	// Resolve model alias
	model := resolveModel(agentDef, opts.Model)

	// Build agent command
	agentCommand := buildAgentCommand(agentDef, model, promptText, opts.Passthrough)

	// Build config.json
	configData, err := buildContainerConfig(agentDef, agentCommand)
	if err != nil {
		return nil, fmt.Errorf("build config.json: %w", err)
	}

	// Determine network mode
	networkMode := ""
	if opts.NetworkNone {
		networkMode = "none"
	}

	// Write state files
	meta := &Meta{
		YoloaiVersion: opts.Version,
		Name:          opts.Name,
		CreatedAt:     time.Now(),
		Agent:         opts.Agent,
		Model:         model,
		Workdir: WorkdirMeta{
			HostPath:    workdir.Path,
			MountPath:   workdir.Path,
			Mode:        workdir.Mode,
			BaselineSHA: baselineSHA,
		},
		HasPrompt:   hasPrompt,
		NetworkMode: networkMode,
		Ports:       opts.Ports,
	}

	if err := SaveMeta(sandboxDir, meta); err != nil {
		return nil, err
	}

	if hasPrompt {
		if err := os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte(promptText), 0600); err != nil {
			return nil, fmt.Errorf("write prompt.txt: %w", err)
		}
	}

	if err := os.WriteFile(filepath.Join(sandboxDir, "log.txt"), nil, 0600); err != nil {
		return nil, fmt.Errorf("write log.txt: %w", err)
	}

	if err := os.WriteFile(filepath.Join(sandboxDir, "config.json"), configData, 0600); err != nil {
		return nil, fmt.Errorf("write config.json: %w", err)
	}

	return &sandboxState{
		name:        opts.Name,
		sandboxDir:  sandboxDir,
		workdir:     workdir,
		workCopyDir: workCopyDir,
		agent:       agentDef,
		model:       model,
		hasPrompt:   hasPrompt,
		networkMode: networkMode,
		ports:       opts.Ports,
		meta:        meta,
		configJSON:  configData,
	}, nil
}

// launchContainer creates a Docker container from sandboxState, starts it,
// and cleans up credential temp files. Used by both initial creation and
// recreation from meta.json.
func (m *Manager) launchContainer(ctx context.Context, state *sandboxState) error {
	secretsDir, err := createSecretsDir(state.agent)
	if err != nil {
		return fmt.Errorf("create secrets: %w", err)
	}
	if secretsDir != "" {
		defer os.RemoveAll(secretsDir) //nolint:errcheck // best-effort cleanup
	}

	mounts := buildMounts(state, secretsDir)

	portBindings, exposedPorts, err := parsePortBindings(state.ports)
	if err != nil {
		return err
	}

	config := &container.Config{
		Image:        "yoloai-base",
		WorkingDir:   state.workdir.Path,
		ExposedPorts: exposedPorts,
	}

	initFlag := true
	hostConfig := &container.HostConfig{
		Init:         &initFlag,
		NetworkMode:  container.NetworkMode(state.networkMode),
		PortBindings: portBindings,
		Mounts:       mounts,
	}

	cname := ContainerName(state.name)
	resp, err := m.client.ContainerCreate(ctx, config, hostConfig, nil, nil, cname)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if err := m.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	// Wait briefly for entrypoint to read secrets before cleanup
	if secretsDir != "" {
		time.Sleep(1 * time.Second)
	}

	return nil
}

// printCreationOutput prints the context-aware summary.
// When autoAttach is true, the attach hint is suppressed (we're about to attach).
func (m *Manager) printCreationOutput(state *sandboxState, autoAttach bool) {
	if state == nil {
		return
	}

	fmt.Fprintf(m.output, "Sandbox %s created\n", state.name)                              //nolint:errcheck // best-effort output
	fmt.Fprintf(m.output, "  Agent:    %s\n", state.agent.Name)                            //nolint:errcheck // best-effort output
	fmt.Fprintf(m.output, "  Workdir:  %s (%s)\n", state.workdir.Path, state.workdir.Mode) //nolint:errcheck // best-effort output
	if state.networkMode == "none" {
		fmt.Fprintln(m.output, "  Network:  none") //nolint:errcheck // best-effort output
	}
	if len(state.ports) > 0 {
		fmt.Fprintf(m.output, "  Ports:    %s\n", strings.Join(state.ports, ", ")) //nolint:errcheck // best-effort output
	}
	fmt.Fprintln(m.output) //nolint:errcheck // best-effort output

	if autoAttach {
		return
	}

	if state.hasPrompt {
		fmt.Fprintf(m.output, "Run 'yoloai attach %s' to interact (Ctrl-b d to detach)\n", state.name) //nolint:errcheck // best-effort output
		fmt.Fprintf(m.output, "    'yoloai diff %s' when done\n", state.name)                          //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintf(m.output, "Run 'yoloai attach %s' to start working (Ctrl-b d to detach)\n", state.name) //nolint:errcheck // best-effort output
	}
}

// resolveModel expands a model alias using the agent definition.
func resolveModel(agentDef *agent.Definition, model string) string {
	if model == "" {
		return ""
	}
	if agentDef.ModelAliases != nil {
		if resolved, ok := agentDef.ModelAliases[model]; ok {
			return resolved
		}
	}
	return model
}

// buildAgentCommand constructs the full agent command string for config.json.
func buildAgentCommand(agentDef *agent.Definition, model string, prompt string, passthrough []string) string {
	var cmd string

	if agentDef.PromptMode == agent.PromptModeHeadless && prompt != "" {
		escaped := shellEscapeForDoubleQuotes(prompt)
		cmd = strings.ReplaceAll(agentDef.HeadlessCmd, "PROMPT", escaped)
	} else {
		cmd = agentDef.InteractiveCmd
		if model != "" && agentDef.ModelFlag != "" {
			cmd += " " + agentDef.ModelFlag + " " + model
		}
	}

	for _, arg := range passthrough {
		cmd += " " + arg
	}

	return cmd
}

// shellEscapeForDoubleQuotes escapes a string for embedding inside
// double quotes in a shell command.
func shellEscapeForDoubleQuotes(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	)
	return r.Replace(s)
}

// buildContainerConfig creates the config.json content.
func buildContainerConfig(agentDef *agent.Definition, agentCommand string) ([]byte, error) {
	cfg := containerConfig{
		HostUID:        os.Getuid(),
		HostGID:        os.Getgid(),
		AgentCommand:   agentCommand,
		StartupDelay:   int(agentDef.StartupDelay / time.Millisecond),
		ReadyPattern:   agentDef.ReadyPattern,
		SubmitSequence: agentDef.SubmitSequence,
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// readPrompt reads the prompt from --prompt, --prompt-file, or stdin ("-").
func readPrompt(prompt, promptFile string) (string, error) {
	if prompt != "" && promptFile != "" {
		return "", NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	if prompt == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if prompt != "" {
		return prompt, nil
	}

	if promptFile == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if promptFile != "" {
		data, err := os.ReadFile(promptFile) //nolint:gosec // G304: path is from user-provided --prompt-file flag
		if err != nil {
			return "", fmt.Errorf("read prompt file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", nil
}

// parsePortBindings converts ["host:container", ...] to Docker port types.
func parsePortBindings(ports []string) (nat.PortMap, nat.PortSet, error) {
	if len(ports) == 0 {
		return nil, nil, nil
	}

	portMap := nat.PortMap{}
	portSet := nat.PortSet{}

	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			return nil, nil, NewUsageError("invalid port format %q (expected host:container)", p)
		}
		hostPort := parts[0]
		containerPort := parts[1]

		port, err := nat.NewPort("tcp", containerPort)
		if err != nil {
			return nil, nil, NewUsageError("invalid container port %q: %s", containerPort, err)
		}

		portMap[port] = append(portMap[port], nat.PortBinding{
			HostPort: hostPort,
		})
		portSet[port] = struct{}{}
	}

	return portMap, portSet, nil
}

// removeGitDirs recursively removes all .git entries (files and directories)
// from root. This strips git metadata from a copied working tree so that
// hooks, LFS filters, submodule links, and worktree links don't interfere
// with yoloAI's internal git operations.
func removeGitDirs(root string) error {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			toRemove = append(toRemove, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk for .git entries: %w", err)
	}

	// Remove in reverse order so nested entries are removed before parents.
	for i := len(toRemove) - 1; i >= 0; i-- {
		if err := os.RemoveAll(toRemove[i]); err != nil {
			return fmt.Errorf("remove %s: %w", toRemove[i], err)
		}
	}
	return nil
}

// gitBaseline creates a fresh git baseline for the work copy.
// Assumes all .git entries have already been removed by removeGitDirs.
func gitBaseline(workDir string) (string, error) {
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "yoloai@localhost"},
		{"config", "user.name", "yoloai"},
		{"add", "-A"},
		{"commit", "-m", "yoloai baseline", "--allow-empty"},
	}
	for _, args := range cmds {
		if err := runGitCmd(workDir, args...); err != nil {
			return "", err
		}
	}

	return gitHeadSHA(workDir)
}

// newGitCmd builds an exec.Cmd for git with hooks disabled.
// All internal git operations use this to prevent copied hooks from firing.
func newGitCmd(dir string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	return exec.Command("git", fullArgs...) //nolint:gosec // G204: dir is sandbox-controlled path
}

// gitHeadSHA returns the HEAD commit SHA for the given git repo.
func gitHeadSHA(dir string) (string, error) {
	cmd := newGitCmd(dir, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// runGitCmd executes a git command in the given directory.
func runGitCmd(dir string, args ...string) error {
	cmd := newGitCmd(dir, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(output)), err)
	}
	return nil
}

// createSecretsDir creates a temp directory with one file per API key.
// Returns empty string if no keys are needed.
func createSecretsDir(agentDef *agent.Definition) (string, error) {
	if len(agentDef.APIKeyEnvVars) == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp("", "yoloai-secrets-*")
	if err != nil {
		return "", fmt.Errorf("create secrets temp dir: %w", err)
	}

	wrote := false
	for _, key := range agentDef.APIKeyEnvVars {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		keyPath := filepath.Join(tmpDir, key)
		if err := os.WriteFile(keyPath, []byte(value), 0600); err != nil { //nolint:gosec // G703: key is from agent definition, not user input
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write secret %s: %w", key, err)
		}
		wrote = true
	}

	if !wrote {
		_ = os.RemoveAll(tmpDir)
		return "", nil
	}

	return tmpDir, nil
}

// buildMounts constructs the Docker bind mounts for the container.
func buildMounts(state *sandboxState, secretsDir string) []mount.Mount {
	var mounts []mount.Mount

	// Work directory
	if state.workdir.Mode == "copy" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: state.workCopyDir,
			Target: state.workdir.Path,
		})
	} else {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: state.workdir.Path,
			Target: state.workdir.Path,
		})
	}

	// Agent state directory
	if state.agent.StateDir != "" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: filepath.Join(state.sandboxDir, "agent-state"),
			Target: state.agent.StateDir,
		})
	}

	// Log file
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: filepath.Join(state.sandboxDir, "log.txt"),
		Target: "/yoloai/log.txt",
	})

	// Prompt file
	if state.hasPrompt {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   filepath.Join(state.sandboxDir, "prompt.txt"),
			Target:   "/yoloai/prompt.txt",
			ReadOnly: true,
		})
	}

	// Config file
	mounts = append(mounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   filepath.Join(state.sandboxDir, "config.json"),
		Target:   "/yoloai/config.json",
		ReadOnly: true,
	})

	// Home-seed files (individual file mounts into /home/yoloai/)
	for _, sf := range state.agent.SeedFiles {
		if !sf.HomeDir {
			continue
		}
		src := filepath.Join(state.sandboxDir, "home-seed", sf.TargetPath)
		if _, err := os.Stat(src); err != nil {
			continue // skip if not seeded
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: src,
			Target: "/home/yoloai/" + sf.TargetPath,
		})
	}

	// Secrets
	if secretsDir != "" {
		for _, key := range state.agent.APIKeyEnvVars {
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   filepath.Join(secretsDir, key),
				Target:   filepath.Join("/run/secrets", key),
				ReadOnly: true,
			})
		}
	}

	return mounts
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// hasAnyAPIKey returns true if any of the agent's required API key env vars are set.
func hasAnyAPIKey(agentDef *agent.Definition) bool {
	for _, key := range agentDef.APIKeyEnvVars {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// hasAnyAuthFile returns true if any auth-only seed files exist on disk.
func hasAnyAuthFile(agentDef *agent.Definition) bool {
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			if _, err := os.Stat(expandTilde(sf.HostPath)); err == nil {
				return true
			}
		}
	}
	return false
}

// describeSeedAuthFiles returns a human-readable description of expected auth file paths.
func describeSeedAuthFiles(agentDef *agent.Definition) string {
	var paths []string
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			paths = append(paths, sf.HostPath)
		}
	}
	return strings.Join(paths, ", ")
}

// copySeedFiles copies seed files from the host into the sandbox.
// Files with AuthOnly=true are skipped when hasAPIKey is true.
// Files with HomeDir=true go to home-seed/ (mounted at /home/yoloai/);
// others go to agent-state/ (mounted at StateDir).
// Returns true if any files were copied. Skips files that don't exist on the host.
func copySeedFiles(agentDef *agent.Definition, sandboxDir string, hasAPIKey bool) (bool, error) {
	copied := false
	agentStateDir := filepath.Join(sandboxDir, "agent-state")
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")

	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly && hasAPIKey {
			continue // auth file not needed when API key is set
		}

		hostPath := expandTilde(sf.HostPath)
		if _, err := os.Stat(hostPath); err != nil {
			continue // skip missing files
		}

		baseDir := agentStateDir
		if sf.HomeDir {
			baseDir = homeSeedDir
		}
		targetPath := filepath.Join(baseDir, sf.TargetPath)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
			return copied, fmt.Errorf("create dir for %s: %w", sf.TargetPath, err)
		}

		data, err := os.ReadFile(hostPath) //nolint:gosec // G304: path is from agent definition, not user input
		if err != nil {
			return copied, fmt.Errorf("read %s: %w", hostPath, err)
		}

		if err := os.WriteFile(targetPath, data, 0600); err != nil {
			return copied, fmt.Errorf("write %s: %w", targetPath, err)
		}
		copied = true
	}

	return copied, nil
}

// readJSONMap reads a JSON file into a map, returning an empty map if the file doesn't exist.
func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeJSONMap marshals a map and writes it as indented JSON to the given path.
func writeJSONMap(path string, m map[string]any) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

// ensureContainerSettings merges required container settings into agent-state/settings.json.
// For agents using --dangerously-skip-permissions, ensures the bypass prompt is skipped.
func ensureContainerSettings(agentDef *agent.Definition, sandboxDir string) error {
	if agentDef.StateDir == "" {
		return nil
	}

	if !strings.Contains(agentDef.InteractiveCmd, "--dangerously-skip-permissions") {
		return nil
	}

	settingsPath := filepath.Join(sandboxDir, "agent-state", "settings.json")

	settings, err := readJSONMap(settingsPath)
	if err != nil {
		return err
	}

	settings["skipDangerousModePermissionPrompt"] = true

	return writeJSONMap(settingsPath, settings)
}

// ensureHomeSeedConfig patches home-seed/.claude.json to set installMethod to
// "npm-global". The host file typically has "native" since the user's local
// Claude Code uses the native installer, but inside the container we install
// via npm. Without this fix Claude Code shows spurious warnings about missing
// ~/.local/bin/claude and PATH misconfiguration.
func ensureHomeSeedConfig(agentDef *agent.Definition, sandboxDir string) error {
	// Only relevant for agents that seed .claude.json into HomeDir
	var hasHomeSeed bool
	for _, sf := range agentDef.SeedFiles {
		if sf.HomeDir && sf.TargetPath == ".claude.json" {
			hasHomeSeed = true
			break
		}
	}
	if !hasHomeSeed {
		return nil
	}

	configPath := filepath.Join(sandboxDir, "home-seed", ".claude.json")

	config, err := readJSONMap(configPath)
	if err != nil {
		return err
	}

	config["installMethod"] = "npm-global"

	return writeJSONMap(configPath, config)
}

// copyDir copies a directory tree using cp -rp.
func copyDir(src, dst string) error {
	cmd := exec.Command("cp", "-rp", src, dst) //nolint:gosec // G204: paths are validated sandbox paths
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp -rp: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

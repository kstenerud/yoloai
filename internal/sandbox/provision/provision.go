// ABOUTME: Seed-file and credential provisioning for a sandbox before first launch:
// ABOUTME: secret-dir creation, sudo recovery, auth checks, and settings injection.
package provision

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// CreateSecretsDir creates a temp directory with one file per env var / API key.
// Env vars are written first; API keys overwrite on conflict (take precedence).
// credOverrides contains sudo-recovered credential defaults for keys absent from
// os.Environ; they are used as a fallback so that creation under sudo sees credentials.
// Returns empty string if nothing was written.
func CreateSecretsDir(agentDef *agent.Definition, envVars map[string]string, security runtime.IsolationMode, credOverrides map[string]string) (string, error) {
	if len(agentDef.APIKeyEnvVars) == 0 && len(agentDef.AuthHintEnvVars) == 0 && len(envVars) == 0 && len(credOverrides) == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp("", "yoloai-secrets-*")
	if err != nil {
		return "", fmt.Errorf("create secrets temp dir: %w", err)
	}

	// Determine permissions based on security mode.
	// gVisor gofer runs as remapped uid and needs world-readable/executable.
	// Standard Docker can use restrictive permissions.
	// The dir lives in /tmp and is removed within seconds of container startup.
	perms := state.Perms(security)

	if err := os.Chmod(tmpDir, perms.SecretsDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("chmod secrets dir: %w", err)
	}
	// When running via sudo, chown the dir to the real user so the container
	// process (running as that user via --userns=keep-id) can read it.
	_ = fileutil.ChownIfSudo(tmpDir) //nolint:errcheck // best-effort; individual files are already chowned by WriteFilePerm

	wrote := false

	// Write env vars first
	for k, v := range envVars {
		if err := fileutil.WriteFilePerm(filepath.Join(tmpDir, k), []byte(v), perms.SecretsFile); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write env %s: %w", k, err)
		}
		wrote = true
	}

	// Write host env vars for API keys and auth hints (overwrites config env on conflict).
	// credOverrides provides sudo-recovered values for keys absent from os.Environ.
	for _, key := range append(agentDef.APIKeyEnvVars, agentDef.AuthHintEnvVars...) {
		value := os.Getenv(key) //nolint:forbidigo // §12: agent API key / auth-hint value (declared exception)
		if value == "" {
			value = credOverrides[key]
		}
		if value == "" {
			continue
		}
		if err := fileutil.WriteFilePerm(filepath.Join(tmpDir, key), []byte(value), perms.SecretsFile); err != nil {
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

// RecoverSudoCredentials returns sudo-recovered credential env vars for keys
// absent from the current process environment. Under `sudo` (without -E) the
// API-key / OAuth env vars are stripped from os.Environ; recovering them from
// the parent sudo process lets both `new` (Create) and `restart`
// (recreateContainer) inject them. Keys present in os.Environ are skipped so a
// real host value always wins.
func RecoverSudoCredentials() map[string]string {
	overrides := make(map[string]string)
	for k, v := range sudoParentEnv() {
		if os.Getenv(k) == "" { //nolint:forbidigo // §12: sudo credential recovery — only override keys absent from the live env
			overrides[k] = v
		}
	}
	return overrides
}

// sudoParentEnv returns env vars from the parent sudo process when yoloai is
// run via sudo. sudo strips most env vars before exec'ing the child, but the
// sudo process itself inherits the full user environment. Reading the parent's
// /proc/<ppid>/environ recovers vars like CLAUDE_CODE_OAUTH_TOKEN and
// ANTHROPIC_API_KEY that were stripped. Returns an empty map if not running
// under sudo or if the parent environ cannot be read.
func sudoParentEnv() map[string]string {
	result := make(map[string]string)
	if fileutil.SudoUID() == -1 {
		return result
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", os.Getppid())) //nolint:gosec,forbidigo // G304 + §12: read parent's environ to recover sudo-stripped credentials
	if err != nil {
		return result
	}
	for kv := range strings.SplitSeq(string(data), "\x00") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" {
			result[k] = v
		}
	}
	return result
}

// HasAnyAPIKey returns true if any of the agent's required API key env vars are set
// in the process environment or in credOverrides (sudo-recovered credential defaults).
func HasAnyAPIKey(agentDef *agent.Definition, credOverrides map[string]string) bool {
	if len(agentDef.APIKeyEnvVars) == 0 {
		return true // no API key required
	}
	for _, key := range agentDef.APIKeyEnvVars {
		if os.Getenv(key) != "" || credOverrides[key] != "" { //nolint:forbidigo // §12: agent API-key presence check (declared exception)
			return true
		}
	}
	return false
}

// HasAnyAuthFile returns true if any auth-only seed files exist on disk
// or can be read from the macOS Keychain.
// homeDir is used for ~ expansion in seed file host paths.
func HasAnyAuthFile(agentDef *agent.Definition, homeDir string) bool {
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			if _, err := os.Stat(config.ExpandTilde(sf.HostPath, homeDir)); err == nil {
				return true
			}
			if sf.KeychainService != "" {
				if _, err := KeychainReader(sf.KeychainService); err == nil {
					return true
				}
			}
		}
	}
	return false
}

// HasAnyAuthHint returns true if any of the agent's auth hint env vars are set
// in the host environment, in the config env map, or in credOverrides
// (sudo-recovered credential defaults). This allows agents like aider to work
// with local model servers (Ollama, LM Studio) without a cloud API key.
func HasAnyAuthHint(agentDef *agent.Definition, configEnv map[string]string, credOverrides map[string]string) bool {
	for _, key := range agentDef.AuthHintEnvVars {
		if os.Getenv(key) != "" || credOverrides[key] != "" { //nolint:forbidigo // §12: agent auth-hint presence check (declared exception)
			return true
		}
		if configEnv[key] != "" {
			return true
		}
	}
	return false
}

// DescribeSeedAuthFiles returns a human-readable description of expected auth file paths.
func DescribeSeedAuthFiles(agentDef *agent.Definition) string {
	var paths []string
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			paths = append(paths, sf.HostPath)
		}
	}
	return strings.Join(paths, ", ")
}

// CopySeedFiles copies seed files from the host into the sandbox.
// Files with AuthOnly=true are skipped when hasAPIKey is true.
// Files with HomeDir=true go to home-seed/ (mounted at /home/yoloai/);
// others go to agent-runtime/ (mounted at StateDir).
// Returns true if any files were copied. Skips files that don't exist on the host.
// homeDir is used for ~ expansion in seed file host paths.
func CopySeedFiles(agentDef *agent.Definition, sandboxDir string, hasAPIKey bool, homeDir string) (bool, error) {
	copiedAuth := false
	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")

	for _, sf := range agentDef.SeedFiles {
		if shouldSkipSeedFile(sf, hasAPIKey) {
			continue
		}

		data, ok, err := loadSeedFileData(sf, homeDir)
		if err != nil {
			return copiedAuth, err
		}
		if !ok {
			continue
		}

		baseDir := agentStateDir
		if sf.HomeDir {
			baseDir = homeSeedDir
		}
		targetPath := filepath.Join(baseDir, sf.TargetPath)

		if err := fileutil.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
			return copiedAuth, fmt.Errorf("create dir for %s: %w", sf.TargetPath, err)
		}
		if err := fileutil.WriteFile(targetPath, data, 0600); err != nil { //nolint:gosec // G703: targetPath is constructed from internal agent config, not user input
			return copiedAuth, fmt.Errorf("write %s: %w", targetPath, err)
		}
		if sf.AuthOnly {
			copiedAuth = true
		}
	}

	return copiedAuth, nil
}

// shouldSkipSeedFile returns true if the seed file should be skipped.
func shouldSkipSeedFile(sf agent.SeedFile, hasAPIKey bool) bool {
	if !sf.AuthOnly {
		return false
	}
	if len(sf.OwnerAPIKeys) > 0 {
		// Per-file API key check (used by shell agent): skip if any key is set
		for _, key := range sf.OwnerAPIKeys {
			if os.Getenv(key) != "" { //nolint:forbidigo // §12: agent API-key presence check (declared exception)
				return true
			}
		}
		return false
	}
	return hasAPIKey // auth file not needed when API key is set
}

// loadSeedFileData reads data from the host file or keychain for a seed file.
// Returns (data, true, nil) if found, (nil, false, nil) if not found, or (nil, false, err) on error.
// homeDir is used for ~ expansion in seed file host paths.
func loadSeedFileData(sf agent.SeedFile, homeDir string) ([]byte, bool, error) {
	hostPath := config.ExpandTilde(sf.HostPath, homeDir)
	if _, err := os.Stat(hostPath); err == nil {
		data, readErr := os.ReadFile(hostPath) //nolint:gosec // G304: path is from agent definition, not user input
		if readErr != nil {
			return nil, false, fmt.Errorf("read %s: %w", hostPath, readErr)
		}
		return data, true, nil
	}
	if sf.KeychainService != "" {
		data, keychainErr := KeychainReader(sf.KeychainService)
		if keychainErr == nil {
			return data, true, nil
		}
	}
	return nil, false, nil
}

// EnsureContainerSettings merges required container settings into agent-state/settings.json.
// Agent-specific adjustments are driven by each agent's ApplySettings field.
// Shell agents (SeedsAllAgents=true) apply each real agent's settings into
// home-seed subdirectories instead.
func EnsureContainerSettings(agentDef *agent.Definition, sandboxDir string, isolation runtime.IsolationMode) error {
	if agentDef.SeedsAllAgents {
		return ensureShellContainerSettings(sandboxDir, isolation)
	}

	if agentDef.StateDir == "" || agentDef.ApplySettings == nil {
		return nil
	}

	// Use restrictive permissions by default, world-writable only for container-enhanced (gVisor)
	perms := state.Perms(isolation)

	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	if err := fileutil.MkdirAllPerm(agentStateDir, perms.Dir); err != nil {
		return fmt.Errorf("create %s dir: %w", store.AgentRuntimeDir, err)
	}
	settingsPath := filepath.Join(agentStateDir, "settings.json")

	settings, err := fileutil.ReadJSONMap(settingsPath)
	if err != nil {
		return err
	}
	agentDef.ApplySettings(settings)
	return fileutil.WriteJSONMap(settingsPath, settings)
}

// ensureShellContainerSettings applies each real agent's container settings
// to its home-seed subdirectory (e.g., home-seed/.claude/settings.json).
func ensureShellContainerSettings(sandboxDir string, _ runtime.IsolationMode) error {
	for _, name := range agent.RealAgents() {
		def := agent.GetAgent(name)
		if def.StateDir == "" || def.ApplySettings == nil {
			continue
		}
		dirBase := filepath.Base(def.StateDir)
		dirPath := filepath.Join(sandboxDir, "home-seed", dirBase)
		settingsPath := filepath.Join(dirPath, "settings.json")

		if err := fileutil.MkdirAll(dirPath, 0750); err != nil {
			return fmt.Errorf("create %s dir: %w", dirBase, err)
		}
		settings, err := fileutil.ReadJSONMap(settingsPath)
		if err != nil {
			return err
		}
		def.ApplySettings(settings)
		if err := fileutil.WriteJSONMap(settingsPath, settings); err != nil {
			return err
		}
	}
	return nil
}

// ensureHomeSeedConfig patches home-seed/.claude.json so its installMethod
// matches how the backend actually installed Claude Code (installMethod is the
// backend's AgentInstallMethod — "npm-global" for the container backends,
// "native" for Tart). The seeded file comes from the host, which usually says
// "native"; when the backend installs via npm, a mismatch makes Claude Code
// emit spurious warnings about a missing ~/.local/bin/claude and PATH
// misconfiguration. Writing the backend's real method keeps them consistent.
func ensureHomeSeedConfig(agentDef *agent.Definition, sandboxDir, installMethod string) error {
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

	cfg, err := fileutil.ReadJSONMap(configPath)
	if err != nil {
		return err
	}

	cfg["installMethod"] = installMethod

	return fileutil.WriteJSONMap(configPath, cfg)
}

// SeedSandbox copies seed files, agent config files, and seeds the home config.
// Returns agentFilesInitialized so the caller can persist it to SandboxState.
// homeDir is used for ~ expansion in seed file host paths.
func SeedSandbox(rt runtime.Runtime, agentDef *agent.Definition, sandboxDir string, isolation runtime.IsolationMode, agentFiles *config.AgentFilesConfig, credOverrides map[string]string, homeDir string, env map[string]string, output io.Writer) (agentFilesInitialized bool, err error) {
	// Copy seed files into agent-state (config, OAuth credentials, etc.)
	hasAPIKey := HasAnyAPIKey(agentDef, credOverrides)
	copiedAuth, err := CopySeedFiles(agentDef, sandboxDir, hasAPIKey, homeDir)
	if err != nil {
		return false, fmt.Errorf("copy seed files: %w", err)
	}

	// Warn when an agent is using short-lived OAuth credentials instead of a long-lived token.
	if agentDef.ShortLivedOAuthWarning && copiedAuth {
		fmt.Fprintln(output, "Warning: using OAuth credentials from ~/.claude/.credentials.json")                         //nolint:errcheck // best-effort warning
		fmt.Fprintln(output, "  These tokens expire after ~30 minutes and may fail in long-running sessions.")            //nolint:errcheck // best-effort warning
		fmt.Fprintln(output, "  For reliable auth, run 'claude setup-token' and export CLAUDE_CODE_OAUTH_TOKEN instead.") //nolint:errcheck // best-effort warning
		fmt.Fprintln(output)                                                                                              //nolint:errcheck // best-effort warning
	}

	// Ensure container-required settings (e.g., skip bypass permissions prompt)
	if err := EnsureContainerSettings(agentDef, sandboxDir, isolation); err != nil {
		return false, fmt.Errorf("ensure container settings: %w", err)
	}

	// Copy agent_files (user-configured agent config files)
	if agentFiles != nil && agentDef.StateDir != "" {
		if err := CopyAgentFiles(agentDef, sandboxDir, agentFiles, homeDir, env); err != nil {
			return false, fmt.Errorf("copy agent files: %w", err)
		}
		agentFilesInitialized = true
	}

	// Fix install method in seeded .claude.json so it matches how this backend
	// installed Claude Code. Skipped for process-based backends that run the
	// host's native agent installation.
	if desc := rt.Descriptor(); desc.AgentProvisionedByBackend {
		if err := ensureHomeSeedConfig(agentDef, sandboxDir, desc.AgentInstallMethod); err != nil {
			return false, fmt.Errorf("ensure home seed config: %w", err)
		}
	}

	return agentFilesInitialized, nil
}

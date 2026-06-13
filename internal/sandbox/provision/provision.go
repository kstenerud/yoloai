// ABOUTME: Seed-file and credential provisioning for a sandbox before first launch:
// ABOUTME: secret-dir creation, auth checks, and settings injection.
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
	"github.com/kstenerud/yoloai/internal/store"
)

// CreateSecretsDir creates a temp directory with one file per env var / API key.
// configEnv (the ${VAR}-expanded profile env) is written first; the agent's API-key
// and auth-hint values are then resolved from hostEnv (the caller-supplied host
// environment snapshot) and overwrite on conflict (take precedence). hostEnv is the
// sole credential source — the library never reads os.Environ (§12). The directory
// is created under stagingRoot; "" means the OS default temp dir (os.TempDir()), so
// an embedder can stage a principal's plaintext credentials on a per-principal tmpfs.
// Returns empty string if nothing was written.
func CreateSecretsDir(agentDef *agent.Definition, configEnv map[string]string, hostEnv config.Layout, stagingRoot string) (string, error) {
	if len(agentDef.APIKeyEnvVars) == 0 && len(agentDef.AuthHintEnvVars) == 0 && len(configEnv) == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp(stagingRoot, "yoloai-secrets-*")
	if err != nil {
		return "", fmt.Errorf("create secrets temp dir: %w", err)
	}

	// Owner-only perms: the container runs as the invoking host UID (the staging
	// owner) in every isolation mode, so 0700/0600 is both readable by the
	// sandbox and denied to other local users — see DF20.
	perms := state.Perms()

	if err := os.Chmod(tmpDir, perms.SecretsDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("chmod secrets dir: %w", err)
	}
	// When running via sudo, chown the dir to the real user so the container
	// process (running as that user) can read it.
	_ = fileutil.ChownIfSudo(tmpDir) //nolint:errcheck // best-effort; individual files are already chowned by WriteFilePerm

	wrote := false

	// Write config (profile) env vars first.
	for k, v := range configEnv {
		if err := fileutil.WriteFilePerm(filepath.Join(tmpDir, k), []byte(v), perms.SecretsFile); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write env %s: %w", k, err)
		}
		wrote = true
	}

	// Write host env values for API keys and auth hints (overwrites config env on
	// conflict). EnvForAgentCredentials yields the present, non-empty subset of
	// the agent's declared credential keys from the threaded host-env snapshot.
	for key, value := range hostEnv.Env().EnvForAgentCredentials(append(agentDef.APIKeyEnvVars, agentDef.AuthHintEnvVars...)) {
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

// HasAnyAPIKey returns true if any of the agent's required API key env vars are
// present in hostEnv (the caller-supplied host-environment snapshot).
func HasAnyAPIKey(agentDef *agent.Definition, hostEnv config.Layout) bool {
	if len(agentDef.APIKeyEnvVars) == 0 {
		return true // no API key required
	}
	return len(hostEnv.Env().EnvForAgentCredentials(agentDef.APIKeyEnvVars)) > 0
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
// in hostEnv (the caller-supplied host-environment snapshot) or in the config
// env map. This allows agents like aider to work with local model servers
// (Ollama, LM Studio) without a cloud API key.
func HasAnyAuthHint(agentDef *agent.Definition, configEnv map[string]string, hostEnv config.Layout) bool {
	hostCreds := hostEnv.Env().EnvForAgentCredentials(agentDef.AuthHintEnvVars)
	for _, key := range agentDef.AuthHintEnvVars {
		if hostCreds[key] != "" || configEnv[key] != "" {
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
func CopySeedFiles(agentDef *agent.Definition, sandboxDir string, hasAPIKey bool, homeDir string, hostEnv config.Layout) (bool, error) {
	copiedAuth := false
	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")

	for _, sf := range agentDef.SeedFiles {
		if shouldSkipSeedFile(sf, hasAPIKey, hostEnv) {
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
		// Executable seeds (e.g. Claude Code's statusLine script, which it execs
		// by path) get 0700 via WriteFilePerm, which chmods past the umask so the
		// exec bit survives into the bind-mounted, possibly-different-uid sandbox.
		// Everything else (credentials, config) stays 0600.
		var writeErr error
		if sf.Executable {
			writeErr = fileutil.WriteFilePerm(targetPath, data, 0700)
		} else {
			writeErr = fileutil.WriteFile(targetPath, data, 0600) //nolint:gosec // G703: targetPath is constructed from internal agent config, not user input
		}
		if writeErr != nil {
			return copiedAuth, fmt.Errorf("write %s: %w", targetPath, writeErr)
		}
		if sf.AuthOnly {
			copiedAuth = true
		}
	}

	return copiedAuth, nil
}

// shouldSkipSeedFile returns true if the seed file should be skipped.
func shouldSkipSeedFile(sf agent.SeedFile, hasAPIKey bool, hostEnv config.Layout) bool {
	if !sf.AuthOnly {
		return false
	}
	if len(sf.OwnerAPIKeys) > 0 {
		// Per-file API key check (used by shell agent): skip if any key is set
		return len(hostEnv.Env().EnvForAgentCredentials(sf.OwnerAPIKeys)) > 0
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

	perms := state.Perms()

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
// hostEnv supplies both the agent-credential lookups (HasAnyAPIKey/CopySeedFiles)
// and, via its curated interpolation map, the ${VAR} expansion in CopyAgentFiles.
func SeedSandbox(rt runtime.Backend, agentDef *agent.Definition, sandboxDir string, isolation runtime.IsolationMode, agentFiles *config.AgentFilesConfig, homeDir string, hostEnv config.Layout, output io.Writer) (agentFilesInitialized bool, err error) {
	// Copy seed files into agent-state (config, OAuth credentials, etc.)
	hasAPIKey := HasAnyAPIKey(agentDef, hostEnv)
	copiedAuth, err := CopySeedFiles(agentDef, sandboxDir, hasAPIKey, homeDir, hostEnv)
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
		if err := CopyAgentFiles(agentDef, sandboxDir, agentFiles, homeDir, hostEnv.Env().EnvForConfigInterpolation()); err != nil {
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

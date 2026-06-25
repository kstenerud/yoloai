// ABOUTME: Seed-file and credential provisioning for a sandbox before first launch:
// ABOUTME: secret-dir creation, auth checks, and settings injection.
package envsetup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/store"
)

// ResolveSecretEnv returns the resolved secret key->value map for a sandbox:
// the profile/config env (configEnv) overlaid by the agent's declared
// credential keys resolved against the caller's host-env snapshot (the latter
// wins on conflict). Returns a nil/empty map when there is nothing to deliver.
// This is the single source of "what the secrets are"; the transport (staged
// files for the legacy path, process env for the Launch path) is the caller's.
func ResolveSecretEnv(spec EnvSpec, configEnv map[string]string, hostEnv config.Layout) map[string]string {
	out := make(map[string]string, len(configEnv))
	for k, v := range configEnv {
		out[k] = v
	}
	for k, v := range hostEnv.Env().EnvForAgentCredentials(append(spec.APIKeyEnvVars, spec.AuthHintEnvVars...)) {
		out[k] = v
	}
	return out
}

// CreateSecretsDir creates a temp directory with one file per env var / API key.
// configEnv (the ${VAR}-expanded profile env) is written first; the agent's API-key
// and auth-hint values are then resolved from hostEnv (the caller-supplied host
// environment snapshot) and overwrite on conflict (take precedence). hostEnv is the
// sole credential source — the library never reads os.Environ (§12). The directory
// is created under stagingRoot; "" means the OS default temp dir (os.TempDir()), so
// an embedder can stage a principal's plaintext credentials on a per-principal tmpfs.
// Returns empty string if nothing was written.
func CreateSecretsDir(spec EnvSpec, configEnv map[string]string, hostEnv config.Layout, stagingRoot string) (string, error) {
	m := ResolveSecretEnv(spec, configEnv, hostEnv)
	if len(m) == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp(stagingRoot, "yoloai-secrets-*")
	if err != nil {
		return "", fmt.Errorf("create secrets temp dir: %w", err)
	}

	// Owner-only perms: the container runs as the invoking host UID (the staging
	// owner) in every isolation mode, so 0700/0600 is both readable by the
	// sandbox and denied to other local users — see DF20.
	perms := store.Perms()

	if err := os.Chmod(tmpDir, perms.SecretsDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("chmod secrets dir: %w", err)
	}
	// When running via sudo, chown the dir to the real user so the container
	// process (running as that user) can read it.
	_ = fileutil.ChownIfSudo(tmpDir) //nolint:errcheck // best-effort; individual files are already chowned by WriteFilePerm

	for k, v := range m {
		if err := fileutil.WriteFilePerm(filepath.Join(tmpDir, k), []byte(v), perms.SecretsFile); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write secret %s: %w", k, err)
		}
	}

	return tmpDir, nil
}

// HasAnyAPIKey returns true if any of the agent's required API key env vars are
// present in hostEnv (the caller-supplied host-environment snapshot).
func HasAnyAPIKey(spec EnvSpec, hostEnv config.Layout) bool {
	if len(spec.APIKeyEnvVars) == 0 {
		return true // no API key required
	}
	return len(hostEnv.Env().EnvForAgentCredentials(spec.APIKeyEnvVars)) > 0
}

// HasAnyAuthFile returns true if any auth-only seed files exist on disk
// or can be read from the macOS Keychain.
// homeDir is used for ~ expansion in seed file host paths.
func HasAnyAuthFile(spec EnvSpec, homeDir string) bool {
	for _, sf := range spec.SeedFiles {
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
func HasAnyAuthHint(spec EnvSpec, configEnv map[string]string, hostEnv config.Layout) bool {
	hostCreds := hostEnv.Env().EnvForAgentCredentials(spec.AuthHintEnvVars)
	for _, key := range spec.AuthHintEnvVars {
		if hostCreds[key] != "" || configEnv[key] != "" {
			return true
		}
	}
	return false
}

// DescribeSeedAuthFiles returns a human-readable description of expected auth file paths.
func DescribeSeedAuthFiles(spec EnvSpec) string {
	var paths []string
	for _, sf := range spec.SeedFiles {
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
func CopySeedFiles(spec EnvSpec, sandboxDir string, hasAPIKey bool, homeDir string, hostEnv config.Layout) (bool, error) {
	copiedAuth := false
	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")

	for _, sf := range spec.SeedFiles {
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
func shouldSkipSeedFile(sf SeedFile, hasAPIKey bool, hostEnv config.Layout) bool {
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
func loadSeedFileData(sf SeedFile, homeDir string) ([]byte, bool, error) {
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

// EnsureContainerSettings applies each resolved settings patch to its
// settings.json (creating the dir as needed). The patch list is compiled
// upstream (envspec.BuildEnvSpec); an empty list is a no-op.
func EnsureContainerSettings(sandboxDir string, patches []SettingsPatch) error {
	for _, p := range patches {
		dir := filepath.Join(sandboxDir, p.RelDir)
		if err := fileutil.MkdirAllPerm(dir, p.DirPerm); err != nil {
			return fmt.Errorf("create settings dir %s: %w", p.RelDir, err)
		}
		settingsPath := filepath.Join(dir, "settings.json")
		settings, err := fileutil.ReadJSONMap(settingsPath)
		if err != nil {
			return err
		}
		p.Apply(settings)
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
func ensureHomeSeedConfig(spec EnvSpec, sandboxDir, installMethod string) error {
	// Only relevant for agents that seed .claude.json into HomeDir
	var hasHomeSeed bool
	for _, sf := range spec.SeedFiles {
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
func SeedSandbox(spec EnvSpec, sandboxDir string, agentFiles *config.AgentFilesConfig, homeDir string, hostEnv config.Layout, provisionedByBackend bool, installMethod string, output io.Writer) (agentFilesInitialized bool, err error) {
	hasAPIKey := HasAnyAPIKey(spec, hostEnv)
	copiedAuth, err := CopySeedFiles(spec, sandboxDir, hasAPIKey, homeDir, hostEnv)
	if err != nil {
		return false, fmt.Errorf("copy seed files: %w", err)
	}

	if spec.ShortLivedOAuthWarning && copiedAuth {
		fmt.Fprintln(output, "Warning: using OAuth credentials from ~/.claude/.credentials.json")                         //nolint:errcheck // best-effort warning
		fmt.Fprintln(output, "  These tokens expire after ~30 minutes and may fail in long-running sessions.")            //nolint:errcheck // best-effort warning
		fmt.Fprintln(output, "  For reliable auth, run 'claude setup-token' and export CLAUDE_CODE_OAUTH_TOKEN instead.") //nolint:errcheck // best-effort warning
		fmt.Fprintln(output)                                                                                              //nolint:errcheck // best-effort warning
	}

	if err := EnsureContainerSettings(sandboxDir, spec.SettingsPatches); err != nil {
		return false, fmt.Errorf("ensure container settings: %w", err)
	}

	if agentFiles != nil && spec.HasStateDir {
		if err := CopyAgentFiles(spec, sandboxDir, agentFiles, homeDir, hostEnv.Env().EnvForConfigInterpolation()); err != nil {
			return false, fmt.Errorf("copy agent files: %w", err)
		}
		agentFilesInitialized = true
	}

	if provisionedByBackend {
		if err := ensureHomeSeedConfig(spec, sandboxDir, installMethod); err != nil {
			return false, fmt.Errorf("ensure home seed config: %w", err)
		}
	}

	return agentFilesInitialized, nil
}

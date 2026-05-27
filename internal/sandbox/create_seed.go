// ABOUTME: seedSandbox copies agent credentials, settings, and agent_files into
// ABOUTME: the sandbox state directory before the container is first launched.
package sandbox

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// seedSandbox copies seed files, agent config files, and seeds the home config.
// Returns agentFilesInitialized so the caller can persist it to SandboxState.
// Extracted from prepareSandboxState().
// homeDir is used for ~ expansion in seed file host paths.
func (m *Manager) seedSandbox(agentDef *agent.Definition, sandboxDir string, isolation runtime.IsolationMode, agentFiles *config.AgentFilesConfig, credOverrides map[string]string, homeDir string) (agentFilesInitialized bool, err error) {
	// Copy seed files into agent-state (config, OAuth credentials, etc.)
	hasAPIKey := hasAnyAPIKey(agentDef, credOverrides)
	copiedAuth, err := copySeedFiles(agentDef, sandboxDir, hasAPIKey, homeDir)
	if err != nil {
		return false, fmt.Errorf("copy seed files: %w", err)
	}

	// Warn when an agent is using short-lived OAuth credentials instead of a long-lived token.
	if agentDef.ShortLivedOAuthWarning && copiedAuth {
		fmt.Fprintln(m.output, "Warning: using OAuth credentials from ~/.claude/.credentials.json")                         //nolint:errcheck // best-effort warning
		fmt.Fprintln(m.output, "  These tokens expire after ~30 minutes and may fail in long-running sessions.")            //nolint:errcheck // best-effort warning
		fmt.Fprintln(m.output, "  For reliable auth, run 'claude setup-token' and export CLAUDE_CODE_OAUTH_TOKEN instead.") //nolint:errcheck // best-effort warning
		fmt.Fprintln(m.output)                                                                                              //nolint:errcheck // best-effort warning
	}

	// Ensure container-required settings (e.g., skip bypass permissions prompt)
	if err := ensureContainerSettings(agentDef, sandboxDir, isolation); err != nil {
		return false, fmt.Errorf("ensure container settings: %w", err)
	}

	// Copy agent_files (user-configured agent config files)
	if agentFiles != nil && agentDef.StateDir != "" {
		if err := copyAgentFiles(agentDef, sandboxDir, agentFiles, homeDir); err != nil {
			return false, fmt.Errorf("copy agent files: %w", err)
		}
		agentFilesInitialized = true
	}

	// Fix install method in seeded .claude.json (host has "native", container uses npm).
	// Skipped for process-based backends that run the host's native agent installation.
	if m.runtime.Descriptor().AgentProvisionedByBackend {
		if err := ensureHomeSeedConfig(agentDef, sandboxDir); err != nil {
			return false, fmt.Errorf("ensure home seed config: %w", err)
		}
	}

	return agentFilesInitialized, nil
}

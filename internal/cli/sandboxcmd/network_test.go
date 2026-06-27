package sandboxcmd

// ABOUTME: Shared fixture for the allow/allowed/deny CLI tests.
// ABOUTME: createNetworkSandbox writes a fake sandbox dir; library-side
// ABOUTME: Network behavior is tested at the yoloai root in network_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/clitest"
	"github.com/kstenerud/yoloai/internal/netpolicycfg"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/require"
)

// createNetworkSandbox writes a fake sandbox directory (environment.json +
// netpolicy.json + runtime-config.json) suitable for end-to-end CLI tests of
// allow/allowed/deny. Returns the sandbox directory path.
//
// Network policy (mode/allow) goes to netpolicy.json (D90), not
// environment.json.
func createNetworkSandbox(t *testing.T, name, networkMode string, domains []string) string {
	t.Helper()
	tmpHome := clitest.Home(t)

	sandboxDir := filepath.Join(tmpHome, ".yoloai", "library", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:        name,
		BackendType: "docker",
		Dirs:        []store.DirEnvironment{{HostPath: "/tmp/test", MountPath: "/tmp/test", Mode: "copy"}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	// Network policy lives in netpolicy.json (D90).
	require.NoError(t, netpolicycfg.Save(sandboxDir, &netpolicycfg.Netpolicy{Mode: networkMode, Allow: domains}))

	// Minimal runtime-config.json so the library's
	// PatchConfigAllowedDomains has a target to update.
	cfg := map[string]any{
		"host_uid":        1000,
		"host_gid":        1000,
		"agent_command":   "bash",
		"working_dir":     "/tmp/test",
		"allowed_domains": domains,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), data, 0600))

	return sandboxDir
}

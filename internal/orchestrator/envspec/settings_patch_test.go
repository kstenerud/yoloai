// ABOUTME: Apply-side tests for settingsPatches — EnsureContainerSettings must
// ABOUTME: merge each patch into the agent's existing config, not clobber it.
// (The routing side — which StateRelPath each patch resolves to — lives in
// TestBuildEnvSpec_ShellAgent.)
package envspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/store"
)

// Confirm that the SettingsFileName and ApplySettings patches for a fake agent
// are **merged** (by EnsureContainerSettings) into the existing user settings.
func TestSettingsPatches_AppliesByMerging(t *testing.T) {
	tests := []struct {
		name             string
		settingsFileName string
		expectedFileName string
	}{
		{name: "default file name", settingsFileName: "", expectedFileName: "settings.json"},         // e.g. Claude Code and Gemini
		{name: "explicit file name", settingsFileName: "hooks.json", expectedFileName: "hooks.json"}, // e.g. Codex
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sandboxDir := t.TempDir()

			fakeAgentDef := &agent.Definition{
				StateDir:         "/home/yoloai/.fake-agent/",
				SettingsFileName: test.settingsFileName,
				ApplySettings: func(s map[string]any) {
					s["patched"] = true
				},
			}

			settingsDir := store.AgentRuntimePath(sandboxDir)
			require.NoError(t, os.MkdirAll(settingsDir, 0o750))
			settingsPath := filepath.Join(settingsDir, test.expectedFileName)
			require.NoError(t, os.WriteFile(settingsPath, []byte(`{"userSetting":"kept"}`), 0o600))

			patches := settingsPatches(fakeAgentDef)
			require.Len(t, patches, 1)
			require.NoError(t, envsetup.EnsureContainerSettings(sandboxDir, patches))

			content := readJSONFile(t, settingsPath)
			assert.Equal(t, "kept", content["userSetting"], "Unrelated user settings must survive the patch")
			assert.Equal(t, true, content["patched"], "ApplySettings updates the settings file")
		})
	}
}

func readJSONFile(t *testing.T, absPath string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(absPath) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, err)
	content := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &content))
	return content
}

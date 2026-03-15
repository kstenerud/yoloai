//go:build e2e

package e2e_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_JSONLs verifies that '--json ls' emits a valid JSON object with sandboxes list.
func TestE2E_JSONLs(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-jsonls", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-jsonls") })

	stdout, _, code := runYoloai(t, "--json", "ls")
	require.Equal(t, 0, code)

	var result struct {
		Sandboxes           []map[string]any `json:"sandboxes"`
		UnavailableBackends []string         `json:"unavailable_backends"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &result), "output should be valid JSON object")
	assert.GreaterOrEqual(t, len(result.Sandboxes), 1)

	// Each entry has shape {"meta": {"name": "..."}, ...}
	found := false
	for _, entry := range result.Sandboxes {
		if meta, ok := entry["meta"].(map[string]any); ok {
			if meta["name"] == "e2e-jsonls" {
				found = true
				break
			}
		}
	}
	assert.True(t, found, "e2e-jsonls should appear in JSON ls output")
}

// TestE2E_JSONAllowed verifies that '--json sandbox NAME allowed' emits valid JSON
// with the expected shape.
func TestE2E_JSONAllowed(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "--network-isolated", "e2e-jsonnet", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-jsonnet") })

	stdout, _, code := runYoloai(t, "--json", "sandbox", "e2e-jsonnet", "allowed")
	require.Equal(t, 0, code)

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result), "output should be valid JSON object")
	assert.Equal(t, "e2e-jsonnet", result["name"])
	assert.Equal(t, "isolated", result["network_mode"])
}

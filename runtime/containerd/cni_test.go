package containerdrt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCNIStatePath verifies the CNI state file path is within the backend dir.
func TestCNIStatePath(t *testing.T) {
	path := cniStatePath("/home/user/.yoloai/sandboxes/mybox")
	assert.Equal(t, "/home/user/.yoloai/sandboxes/mybox/backend/cni-state.json", path)
}

// TestTeardownCNI_MissingState verifies teardownCNI is a no-op if cni-state.json is absent.
func TestTeardownCNI_MissingState(t *testing.T) {
	dir := t.TempDir()
	// No backend/ subdir or cni-state.json — should return nil without error.
	err := teardownCNI(context.Background(), dir)
	assert.NoError(t, err)
}

// TestTeardownCNI_Idempotent verifies a second teardown after the state file is removed is a no-op.
func TestTeardownCNI_Idempotent(t *testing.T) {
	dir := t.TempDir()
	backendDir := filepath.Join(dir, "backend")
	require.NoError(t, os.MkdirAll(backendDir, 0o750))

	// Write a minimal state file.
	state := cniState{
		NetnsName: "yoloai-test",
		NetnsPath: "/var/run/netns/yoloai-test",
		Interface: "eth0",
		IP:        "10.88.0.5/16",
	}
	data, err := json.MarshalIndent(state, "", "  ")
	require.NoError(t, err)
	statePath := filepath.Join(backendDir, cniStateFileName)
	require.NoError(t, os.WriteFile(statePath, data, 0o600))

	// teardownCNI will fail on CNI DEL (no real CNI plugins) and netns delete
	// (no real netns), but it should still remove the state file.
	// We verify idempotency by calling twice — both calls should not error on
	// the missing state file case.
	_ = teardownCNI(context.Background(), dir) // first call: state exists, CNI ops may fail
	// State file should be removed after first call regardless of CNI/netns errors.
	_, statErr := os.Stat(statePath)
	assert.True(t, os.IsNotExist(statErr), "state file should be removed after teardown")

	// Second call: no state file — must return nil.
	err = teardownCNI(context.Background(), dir)
	assert.NoError(t, err)
}

// TestEnsureCNIConflist writes the conflist to a temp dir.
func TestEnsureCNIConflist(t *testing.T) {
	// Override the CNI conf dir by pointing to a temp dir.
	// We can't easily override cniConfDir() directly, so just test the file
	// is valid JSON by parsing it.
	var parsed map[string]interface{}
	err := json.Unmarshal([]byte(cniConflistTemplate), &parsed)
	assert.NoError(t, err, "cniConflistTemplate should be valid JSON")
	assert.Equal(t, "yoloai", parsed["name"])
}

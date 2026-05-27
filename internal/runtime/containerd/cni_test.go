//go:build linux

package containerdrt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noLayout is a placeholder layout used by tests that don't actually
// invoke any Layout method (the constant exists so test setups compile
// uniformly). F14: NewLayout panics on empty, so this uses a dummy
// non-empty path.
var noLayout = config.NewLayout("/tmp/yoloai-containerd-test")

// TestCNIStatePath verifies the CNI state file path is within the backend dir.
func TestCNIStatePath(t *testing.T) {
	path := cniStatePath("/home/user/.yoloai/sandboxes/mybox")
	assert.Equal(t, "/home/user/.yoloai/sandboxes/mybox/backend/cni-state.json", path)
}

// TestTeardownCNI_MissingState verifies teardownCNI is a no-op if cni-state.json is absent.
func TestTeardownCNI_MissingState(t *testing.T) {
	dir := t.TempDir()
	// No backend/ subdir or cni-state.json — should return nil without error.
	err := teardownCNI(context.Background(), noLayout, dir)
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
	_ = teardownCNI(context.Background(), noLayout, dir) // first call: state exists, CNI ops may fail
	// State file should be removed after first call regardless of CNI/netns errors.
	_, statErr := os.Stat(statePath)
	assert.True(t, os.IsNotExist(statErr), "state file should be removed after teardown")

	// Second call: no state file — must return nil.
	err = teardownCNI(context.Background(), noLayout, dir)
	assert.NoError(t, err)
}

// TestCNIForwardHasIP verifies the pure parsing helper detects ACCEPT rules
// containing the /32 host form of the IP, and rejects unrelated chain dumps.
// The realistic positive sample mirrors `iptables -S CNI-FORWARD` output
// captured during a healthy run; the negative sample mirrors the DF9
// signature (POSTROUTING masquerade present, CNI-FORWARD ACCEPT absent).
func TestCNIForwardHasIP(t *testing.T) {
	healthy := `-N CNI-FORWARD
-A CNI-FORWARD -j CNI-ADMIN
-A CNI-FORWARD -d 10.89.1.90/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A CNI-FORWARD -s 10.89.1.90/32 -j ACCEPT
-A CNI-FORWARD -d 10.89.1.88/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A CNI-FORWARD -s 10.89.1.88/32 -j ACCEPT
`
	assert.True(t, cniForwardHasIP(healthy, "10.89.1.90"))
	assert.True(t, cniForwardHasIP(healthy, "10.89.1.88"))

	// Sibling-only chain: 10.89.1.88 present, 10.89.1.90 missing (DF9 case).
	siblingOnly := `-N CNI-FORWARD
-A CNI-FORWARD -j CNI-ADMIN
-A CNI-FORWARD -d 10.89.1.88/32 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A CNI-FORWARD -s 10.89.1.88/32 -j ACCEPT
`
	assert.False(t, cniForwardHasIP(siblingOnly, "10.89.1.90"))

	// Substring guard: 10.89.1.9 must NOT match 10.89.1.90/32.
	assert.False(t, cniForwardHasIP(healthy, "10.89.1.9"))

	// Drop rules (no ACCEPT) must not satisfy the check.
	dropOnly := `-A CNI-FORWARD -s 10.89.1.90/32 -j DROP
`
	assert.False(t, cniForwardHasIP(dropOnly, "10.89.1.90"))

	assert.False(t, cniForwardHasIP("", "10.89.1.90"))
}

// TestEnsureCNIConflist writes the conflist to a temp dir.
func TestEnsureCNIConflist(t *testing.T) {
	// Override the CNI conf dir by pointing to a temp dir.
	// We can't easily override cniConfDir() directly, so just test the file
	// is valid JSON by parsing it.
	var parsed map[string]any
	err := json.Unmarshal([]byte(cniConflistTemplate), &parsed)
	assert.NoError(t, err, "cniConflistTemplate should be valid JSON")
	assert.Equal(t, "yoloai", parsed["name"])
}

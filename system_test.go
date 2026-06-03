// ABOUTME: Tests for System cross-backend introspection (Info / Backends).

package yoloai

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemClient_Info verifies paths are derived from the layout and that the
// backend probe returns exactly one status per registered backend (names in
// registration order; unavailable backends carry a reason).
func TestSystemClient_Info(t *testing.T) {
	c := newTestClient(t)

	info, err := c.Info(context.Background())
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, c.layout.YoloaiDir(), info.DataDir)
	assert.Equal(t, c.layout.SandboxesDir(), info.SandboxesDir)
	assert.Equal(t, c.layout.GlobalConfigPath(), info.GlobalConfig)
	assert.Equal(t, c.layout.DefaultsConfigPath(), info.DefaultsConfig)

	descs := runtime.Descriptors()
	require.Len(t, info.Backends, len(descs), "one BackendInfo per registered backend")
	for i, b := range info.Backends {
		assert.Equal(t, descs[i].Type, b.Type, "backend statuses preserve registration order")
		if !b.Available {
			assert.NotEmpty(t, b.Note, "an unavailable backend must explain why")
		}
	}
}

// TestClient_Principal threads ClientConfiguration.Principal into the layout
// (default "" stays default; a valid segment parses; an invalid one is a
// *UsageError).
func TestClient_Principal(t *testing.T) {
	root := t.TempDir()

	def, err := NewClient(context.Background(), ClientConfiguration{DataDir: root, HomeDir: root})
	require.NoError(t, err)
	assert.Equal(t, config.PrincipalSegment(""), def.layout.Principal)

	acme, err := NewClient(context.Background(), ClientConfiguration{DataDir: root, HomeDir: root, Principal: "acme"})
	require.NoError(t, err)
	assert.Equal(t, config.PrincipalSegment("acme"), acme.layout.Principal)

	_, err = NewClient(context.Background(), ClientConfiguration{DataDir: root, HomeDir: root, Principal: "way-too-long-and-invalid"})
	require.Error(t, err)
	var usageErr *yoerrors.UsageError
	assert.ErrorAs(t, err, &usageErr)
}

// TestSystemClient_ValidateSandboxName accepts a well-formed name and rejects
// path-traversal, with no host state consulted.
func TestSystemClient_ValidateSandboxName(t *testing.T) {
	c := newTestClient(t)
	assert.NoError(t, c.ValidateSandboxName("my-box"))
	assert.Error(t, c.ValidateSandboxName("../escape"))
}

// TestSandbox_MissingReturnsNotFound returns ErrSandboxNotFound for a sandbox
// whose directory does not exist — obtaining the handle IS the existence check.
func TestSandbox_MissingReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	c, err := NewClient(context.Background(), ClientConfiguration{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = c.Sandbox("nope")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

// TestSystemClient_ListAcrossBackends_Empty verifies a fresh install (no sandbox
// dirs) lists nothing and probes no backends — no enumeration, no error.
func TestSystemClient_ListAcrossBackends_Empty(t *testing.T) {
	c := newTestClient(t)
	infos, unavailable, err := c.ListAcrossBackends(context.Background())
	require.NoError(t, err)
	assert.Empty(t, infos)
	assert.Empty(t, unavailable)
}

// TestSystemClient_Doctor verifies every registered backend produces at least
// one report row (base-mode or init-failure), and that a non-matching backend
// filter yields nothing.
func TestSystemClient_Doctor(t *testing.T) {
	c := newTestClient(t)

	reports, err := c.Doctor(context.Background(), DoctorOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, reports, "every registered backend produces at least one report row")
	for _, r := range reports {
		assert.NotEmpty(t, r.Backend, "each report names its backend")
	}

	none, err := c.Doctor(context.Background(), DoctorOptions{BackendFilter: "does-not-exist"})
	require.NoError(t, err)
	assert.Empty(t, none, "a non-matching backend filter reports nothing")
}

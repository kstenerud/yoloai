// ABOUTME: Unit tests for the public creation surface (F1/F3): toInternal
// ABOUTME: mapping, RunOptions.materialize sugar, port formatting, and the
// ABOUTME: A2/A3 lazy-backend / optional-Backend contract at construction.

package yoloai

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/yoerrors"
)

func TestFormatPorts(t *testing.T) {
	assert.Nil(t, formatPorts(nil))
	got := formatPorts([]PortMapping{
		{HostPort: 3000, ContainerPort: 3000},
		{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
	})
	assert.Equal(t, []string{"3000:3000", "8080:80"}, got)
}

func TestCreateOptions_toInternal_DefaultsModeAndFoldsAck(t *testing.T) {
	o := CreateOptions{
		Name:                 "box",
		Workdir:              DirSpec{Path: "/p"}, // Mode empty → defaults to copy
		AgentType:            AgentClaude,
		Ports:                []PortMapping{{HostPort: 3000, ContainerPort: 80}},
		AllowDirtyWorkdir:    true,
		AbandonUnappliedWork: true,
	}
	in := o.toInternal()

	assert.Equal(t, "box", in.Name)
	assert.Equal(t, DirModeCopy, in.Workdir.Mode, "empty workdir mode defaults to copy")
	assert.True(t, in.Workdir.AllowDirty, "AllowDirtyWorkdir folds into Workdir.AllowDirty")
	assert.True(t, in.AbandonUnappliedWork, "AbandonUnappliedWork carries through to the internal create options")
	assert.Equal(t, "claude", in.Agent, "AgentType converts to string")
	assert.Equal(t, []string{"3000:80"}, in.Ports, "PortMappings render to host:container")
}

func TestCreateOptions_toInternal_PreservesExplicitWorkdir(t *testing.T) {
	o := CreateOptions{Workdir: DirSpec{Path: "/p", Mode: DirModeRW, AllowDirty: true}}
	in := o.toInternal()
	assert.Equal(t, DirModeRW, in.Workdir.Mode, "explicit mode is preserved")
	assert.True(t, in.Workdir.AllowDirty, "per-directory AllowDirty is preserved")
}

func TestRunOptions_materialize(t *testing.T) {
	c := RunOptions{
		Name:              "b",
		WorkDir:           "/w",
		Prompt:            "do the thing",
		AgentType:         AgentClaude,
		Replace:           true,
		AllowDirtyWorkdir: true,
	}.materialize()

	assert.Equal(t, "b", c.Name)
	assert.Equal(t, "/w", c.Workdir.Path)
	assert.Equal(t, DirModeCopy, c.Workdir.Mode, "Run always copies the workdir")
	assert.Equal(t, AgentClaude, c.AgentType)
	assert.Equal(t, "do the thing", c.Prompt)
	assert.True(t, c.Replace)
	assert.True(t, c.AllowDirtyWorkdir)
}

// A2/A3: Backend is OPTIONAL. A backend-less Client constructs cleanly (it
// serves host-only reads and, via System(), cross-backend admin) and never
// opens a connection at construction.
func TestNewWithOptions_BackendOptional(t *testing.T) {
	c, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), HomeDir: t.TempDir()})
	require.NoError(t, err, "empty Backend is allowed — the Client is backend-less")
	require.NotNil(t, c)
	assert.False(t, c.opened, "construction must not open the backend")
	assert.Equal(t, BackendType(""), c.backend)
}

// A backend-bound operation on a backend-less Client returns ErrBackendRequired
// (a *UsageError) instead of the old panic footgun — and does not latch, so a
// later op can still succeed once a backend is supplied.
func TestBackendBoundOp_OnBackendlessClient_ReturnsErrBackendRequired(t *testing.T) {
	c, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), HomeDir: t.TempDir()})
	require.NoError(t, err)

	_, err = c.List(context.Background())
	require.Error(t, err, "a backend-bound op on a backend-less Client must error")
	require.ErrorIs(t, err, ErrBackendRequired)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "ErrBackendRequired is a *UsageError for CLI exit-code mapping")
	assert.False(t, c.opened, "a failed/short-circuited ensure must not latch opened")
}

// ErrBackendRequired is a stable sentinel: errors.Is matches it directly.
func TestErrBackendRequired_IsSentinel(t *testing.T) {
	c, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), HomeDir: t.TempDir()})
	require.NoError(t, err)
	assert.True(t, errors.Is(c.ensure(context.Background()), ErrBackendRequired))
}

// Close on a Client whose backend was never opened is a no-op (no panic, no
// error) — the lazy core must not dereference a nil runtime.
func TestClose_OnUnopenedClient_IsNoop(t *testing.T) {
	c, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), HomeDir: t.TempDir()})
	require.NoError(t, err)
	require.NoError(t, c.Close(), "Close on an unopened Client must be a no-op")
}

func TestNewWithOptions_DataDirRequired(t *testing.T) {
	_, err := NewWithOptions(context.Background(), Options{BackendType: BackendDocker})
	require.Error(t, err, "empty DataDir must be rejected")
}

// HomeDir is required — empty rejected with a *UsageError, so the old silent
// filepath.Dir(DataDir) derivation (wrong under the D60 $HOME/.yoloai/library
// bifurcation) can never resolve seed/credential lookups to the wrong home.
func TestNewWithOptions_HomeDirRequired(t *testing.T) {
	_, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), BackendType: BackendDocker})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "empty HomeDir must yield a *UsageError")
}

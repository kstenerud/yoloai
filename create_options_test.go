// ABOUTME: Unit tests for the public creation surface (F1/F3/F4): toInternal
// ABOUTME: mapping, RunOptions.materialize sugar, port formatting, and the F4
// ABOUTME: required-Backend guard at construction.

package yoloai

import (
	"context"
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
		Agent:                AgentClaude,
		Ports:                []PortMapping{{HostPort: 3000, ContainerPort: 80}},
		AllowDirtyWorkdir:    true,
		AbandonUnappliedWork: true,
	}
	in := o.toInternal()

	assert.Equal(t, "box", in.Name)
	assert.Equal(t, DirModeCopy, in.Workdir.Mode, "empty workdir mode defaults to copy")
	assert.True(t, in.Workdir.AllowDirty, "AllowDirtyWorkdir folds into Workdir.AllowDirty")
	assert.True(t, in.AbandonUnappliedWork, "AbandonUnappliedWork carries through to the internal create options")
	assert.Equal(t, "claude", in.Agent, "AgentName converts to string")
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
		Agent:             AgentClaude,
		Replace:           true,
		AllowDirtyWorkdir: true,
	}.materialize()

	assert.Equal(t, "b", c.Name)
	assert.Equal(t, "/w", c.Workdir.Path)
	assert.Equal(t, DirModeCopy, c.Workdir.Mode, "Run always copies the workdir")
	assert.Equal(t, AgentClaude, c.Agent)
	assert.Equal(t, "do the thing", c.Prompt)
	assert.True(t, c.Replace)
	assert.True(t, c.AllowDirtyWorkdir)
}

// F4: Backend is required — empty rejected at construction with a *UsageError,
// before any backend connection is attempted.
func TestNewWithOptions_BackendRequired(t *testing.T) {
	_, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), HomeDir: t.TempDir()})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "empty Backend must yield a *UsageError")
}

func TestNewWithOptions_DataDirRequired(t *testing.T) {
	_, err := NewWithOptions(context.Background(), Options{Backend: BackendDocker})
	require.Error(t, err, "empty DataDir must be rejected")
}

// HomeDir is required — empty rejected with a *UsageError, so the old silent
// filepath.Dir(DataDir) derivation (wrong under the D60 $HOME/.yoloai/library
// bifurcation) can never resolve seed/credential lookups to the wrong home.
func TestNewWithOptions_HomeDirRequired(t *testing.T) {
	_, err := NewWithOptions(context.Background(), Options{DataDir: t.TempDir(), Backend: BackendDocker})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "empty HomeDir must yield a *UsageError")
}

func TestNewSystemClient_HomeDirRequired(t *testing.T) {
	_, err := NewSystemClient(SystemOptions{DataDir: t.TempDir()})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "empty HomeDir must yield a *UsageError")
}

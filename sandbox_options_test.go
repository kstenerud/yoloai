// ABOUTME: Unit tests for the per-sandbox public option types (F2): the
// ABOUTME: ResetOptions→internal mapping (Name folded from the handle, the
// ABOUTME: Restart→RestartContainer rename) and the Sandbox handle accessor.

package yoloai

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sandbox"
)

func TestCloneOptions_toInternal(t *testing.T) {
	in := CloneOptions{Source: "src", Dest: "dst", Overwrite: true}.toInternal()
	assert.Equal(t, sandbox.CloneOptions{Source: "src", Dest: "dst"}, in,
		"Overwrite is an orchestration-layer concern, not carried into the Engine clone")
}

// destroyForOverwrite must short-circuit (and never touch the runtime) when the
// destination doesn't exist — Overwrite on a fresh name is a plain clone.
func TestClient_destroyForOverwrite_MissingDestIsNoop(t *testing.T) {
	c, _ := clientWithSandbox(t) // nil runtime; the no-op path must not reach it
	require.NoError(t, c.destroyForOverwrite(context.Background(), "ghost"))
}

func TestResetOptions_toInternal(t *testing.T) {
	in := ResetOptions{
		RestartContainer: true,
		ClearState:       true,
		KeepCache:        true,
		KeepFiles:        true,
		NoPrompt:         true,
		Debug:            true,
	}.toInternal("mybox")

	assert.Equal(t, "mybox", in.Name, "handle name is folded in")
	assert.True(t, in.Restart, "RestartContainer maps to internal Restart")
	assert.True(t, in.ClearState)
	assert.True(t, in.KeepCache)
	assert.True(t, in.KeepFiles)
	assert.True(t, in.NoPrompt)
	assert.True(t, in.Debug)
}

func TestResetOptions_toInternal_Defaults(t *testing.T) {
	in := ResetOptions{}.toInternal("mybox")
	assert.Equal(t, "mybox", in.Name)
	assert.False(t, in.Restart)
	assert.False(t, in.ClearState)
}

func TestClient_Sandbox_BindsName(t *testing.T) {
	c, sys := clientWithSandbox(t)
	require.NoError(t, os.MkdirAll(sys.layout.SandboxDir("mybox"), 0750))
	sb, err := c.Sandbox("mybox")
	require.NoError(t, err)
	assert.Equal(t, "mybox", sb.Name())
}

func TestClient_Sandbox_NotFound(t *testing.T) {
	c, _ := clientWithSandbox(t)
	_, err := c.Sandbox("ghost")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

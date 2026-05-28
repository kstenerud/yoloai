// ABOUTME: Unit tests for the per-sandbox public option types (F2): the
// ABOUTME: ResetOptions→internal mapping (Name folded from the handle, the
// ABOUTME: Restart→RestartContainer rename) and the Sandbox handle accessor.

package yoloai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
	c := &Client{}
	assert.Equal(t, "mybox", c.Sandbox("mybox").Name())
}

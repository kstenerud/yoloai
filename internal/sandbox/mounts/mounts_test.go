// ABOUTME: Tests for Build — verifies workdir/agent/prompt/secret mount specs
// ABOUTME: are assembled correctly from resolved State.
package mounts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/store"
)

func TestBuild_CopyMode(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	st := &state.State{
		SandboxDir:  "/home/user/.yoloai/sandboxes/test",
		Workdir:     &state.DirSpec{Path: "/home/user/project", Mode: store.DirMode("copy")},
		WorkCopyDir: "/home/user/.yoloai/sandboxes/test/work/project",
		Agent:       agentDef,
		HasPrompt:   true,
	}

	mounts := Build(st, "")

	// Find workdir mount
	var workMount *runtime.MountSpec
	for i := range mounts {
		if mounts[i].ContainerPath == "/home/user/project" {
			workMount = &mounts[i]
			break
		}
	}
	require.NotNil(t, workMount)
	assert.Equal(t, st.WorkCopyDir, workMount.HostPath)
}

func TestBuild_RWMode(t *testing.T) {
	agentDef := agent.GetAgent("test")
	st := &state.State{
		SandboxDir: "/home/user/.yoloai/sandboxes/test",
		Workdir:    &state.DirSpec{Path: "/home/user/project", Mode: store.DirMode("rw")},
		Agent:      agentDef,
	}

	mounts := Build(st, "")

	// In rw mode, source should be the host path itself
	var workMount *runtime.MountSpec
	for i := range mounts {
		if mounts[i].ContainerPath == "/home/user/project" {
			workMount = &mounts[i]
			break
		}
	}
	require.NotNil(t, workMount)
	assert.Equal(t, "/home/user/project", workMount.HostPath)
}

func TestBuild_IncludesAgentState(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	st := &state.State{
		SandboxDir: "/sandbox",
		Workdir:    &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:      agentDef,
	}

	mounts := Build(st, "")

	var found bool
	for _, m := range mounts {
		if m.ContainerPath == agentDef.StateDir {
			found = true
			assert.Equal(t, "/sandbox/"+store.AgentRuntimeDir, m.HostPath)
		}
	}
	assert.True(t, found, "should include agent runtime mount")
}

func TestBuild_IncludesPrompt(t *testing.T) {
	agentDef := agent.GetAgent("test")
	st := &state.State{
		SandboxDir: "/sandbox",
		Workdir:    &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:      agentDef,
		HasPrompt:  true,
	}

	mounts := Build(st, "")

	var found bool
	for _, m := range mounts {
		if m.ContainerPath == "/yoloai/prompt.txt" {
			found = true
			assert.True(t, m.ReadOnly)
		}
	}
	assert.True(t, found, "should include prompt mount when hasPrompt")
}

func TestBuild_ExcludesPromptWhenNone(t *testing.T) {
	agentDef := agent.GetAgent("test")
	st := &state.State{
		SandboxDir: "/sandbox",
		Workdir:    &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:      agentDef,
		HasPrompt:  false,
	}

	mounts := Build(st, "")

	for _, m := range mounts {
		assert.NotEqual(t, "/yoloai/prompt.txt", m.ContainerPath, "should not include prompt mount")
	}
}

func TestBuild_IncludesSecrets(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	st := &state.State{
		SandboxDir: "/sandbox",
		Workdir:    &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:      agentDef,
	}

	secretsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "ANTHROPIC_API_KEY"), []byte("key"), 0600))

	mounts := Build(st, secretsDir)

	var found bool
	for _, m := range mounts {
		if m.ContainerPath == "/run/secrets" {
			found = true
			assert.Equal(t, secretsDir, m.HostPath)
			assert.True(t, m.ReadOnly)
		}
	}
	assert.True(t, found, "should include secrets mount")
}

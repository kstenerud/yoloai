// ABOUTME: Tests for the public Environment read-model — currently the
// ABOUTME: HasOverlayDirs query consumers branch on to route diff/apply.

package yoloai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvironmentHasOverlayDirs_Workdir(t *testing.T) {
	env := &Environment{Workdir: WorkdirInfo{Mode: DirModeOverlay}}
	assert.True(t, env.HasOverlayDirs())
}

func TestEnvironmentHasOverlayDirs_AuxDir(t *testing.T) {
	env := &Environment{
		Workdir:     WorkdirInfo{Mode: DirModeCopy},
		Directories: []DirInfo{{Mode: DirModeRW}, {Mode: DirModeOverlay}},
	}
	assert.True(t, env.HasOverlayDirs())
}

func TestEnvironmentHasOverlayDirs_None(t *testing.T) {
	env := &Environment{
		Workdir:     WorkdirInfo{Mode: DirModeCopy},
		Directories: []DirInfo{{Mode: DirModeCopy}, {Mode: DirModeRW}},
	}
	assert.False(t, env.HasOverlayDirs())
}

package workspace

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatApplyError_PatchFailed(t *testing.T) {
	gitErr := fmt.Errorf("error: patch failed: handler.go:42\nerror: handler.go: patch does not apply: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "handler.go")
	assert.Contains(t, err.Error(), "42")
	assert.Contains(t, err.Error(), "conflict")
}

func TestFormatApplyError_Unknown(t *testing.T) {
	gitErr := fmt.Errorf("some unusual error: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "git apply failed")
	assert.Contains(t, err.Error(), "/tmp/project")
}

func TestIsGitRepo_True(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	assert.True(t, IsGitRepo(dir))
}

func TestIsGitRepo_False(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, IsGitRepo(dir))
}

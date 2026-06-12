// ABOUTME: Unit tests for the CAS-guarded baseline verbs (advance/set) and the
// ABOUTME: baseline-log read-model in baseline.go.
package patch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type commitSpec = struct {
	subject  string
	filename string
	content  string
}

func currentBaseline(t *testing.T, tmpDir, name string) string {
	t.Helper()
	meta, err := store.LoadEnvironment(testLayout(tmpDir).SandboxDir(name))
	require.NoError(t, err)
	return meta.Workdir().BaselineSHA
}

func TestAdvanceBaselineCAS_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cas-advance-ok"
	workDir := createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []commitSpec{
		{"add feature", "feature.txt", "feature\n"},
	})

	rt := hostGitRuntime()
	expected := currentBaseline(t, tmpDir, name)

	change, err := AdvanceBaselineCAS(context.Background(), testLayout(tmpDir), rt, name, expected)
	require.NoError(t, err)
	assert.Equal(t, gitHEAD(t, workDir), change.NewSHA)
	assert.Equal(t, "add feature", change.Subject)
	assert.Equal(t, change.NewSHA, currentBaseline(t, tmpDir, name))
}

func TestAdvanceBaselineCAS_ConflictDoesNotWrite(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cas-advance-conflict"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []commitSpec{
		{"add feature", "feature.txt", "feature\n"},
	})

	rt := hostGitRuntime()
	before := currentBaseline(t, tmpDir, name)

	_, err := AdvanceBaselineCAS(context.Background(), testLayout(tmpDir), rt, name, "deadbeefdeadbeef")
	var conflict *BaselineConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "deadbeefdeadbeef", conflict.Expected)
	assert.Equal(t, before, conflict.Actual)
	assert.Equal(t, before, currentBaseline(t, tmpDir, name), "baseline must be unchanged after conflict")
}

func TestAdvanceBaselineCAS_EmptyExpectedConflictsWhenSet(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cas-advance-empty"
	createCopySandbox(t, tmpDir, name, "/tmp/project")

	rt := hostGitRuntime()
	_, err := AdvanceBaselineCAS(context.Background(), testLayout(tmpDir), rt, name, "")
	var conflict *BaselineConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "", conflict.Expected)
}

func TestSetBaselineCAS_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cas-set-ok"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []commitSpec{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	target := commits[0] // "first"

	expected := currentBaseline(t, tmpDir, name)
	change, err := SetBaselineCAS(context.Background(), testLayout(tmpDir), rt, name, expected, target.SHA)
	require.NoError(t, err)
	assert.Equal(t, target.SHA, change.NewSHA)
	assert.Equal(t, "first", change.Subject)
	assert.Equal(t, target.SHA, currentBaseline(t, tmpDir, name))
}

func TestSetBaselineCAS_Conflict(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cas-set-conflict"
	workDir := createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []commitSpec{
		{"first", "a.txt", "a\n"},
	})

	rt := hostGitRuntime()
	before := currentBaseline(t, tmpDir, name)
	_, err := SetBaselineCAS(context.Background(), testLayout(tmpDir), rt, name, "notthecurrentsha", gitHEAD(t, workDir))
	var conflict *BaselineConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, before, currentBaseline(t, tmpDir, name))
}

func TestBaselineCAS_RWUsageError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	name := "cas-rw"
	createRWSandbox(t, tmpDir, name, hostDir)

	rt := hostGitRuntime()
	_, err := AdvanceBaselineCAS(context.Background(), testLayout(tmpDir), rt, name, "")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	assert.True(t, errors.As(err, &usage))
	assert.Contains(t, err.Error(), ":rw directories")
}

func TestBaselineLog_MarksBaseline(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cas-log"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []commitSpec{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
	})

	rt := hostGitRuntime()
	baseline := currentBaseline(t, tmpDir, name)

	entries, err := BaselineLog(context.Background(), testLayout(tmpDir), rt, name)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	marked := 0
	for _, e := range entries {
		if e.IsBaseline {
			marked++
			assert.Equal(t, baseline, e.SHA)
		}
	}
	assert.Equal(t, 1, marked, "exactly one entry should be marked as the baseline")
}

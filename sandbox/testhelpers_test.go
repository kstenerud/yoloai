package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/workspace"
)

func initGitRepo(t *testing.T, dir string)        { t.Helper(); testutil.InitGitRepo(t, dir) }
func gitAdd(t *testing.T, dir, path string)       { t.Helper(); testutil.GitAdd(t, dir, path) }
func gitCommit(t *testing.T, dir, msg string)     { t.Helper(); testutil.GitCommit(t, dir, msg) }
func gitRevParse(t *testing.T, dir string) string { t.Helper(); return testutil.GitRevParse(t, dir) }
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

// gitHEAD returns the HEAD commit SHA for the git repo at dir.
func gitHEAD(t *testing.T, dir string) string {
	t.Helper()
	sha, err := workspace.HeadSHA(dir)
	require.NoError(t, err)
	return sha
}

// createRWSandbox creates a minimal :rw mode sandbox directory structure for tests.
func createRWSandbox(t *testing.T, tmpDir, name, hostPath string) {
	t.Helper()

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "rw",
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))
}

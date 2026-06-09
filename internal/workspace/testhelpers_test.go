package workspace

import (
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
)

func initGitRepo(t *testing.T, dir string)            { t.Helper(); testutil.InitGitRepo(t, dir) }
func gitAdd(t *testing.T, dir, path string)           { t.Helper(); testutil.GitAdd(t, dir, path) }
func gitCommit(t *testing.T, dir, msg string)         { t.Helper(); testutil.GitCommit(t, dir, msg) }
func runGit(t *testing.T, dir string, args ...string) { t.Helper(); testutil.RunGit(t, dir, args...) }
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

// testEnv returns os.Environ() for use as an explicit subprocess env in tests.
// Tests are the outermost edge; reading os.Environ() here is the licensed
// boundary-resolve for the test process (DEV §12).
func testEnv() []string { return os.Environ() } //nolint:forbidigo // §12: test-edge boundary; tests resolve ambient env once here

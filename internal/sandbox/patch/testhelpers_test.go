// ABOUTME: Shared test helpers for sandbox/patch unit tests.

package patch

import (
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"

	_ "github.com/kstenerud/yoloai/internal/runtime/docker" // register backend for tests
)

// testLayout returns a config.Layout rooted at tmpDir/.yoloai/ — matches the
// HOME=tmpDir convention used by patch tests.
func testLayout(tmpDir string) config.Layout {
	return config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
}

func initGitRepo(t *testing.T, dir string)    { t.Helper(); testutil.InitGitRepo(t, dir) }
func gitAdd(t *testing.T, dir, path string)   { t.Helper(); testutil.GitAdd(t, dir, path) }
func gitCommit(t *testing.T, dir, msg string) { t.Helper(); testutil.GitCommit(t, dir, msg) }
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

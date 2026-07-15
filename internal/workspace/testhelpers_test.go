// ABOUTME: Shared test fixture for package workspace: writeTestFile wraps
// ABOUTME: testutil.WriteFile for staging file contents under a temp dir.
package workspace

import (
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

package workspace

import (
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

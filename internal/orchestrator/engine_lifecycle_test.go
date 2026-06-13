// ABOUTME: White-box tests for the Engine lifecycle/create verbs — currently the
// ABOUTME: DestroyForOverwrite missing-destination no-op short-circuit.

package orchestrator

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
)

// DestroyForOverwrite must short-circuit (and never touch the runtime) when the
// destination doesn't exist — an Overwrite clone onto a fresh name is a plain
// clone. The injected nil-runtime Engine latches opened, so ensure is a no-op
// and the os.Stat miss returns before any runtime call.
func TestEngine_DestroyForOverwrite_MissingDestIsNoop(t *testing.T) {
	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))
	e := NewEngineWithRuntime(nil, slog.Default(), strings.NewReader(""), WithLayout(layout))
	require.NoError(t, e.DestroyForOverwrite(context.Background(), "ghost"))
}

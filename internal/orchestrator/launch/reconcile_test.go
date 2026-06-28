// ABOUTME: Tests that ReconcileInjector's cheap paths (not brokered / injector
// ABOUTME: alive) short-circuit without ever touching the runtime backend.
package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
)

// panicBackend satisfies runtime.Backend with a nil embedded interface, so any
// method call panics — proving the cheap reconcile paths never reach the backend.
type panicBackend struct{ runtime.Backend }

func TestReconcileInjector_NotBrokeredSkipsBackend(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	d := state.Deps{Runtime: panicBackend{}, Layout: layout}

	// No injector.json under the sandbox dir -> the sandbox was never brokered.
	require.NoError(t, ReconcileInjector(context.Background(), d, "nobroker"))
}

func TestReconcileInjector_LiveInjectorSkipsBackend(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	d := state.Deps{Runtime: panicBackend{}, Layout: layout}

	// A record pointing at a live PID (this test process) -> injector healthy.
	dir := layout.SandboxDir("live")
	require.NoError(t, fileutil.MkdirAll(dir, 0o755))
	rec := fmt.Sprintf(`{"pid":%d,"addr":"127.0.0.1:1"}`, os.Getpid())
	require.NoError(t, fileutil.WriteFile(filepath.Join(dir, "injector.json"), []byte(rec), 0o600))

	require.NoError(t, ReconcileInjector(context.Background(), d, "live"))
}

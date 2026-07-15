// ABOUTME: TartStoreLayout builds the Layout a Tart integration test needs: yoloai
// ABOUTME: state isolated in a temp dir, but tart's own multi-GB VM store shared.
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
)

// TartStoreLayout returns the Layout for a Tart integration test.
//
// DataDir is a temp dir, so yoloai's own state (config, sandboxes, profiles) is
// isolated per test and the real ~/.yoloai is never touched. HomeDir, however,
// is the REAL home — because tart resolves its store via TART_HOME, which
// config.HostEnv.EnvForTartInvocation defaults to <HomeDir>/.tart. A temp
// HomeDir therefore points tart at an EMPTY store, and tart responds by
// re-downloading the ~30 GB base image and re-provisioning the ~29 GB
// yoloai-base VM — per test. That is not isolation, it is a cache miss with a
// multi-hour price tag (DF19).
//
// Isolation inside the shared store comes from the principal instead of the
// path: every instance the engine creates is named
// yoloai-<principal>-<sandbox> (store.InstanceName), and a prune sweep is
// scoped to exactly that prefix (runtime/tart/prune.go). A unique principal per
// test therefore cannot collide with — or reap — a real VM. This is the same
// trade the container backends already make: they share the daemon's image
// store and isolate by name, and tart's store is the same kind of host
// infrastructure.
//
// The env comes from the start-of-process capture (see hostEnvAtStart), not a
// call-time read, so an integration TestMain's $HOME rewrite cannot silently
// reintroduce the empty-store bug. An ambient TART_HOME, if set, is honored:
// it is in the IntegrationHostEnvVars allowlist and EnvForTartInvocation
// prefers it over the <HomeDir>/.tart default.
//
// Uniqueness is per-process: UniqueTestPrincipal counts from t0000001 in each
// test binary, so two test PROCESSES running tart concurrently draw the same
// principal and collide over one VM name in the shared store — the second sees
// the first's VM as already running, then fails on a work dir that is not its
// own. Tests within a process are fine. `make integration` runs the tart tier
// as its own single invocation, which is what keeps this theoretical.
func TartStoreLayout(t *testing.T) config.Layout {
	t.Helper()
	home := HostHomeAtStart()
	if home == "" {
		t.Fatal("cannot resolve the real host home: HOME was unset at process start, " +
			"so tart's store location (TART_HOME defaults to <home>/.tart) cannot be determined")
	}
	return config.NewLayoutFor(filepath.Join(t.TempDir(), ".yoloai"), home).
		WithEnv(hostEnvAtStart).
		WithPrincipal(UniqueTestPrincipal(t))
}

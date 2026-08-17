// ABOUTME: Tests for the System sub-handle: cross-backend introspection
// ABOUTME: (Info / Backends / Doctor), name validation, and Prune host-side
// ABOUTME: classification (known / never-init / corrupt-trash / data-bearing) + EmptyTrash.

package yoloai

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystem_Info verifies paths are derived from the layout and that the
// backend probe returns exactly one status per registered backend (names in
// registration order; unavailable backends carry a reason).
func TestSystem_Info(t *testing.T) {
	c := newTestClient(t)

	info, err := c.Info(context.Background())
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, c.layout.YoloaiDir(), info.DataDir)
	assert.Equal(t, c.layout.SandboxesDir(), info.SandboxesDir)
	assert.Equal(t, c.layout.GlobalConfigPath(), info.GlobalConfigPath)
	assert.Equal(t, c.layout.DefaultsConfigPath(), info.DefaultsConfigPath)

	descs := runtime.Descriptors()
	require.Len(t, info.Backends, len(descs), "one BackendInfo per registered backend")
	for i, b := range info.Backends {
		assert.Equal(t, descs[i].Type, b.Type, "backend statuses preserve registration order")
		if !b.Available {
			assert.NotEmpty(t, b.Note, "an unavailable backend must explain why")
		}
	}
}

// TestClient_Principal threads ClientCreateOptions.Principal into the layout
// (empty is rejected — no default principal, D126; a valid segment parses;
// an invalid one is a *UsageError).
func TestClient_Principal(t *testing.T) {
	root := t.TempDir()

	_, err := NewClient(context.Background(), ClientCreateOptions{DataDir: root, HomeDir: root})
	require.Error(t, err)
	assert.ErrorContains(t, err, "principal is required")

	acme, err := NewClient(context.Background(), ClientCreateOptions{DataDir: root, HomeDir: root, Principal: "acme"})
	require.NoError(t, err)
	assert.Equal(t, config.PrincipalSegment("acme"), acme.layout.Principal)

	_, err = NewClient(context.Background(), ClientCreateOptions{DataDir: root, HomeDir: root, Principal: "way-too-long-and-invalid"})
	require.Error(t, err)
	var usageErr *yoerrors.UsageError
	assert.ErrorAs(t, err, &usageErr)
}

// TestSystem_ValidateSandboxName accepts a well-formed name and rejects
// path-traversal, with no host state consulted.
func TestSystem_ValidateSandboxName(t *testing.T) {
	c := newTestClient(t)
	assert.NoError(t, c.ValidateSandboxName("my-box"))
	assert.Error(t, c.ValidateSandboxName("../escape"))
}

// TestSystem_ListAcrossBackends_Empty verifies a fresh install (no sandbox
// dirs) lists nothing and probes no backends — no enumeration, no error.
func TestSystem_ListAcrossBackends_Empty(t *testing.T) {
	c := newTestClient(t)
	infos, unavailable, err := c.AllSandboxes(context.Background())
	require.NoError(t, err)
	assert.Empty(t, infos)
	assert.Empty(t, unavailable)
}

// TestSystem_Doctor verifies every registered backend produces at least
// one report row (base-mode or init-failure), and that a non-matching backend
// filter yields nothing.
func TestSystem_Doctor(t *testing.T) {
	c := newTestClient(t)

	reports, err := c.Doctor(context.Background(), SystemDoctorOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, reports, "every registered backend produces at least one report row")
	for _, r := range reports {
		assert.NotEmpty(t, r.Type, "each report names its backend type")
	}

	none, err := c.Doctor(context.Background(), SystemDoctorOptions{BackendFilter: "does-not-exist"})
	require.NoError(t, err)
	assert.Empty(t, none, "a non-matching backend filter reports nothing")
}

// mkSandboxDir creates DataDir/sandboxes/<name>/ and returns its path.
func mkSandboxDir(t *testing.T, c *System, name string) string {
	t.Helper()
	dir := c.layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	return dir
}

// writeEnv writes environment.json content into a sandbox dir.
func writeEnv(t *testing.T, dir, content string) {
	t.Helper()
	testutil.WriteSandboxRecord(t, filepath.Join(dir, "host", "environment.json"), []byte(content))
}

// TestLiveInjectorPIDs_ProtectsOnlyLiveSandboxes guards the keep-set that scopes
// the DF71 injector sweep: a sandbox with loadable metadata protects its recorded
// injector PID; a broken sandbox's injector is fair game (an orphan).
func TestLiveInjectorPIDs_ProtectsOnlyLiveSandboxes(t *testing.T) {
	c := newTestClient(t)

	good := mkSandboxDir(t, c, "good")
	writeEnv(t, good, `{"version":3}`)
	require.NoError(t, os.WriteFile(config.InjectorRecordPath(good), []byte(`{"pid":111,"addr":"127.0.0.1:1"}`), 0o600))

	broken := mkSandboxDir(t, c, "broken")
	writeEnv(t, broken, `{not json`)
	require.NoError(t, os.WriteFile(config.InjectorRecordPath(broken), []byte(`{"pid":222,"addr":"127.0.0.1:2"}`), 0o600))

	keep := c.liveInjectorPIDs()

	assert.True(t, keep[111], "a live sandbox's injector PID is protected")
	assert.False(t, keep[222], "a broken sandbox's injector is an orphan, not protected")
}

func findItem(items []PruneItem, kind PruneItemKind, name string) bool {
	for _, it := range items {
		if it.Kind == kind && it.Name == name {
			return true
		}
	}
	return false
}

func findRefused(rs []RefusedSandbox, name string) bool {
	for _, r := range rs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func findTrashed(ts []TrashedSandbox, name string) bool {
	for _, tt := range ts {
		if tt.Name == name {
			return true
		}
	}
	return false
}

// TestPrune_ClassifiesSandboxDirs verifies the four-way classification on a
// dry run: known (untouched), never-init (delete), corrupt-no-data (trash),
// data-bearing (refuse).
func TestPrune_ClassifiesSandboxDirs(t *testing.T) {
	c := newTestClient(t)

	// known: valid metadata at the current schema version (a pre-v3 record now
	// balks on load and would classify as corrupt — see Q104).
	good := mkSandboxDir(t, c, "good")
	writeEnv(t, good, `{"version":3}`)

	// never-init: no metadata, no work dir.
	mkSandboxDir(t, c, "neverinit")

	// corrupt: unparseable metadata, no work dir.
	corrupt := mkSandboxDir(t, c, "corrupt")
	writeEnv(t, corrupt, `{not json`)

	// version-too-new: parseable but migrate rejects, no work dir.
	toonew := mkSandboxDir(t, c, "toonew")
	writeEnv(t, toonew, `{"version":99}`)

	// data-bearing: overlay upper/ with content (host-side, no container needed).
	dirty := mkSandboxDir(t, c, "dirty")
	require.NoError(t, os.MkdirAll(filepath.Join(config.WorkBasePath(dirty), "proj", "upper"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(config.WorkBasePath(dirty), "proj", "upper", "f"), []byte("x"), 0o600))

	res, err := c.Prune(context.Background(), SystemPruneOptions{DryRun: true})
	require.NoError(t, err)

	assert.True(t, findItem(res.RemovedItems, PruneKindSandboxDir, "neverinit"), "never-init dir should be slated for delete")
	assert.True(t, findTrashed(res.Trashed, "corrupt"), "corrupt-no-data should be quarantined")
	assert.True(t, findTrashed(res.Trashed, "toonew"), "version-too-new no-data should be quarantined")
	assert.True(t, findRefused(res.RefusedDataBearing, "dirty"), "data-bearing dir should be refused")

	// "good" must not appear in any removal/trash/refuse list.
	assert.False(t, findItem(res.RemovedItems, PruneKindSandboxDir, "good"))
	assert.False(t, findTrashed(res.Trashed, "good"))
	assert.False(t, findRefused(res.RefusedDataBearing, "good"))

	// Dry run must not mutate the filesystem.
	assert.DirExists(t, c.layout.SandboxDir("neverinit"))
	assert.DirExists(t, c.layout.SandboxDir("corrupt"))
}

// TestPrune_ExecutesClassifications verifies the actual (non-dry-run) prune
// deletes never-init dirs, quarantines corrupt dirs to trash, and leaves
// data-bearing dirs untouched.
func TestPrune_ExecutesClassifications(t *testing.T) {
	c := newTestClient(t)

	mkSandboxDir(t, c, "neverinit")
	corrupt := mkSandboxDir(t, c, "corrupt")
	writeEnv(t, corrupt, `{bad`)
	dirty := mkSandboxDir(t, c, "dirty")
	require.NoError(t, os.MkdirAll(filepath.Join(config.WorkBasePath(dirty), "proj", "upper"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(config.WorkBasePath(dirty), "proj", "upper", "f"), []byte("x"), 0o600))

	res, err := c.Prune(context.Background(), SystemPruneOptions{DryRun: false})
	require.NoError(t, err)

	assert.NoDirExists(t, c.layout.SandboxDir("neverinit"), "never-init dir removed")
	assert.NoDirExists(t, c.layout.SandboxDir("corrupt"), "corrupt dir moved out of sandboxes")
	assert.DirExists(t, filepath.Join(c.layout.TrashDir(), "corrupt"), "corrupt dir quarantined to trash")
	assert.DirExists(t, c.layout.SandboxDir("dirty"), "data-bearing dir left untouched")

	assert.Equal(t, 1, res.TrashContents.Count, "trash summary reflects the quarantined dir")
}

// TestPrune_RecordsActionsAndKeepsFailuresOutOfRemovedItems pins the split
// RemovedItems drives.
//
// That list is what the CLI counts in "Remove N resource(s)?" and what a
// scripted caller reads as "reclaimed", so a resource still on disk must never
// appear there. Before actions existed the question could not even be asked:
// a failure was dropped from the result and written to a writer.
func TestPrune_RecordsActionsAndKeepsFailuresOutOfRemovedItems(t *testing.T) {
	c := newTestClient(t)

	mkSandboxDir(t, c, "neverinit")

	res, err := c.Prune(context.Background(), SystemPruneOptions{DryRun: true})
	require.NoError(t, err)

	require.True(t, findItem(res.RemovedItems, PruneKindSandboxDir, "neverinit"))
	for _, item := range res.RemovedItems {
		assert.Truef(t, item.Action == PruneActionRemoved || item.Action == PruneActionWouldRemove,
			"%s %q is on RemovedItems with action %q; that list means reclaimed",
			item.Kind, item.Name, item.Action)
	}
	for _, item := range res.SkippedItems {
		assert.Truef(t, item.Action == PruneActionFailed || item.Action == PruneActionSkipped,
			"%s %q is on SkippedItems with action %q", item.Kind, item.Name, item.Action)
		assert.NotEmptyf(t, item.Reason, "%s %q was skipped without saying why", item.Kind, item.Name)
	}
}

// TestPrune_NoticesReachBothTheResultAndTheWriter covers the fan-out at the
// aggregation boundary. Backends no longer format their advisories, so if the
// result were the only destination, every existing caller passing Output would
// silently go quiet; if the writer were the only one, the conversion would have
// bought nothing. Both, exactly once each, is the contract (D145).
func TestPrune_NoticesReachBothTheResultAndTheWriter(t *testing.T) {
	c := newTestClient(t)

	// A lock file with no sandbox dir and no live holder, made unremovable by
	// stripping write on its directory — the one advisory this layer can
	// provoke without a backend.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions, so the removal cannot be made to fail")
	}
	unlock, err := store.AcquireLock(c.layout, "ghost")
	require.NoError(t, err)
	unlock()

	dir := c.layout.SandboxesDir()
	require.NoError(t, os.Chmod(dir, 0o500))       //nolint:gosec // read-only is the point: it forces the removal to fail
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore so TempDir cleanup can recurse

	var rendered bytes.Buffer
	res, err := c.Prune(context.Background(), SystemPruneOptions{DryRun: false, Output: &rendered})
	require.NoError(t, err)

	// Searched, not indexed: an available backend contributes its own notices
	// first, and which ones depend on the host.
	var found bool
	for _, n := range res.Notices {
		if n.Event == "lock.stale_remove_failed" {
			found = true
			assert.Equal(t, feedback.LevelWarn, n.Level)
			assert.Contains(t, n.Fields["path"], "ghost")
		}
	}
	assert.True(t, found, "the advisory must reach the caller as data, not only as text")
	assert.Contains(t, rendered.String(), "ghost",
		"the advisory must still reach a caller that only gave us a writer")
	assert.Equal(t, 1, strings.Count(rendered.String(), "could not remove stale lock"),
		"the tee must deliver once, not once per destination")
}

// TestEmptyTrash_RemovesAll verifies EmptyTrash deletes all trash entries.
func TestEmptyTrash_RemovesAll(t *testing.T) {
	c := newTestClient(t)

	require.NoError(t, os.MkdirAll(filepath.Join(c.layout.TrashDir(), "a"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(c.layout.TrashDir(), "b"), 0o750))

	removed, _, err := c.EmptyTrash()
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	entries, err := os.ReadDir(c.layout.TrashDir())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestEmptyTrash_NoTrashDirIsNoOp verifies EmptyTrash is a no-op when the
// trash dir does not exist.
func TestEmptyTrash_NoTrashDirIsNoOp(t *testing.T) {
	c := newTestClient(t)
	removed, freed, err := c.EmptyTrash()
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.Zero(t, freed)
}

// TestSystem_CheckAgent verifies the agent-credential check mirrors the runtime
// auth-presence policy: an API-key env var, an auth credential file, OR a macOS
// Keychain entry each satisfy it — not env vars alone. Regression guard for the
// bug where `system check` reported "no credentials" for a native Claude Code
// install whose token lives only in the Keychain, even though `new` succeeds.
func TestSystem_CheckAgent(t *testing.T) {
	// The check consults the real macOS Keychain via envsetup.KeychainReader;
	// stub it so the test is hermetic (and can exercise the keychain branch
	// directly rather than depending on the host's keychain contents).
	newSystem := func(t *testing.T, env map[string]string) (*System, string) {
		t.Helper()
		home := t.TempDir()
		dataDir := filepath.Join(home, ".yoloai")
		require.NoError(t, os.MkdirAll(dataDir, 0o750))
		c, err := NewClient(context.Background(), ClientCreateOptions{
			DataDir:   dataDir,
			HomeDir:   home,
			Env:       env,
			Principal: "cli",
		})
		require.NoError(t, err)
		return c.System(), home
	}
	stubKeychain := func(t *testing.T, data []byte, err error) {
		t.Helper()
		orig := envsetup.KeychainReader
		envsetup.KeychainReader = func(string) ([]byte, error) { return data, err }
		t.Cleanup(func() { envsetup.KeychainReader = orig })
	}
	noKeychain := errors.New("keychain: item not found")

	t.Run("api key env var", func(t *testing.T) {
		stubKeychain(t, nil, noKeychain)
		s, _ := newSystem(t, map[string]string{"ANTHROPIC_API_KEY": "sk-test"})
		res := s.checkAgent("claude")
		assert.True(t, res.OK, res.Message)
		assert.Contains(t, res.Message, "ANTHROPIC_API_KEY")
	})

	t.Run("keychain entry, no env", func(t *testing.T) {
		stubKeychain(t, []byte(`{"claudeAiOauth":{"accessToken":"x"}}`), nil)
		s, _ := newSystem(t, nil)
		res := s.checkAgent("claude")
		assert.True(t, res.OK, res.Message)
		assert.Contains(t, res.Message, "Keychain")
	})

	t.Run("auth file on disk, no env, no keychain", func(t *testing.T) {
		stubKeychain(t, nil, noKeychain)
		s, home := newSystem(t, nil)
		credPath := filepath.Join(home, ".claude", ".credentials.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o750))
		require.NoError(t, os.WriteFile(credPath, []byte(`{}`), 0o600))
		res := s.checkAgent("claude")
		assert.True(t, res.OK, res.Message)
		assert.Contains(t, res.Message, "auth file")
	})

	t.Run("no credentials at all", func(t *testing.T) {
		stubKeychain(t, nil, noKeychain)
		s, _ := newSystem(t, nil)
		res := s.checkAgent("claude")
		assert.False(t, res.OK)
		assert.Contains(t, res.Message, "no credentials set for agent")
	})

	t.Run("agent requiring no credentials", func(t *testing.T) {
		stubKeychain(t, nil, noKeychain)
		s, _ := newSystem(t, nil)
		res := s.checkAgent("idle")
		assert.True(t, res.OK, res.Message)
	})

	t.Run("unknown agent", func(t *testing.T) {
		s, _ := newSystem(t, nil)
		res := s.checkAgent("nonesuch")
		assert.False(t, res.OK)
		assert.Contains(t, res.Message, "unknown agent")
	})
}

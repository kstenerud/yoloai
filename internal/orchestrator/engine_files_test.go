// ABOUTME: Tests that an import replacing an existing entry has its delivery verified against
// ABOUTME: the running guest, and that an unrepairable one fails the command instead of exiting 0.

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refreshBackendType is registered for real, with the real capability flag, so
// these tests go through runtime.Descriptor and runtime.New exactly as a `files
// put` does. Asserting against a hand-built fake instead would certify a path
// the product does not have: the capability gate is what decides whether any of
// this runs, and a fake that bypasses it proves nothing about tart (rule 10).
const refreshBackendType runtime.BackendType = "refresh-test"

// currentRefresher is what the registered factory hands back. Tests in a package
// run sequentially unless they call t.Parallel, and none of these do.
var currentRefresher *refreshingRuntime

func init() {
	runtime.Register(
		func(_ context.Context, _ config.Layout) (runtime.Backend, error) { return currentRefresher, nil },
		runtime.BackendDescriptor{
			Type:         refreshBackendType,
			BaseModeName: runtime.IsolationModeContainer,
			Capabilities: runtime.BackendCaps{HostWritesNeedGuestRefresh: true},
		},
	)
}

// refreshingRuntime is a mockRuntime that also implements GuestFileRefresher —
// the combination tart ships. It records what it was asked to refresh and replies
// with a scripted verdict.
type refreshingRuntime struct {
	mockRuntime
	calls    [][]runtime.GuestFileDigest
	instance string
	stale    []runtime.GuestFileDigest // returned as unrepairable
	err      error
}

var _ runtime.GuestFileRefresher = (*refreshingRuntime)(nil)

func (r *refreshingRuntime) RefreshGuestFiles(_ context.Context, name string, files []runtime.GuestFileDigest) ([]runtime.GuestFileDigest, error) {
	r.calls = append(r.calls, files)
	r.instance = name
	return r.stale, r.err
}

// newImportEngine builds a backend-less Engine — which is what the `files`
// command actually constructs — over a sandbox whose environment.json names
// backendType. The backend-less part is load-bearing: the first version of this
// fix read e.Runtime(), which is nil here, so it silently did nothing on the one
// path that needs it and still passed every unit test.
func newImportEngine(t *testing.T, backendType runtime.BackendType) (*Engine, config.Layout) {
	t.Helper()
	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai")).WithPrincipal(config.CLIPrincipal)
	sandboxDir := layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Name: "box", BackendType: backendType, CreatedAt: time.Now(),
	}))
	return NewEngine("", slog.Default(), strings.NewReader(""), WithLayout(layout)), layout
}

func newRefreshEngine(t *testing.T, rt *refreshingRuntime) (*Engine, config.Layout) {
	t.Helper()
	currentRefresher = rt
	t.Cleanup(func() { currentRefresher = nil })
	return newImportEngine(t, refreshBackendType)
}

func hostFile(t *testing.T, content string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "payload.txt")
	require.NoError(t, os.WriteFile(src, []byte(content), 0600))
	return src
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// A name the guest has never read is always delivered correctly — measured — so a
// first import must not pay for a guest round trip.
func TestImportFile_FirstImportDoesNotTouchTheGuest(t *testing.T) {
	rt := &refreshingRuntime{}
	engine, _ := newRefreshEngine(t, rt)

	_, err := engine.ImportFile(context.Background(), "box", hostFile(t, "hi"), false)
	require.NoError(t, err)
	assert.Empty(t, rt.calls, "nothing was replaced, so nothing can be stale")
}

func TestImportFile_ReplacementIsVerifiedAgainstTheGuest(t *testing.T) {
	rt := &refreshingRuntime{}
	engine, layout := newRefreshEngine(t, rt)
	src := hostFile(t, "v1")

	_, err := engine.ImportFile(context.Background(), "box", src, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, []byte("v2-longer"), 0600))

	_, err = engine.ImportFile(context.Background(), "box", src, true)
	require.NoError(t, err)

	require.Len(t, rt.calls, 1, "the overwrite is checked exactly once")
	require.Len(t, rt.calls[0], 1)
	got := rt.calls[0][0]
	assert.Equal(t, filepath.Join(FilesDir(layout, "box"), "payload.txt"), got.HostPath)
	// The digest must be of what the host just wrote. Sending the OLD digest
	// would make every stale file verify clean, which is the defect wearing the
	// fix's clothes.
	assert.Equal(t, sha256Of("v2-longer"), got.SHA256)
	assert.Equal(t, "yoloai-cli-box", rt.instance)
}

// The whole point: a put that cannot be made visible must not exit 0.
func TestImportFile_UnrepairableStaleContentFailsTheCommand(t *testing.T) {
	rt := &refreshingRuntime{}
	engine, layout := newRefreshEngine(t, rt)
	src := hostFile(t, "v1")
	_, err := engine.ImportFile(context.Background(), "box", src, false)
	require.NoError(t, err)

	rt.stale = []runtime.GuestFileDigest{{
		HostPath: filepath.Join(FilesDir(layout, "box"), "payload.txt"),
		SHA256:   "deadbeef",
	}}
	require.NoError(t, os.WriteFile(src, []byte("v2"), 0600))

	_, err = engine.ImportFile(context.Background(), "box", src, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stale")
	assert.Contains(t, err.Error(), "payload.txt")
	// The message has to name the one thing that is known to clear it, or the
	// user is told they have a problem and nothing else.
	assert.Contains(t, err.Error(), "yoloai stop box")
}

// A stopped guest holds no cache, so it cannot be stale — and a `files put` into
// a stopped sandbox is an ordinary, supported thing to do.
func TestImportFile_StoppedSandboxIsNotAnError(t *testing.T) {
	rt := &refreshingRuntime{err: runtime.ErrNotRunning}
	engine, _ := newRefreshEngine(t, rt)
	src := hostFile(t, "v1")
	_, err := engine.ImportFile(context.Background(), "box", src, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, []byte("v2"), 0600))

	_, err = engine.ImportFile(context.Background(), "box", src, true)
	require.NoError(t, err)
}

func TestImportFile_BackendWithoutRefreshIsUnaffected(t *testing.T) {
	engine, _ := newImportEngine(t, "mock")
	src := hostFile(t, "v1")
	_, err := engine.ImportFile(context.Background(), "box", src, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, []byte("v2"), 0600))

	_, err = engine.ImportFile(context.Background(), "box", src, true)
	require.NoError(t, err, "every backend but tart delivers host writes to a running guest")
}

// A directory import must verify every file it placed, not just the top entry —
// otherwise `files put somedir/` reports success over a tree the guest cannot read.
func TestImportFile_DirectoryVerifiesEveryFileInTheTree(t *testing.T) {
	rt := &refreshingRuntime{}
	engine, layout := newRefreshEngine(t, rt)

	srcDir := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "nested", "b.txt"), []byte("b"), 0600))

	_, err := engine.ImportFile(context.Background(), "box", srcDir, false)
	require.NoError(t, err)
	_, err = engine.ImportFile(context.Background(), "box", srcDir, true)
	require.NoError(t, err)

	// Expectation is read off the tree that is actually on disk, not hardcoded.
	// That was originally to avoid pinning DF177's nesting bug as intended
	// behaviour — re-importing a directory used to copy INTO it, leaving
	// bundle/bundle/… beside stale bundle/*. DF177 is fixed now, so the tree is
	// the replaced one, and reading it off disk still keeps this test owning one
	// thing: that every regular file under the placed entry gets verified,
	// whatever the copy left there.
	dstDir := filepath.Join(FilesDir(layout, "box"), filepath.Base(srcDir))
	want, err := digestTree(dstDir)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	require.Len(t, rt.calls, 1)
	assert.ElementsMatch(t, want, rt.calls[0], "every file in the placed tree is verified")
}

func TestDigestTree_SkipsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0600))
	require.NoError(t, os.Symlink("real.txt", filepath.Join(dir, "link.txt")))

	files, err := digestTree(dir)
	require.NoError(t, err)
	require.Len(t, files, 1, "the symlink is resolved by the guest, not hashed here")
	assert.Equal(t, "real.txt", filepath.Base(files[0].HostPath))
	assert.Equal(t, sha256Of("x"), files[0].SHA256)
}

func TestStaleList_CapsWhatItNames(t *testing.T) {
	var stale []runtime.GuestFileDigest
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		stale = append(stale, runtime.GuestFileDigest{HostPath: "/x/" + n})
	}
	assert.Equal(t, "a, b, c and 2 more", staleList(stale))
	assert.Equal(t, "a", staleList(stale[:1]))
}

// The exchange directory has two writers, not one. `files put` goes through
// ImportFile; the MCP file-write tool goes through WriteFile, which lands bytes
// in the same share with the same hazard. Fixing only the first would leave the
// path an agent's tooling actually uses still delivering fabricated content.
func TestWriteFile_ReplacementIsVerifiedAgainstTheGuest(t *testing.T) {
	rt := &refreshingRuntime{}
	engine, layout := newRefreshEngine(t, rt)
	ctx := context.Background()

	require.NoError(t, engine.WriteFile(ctx, "box", "notes.md", []byte("v1")))
	assert.Empty(t, rt.calls, "a first write replaces nothing")

	require.NoError(t, engine.WriteFile(ctx, "box", "notes.md", []byte("v2-different")))
	require.Len(t, rt.calls, 1)
	require.Len(t, rt.calls[0], 1)
	assert.Equal(t, filepath.Join(FilesDir(layout, "box"), "notes.md"), rt.calls[0][0].HostPath)
	assert.Equal(t, sha256Of("v2-different"), rt.calls[0][0].SHA256)
}

func TestWriteFile_UnrepairableStaleContentFailsTheWrite(t *testing.T) {
	rt := &refreshingRuntime{}
	engine, layout := newRefreshEngine(t, rt)
	ctx := context.Background()
	require.NoError(t, engine.WriteFile(ctx, "box", "notes.md", []byte("v1")))

	rt.stale = []runtime.GuestFileDigest{{
		HostPath: filepath.Join(FilesDir(layout, "box"), "notes.md"),
		SHA256:   "deadbeef",
	}}
	err := engine.WriteFile(ctx, "box", "notes.md", []byte("v2"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notes.md")
}

// DF177: `cp -rp src dst` copies INTO an existing directory. So a second
// --overwrite of a directory used to leave the old files in place and nest a copy
// beneath them, exiting 0 — "overwrite" that did not overwrite.
func TestImportFile_DirectoryOverwriteReplacesRatherThanNesting(t *testing.T) {
	engine, layout := newImportEngine(t, "mock")
	srcDir := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "keep.txt"), []byte("v1"), 0600))

	_, err := engine.ImportFile(context.Background(), "box", srcDir, false)
	require.NoError(t, err)

	// Second import: one file changed, one file gone from the source entirely.
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "keep.txt"), []byte("v2"), 0600))
	_, err = engine.ImportFile(context.Background(), "box", srcDir, true)
	require.NoError(t, err)

	dst := filepath.Join(FilesDir(layout, "box"), "bundle")
	assert.NoDirExists(t, filepath.Join(dst, "bundle"), "the copy must not nest inside the old directory")
	got, err := os.ReadFile(filepath.Join(dst, "keep.txt")) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "v2", string(got), "the replaced tree holds the new content, not the old")
}

// A file that the source no longer has must not survive the replace — that is the
// difference between a replace and the merge this used to be.
func TestImportFile_DirectoryOverwriteDropsFilesTheSourceRemoved(t *testing.T) {
	engine, layout := newImportEngine(t, "mock")
	srcDir := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "stale.txt"), []byte("old"), 0600))
	_, err := engine.ImportFile(context.Background(), "box", srcDir, false)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(srcDir, "stale.txt")))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "fresh.txt"), []byte("new"), 0600))
	_, err = engine.ImportFile(context.Background(), "box", srcDir, true)
	require.NoError(t, err)

	dst := filepath.Join(FilesDir(layout, "box"), "bundle")
	assert.NoFileExists(t, filepath.Join(dst, "stale.txt"), "a merge would have left this behind")
	assert.FileExists(t, filepath.Join(dst, "fresh.txt"))
}

// A single-file overwrite must NOT become an unlink+recreate: that is the one
// shape a tart guest cannot be made to re-read (DF175), so cp's truncate-in-place
// is load-bearing rather than incidental.
func TestImportFile_SingleFileOverwriteKeepsTheSameInode(t *testing.T) {
	engine, layout := newImportEngine(t, "mock")
	src := hostFile(t, "v1")
	_, err := engine.ImportFile(context.Background(), "box", src, false)
	require.NoError(t, err)

	dst := filepath.Join(FilesDir(layout, "box"), "payload.txt")
	before, err := os.Stat(dst)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(src, []byte("v2"), 0600))
	_, err = engine.ImportFile(context.Background(), "box", src, true)
	require.NoError(t, err)

	after, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after),
		"a file overwrite must truncate in place; unlinking it would create DF175's unrepairable case")
}

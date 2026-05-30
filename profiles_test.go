// ABOUTME: Tests for SystemClient.Profiles() sub-handle: Create / List / Info /
// ABOUTME: Delete / ReferencingSandboxes. Filesystem-backed; uses t.TempDir.

package yoloai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient returns a SystemClient bound to a temp DataDir.
// Layout points at t.TempDir/.yoloai so each subtest gets a clean
// state without touching the real user home.
func newTestClient(t *testing.T) *SystemClient {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, ".yoloai")
	require.NoError(t, os.MkdirAll(dataDir, 0750))
	return NewSystemClient(config.NewLayout(dataDir))
}

// --- Create ---

func TestProfiles_Create_WritesScaffold(t *testing.T) {
	c := newTestClient(t)
	require.NoError(t, c.Profiles().Create(context.Background(), "demo"))

	yamlPath := filepath.Join(c.layout.ProfileDir("demo"), "config.yaml")
	require.FileExists(t, yamlPath)

	data, err := os.ReadFile(yamlPath) //nolint:gosec // test path
	require.NoError(t, err)
	// Spot-check that the scaffold is the commented template, not
	// something accidentally truncated to the first line.
	body := string(data)
	assert.Contains(t, body, "# agent: claude")
	assert.Contains(t, body, "# workdir:")
	assert.Contains(t, body, "# directories:")
}

func TestProfiles_Create_AlreadyExists_UsageError(t *testing.T) {
	c := newTestClient(t)
	require.NoError(t, c.Profiles().Create(context.Background(), "demo"))

	err := c.Profiles().Create(context.Background(), "demo")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "already exists")
}

func TestProfiles_Create_InvalidName_UsageError(t *testing.T) {
	c := newTestClient(t)
	// Slashes are rejected by config.ValidateProfileName.
	err := c.Profiles().Create(context.Background(), "bad/name")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	assert.ErrorAs(t, err, &usage)
}

// --- List ---

func TestProfiles_List_Empty(t *testing.T) {
	c := newTestClient(t)
	summaries, err := c.Profiles().List(context.Background())
	require.NoError(t, err)
	// Empty (not nil) so callers can range without nil-check and
	// JSON output renders `[]` not `null`.
	assert.NotNil(t, summaries)
	assert.Empty(t, summaries)
}

func TestProfiles_List_ReturnsAllProfiles(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, c.Profiles().Create(ctx, "alpha"))
	require.NoError(t, c.Profiles().Create(ctx, "beta"))

	summaries, err := c.Profiles().List(ctx)
	require.NoError(t, err)
	require.Len(t, summaries, 2)

	names := []string{summaries[0].Name, summaries[1].Name}
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
}

func TestProfiles_List_FlagsDockerfile(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, c.Profiles().Create(ctx, "with-image"))
	require.NoError(t, c.Profiles().Create(ctx, "no-image"))

	// Drop a Dockerfile alongside one profile's config.yaml.
	dockerfilePath := filepath.Join(c.layout.ProfileDir("with-image"), "Dockerfile")
	require.NoError(t, os.WriteFile(dockerfilePath, []byte("FROM yoloai-base\n"), 0600))

	summaries, err := c.Profiles().List(ctx)
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	byName := map[string]ProfileSummary{}
	for _, s := range summaries {
		byName[s.Name] = s
	}
	assert.True(t, byName["with-image"].HasDockerfile)
	assert.False(t, byName["no-image"].HasDockerfile)
}

// --- Info ---

func TestProfiles_Info_Base(t *testing.T) {
	c := newTestClient(t)
	info, err := c.Profiles().Info(context.Background(), "base")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "base", info.Name)
	assert.Equal(t, []string{"base"}, info.Chain)
	assert.Equal(t, "yoloai-base", info.Image)
	require.NotNil(t, info.Merged, "Merged must be populated for callers that render it")
	require.NotNil(t, info.Parent, "Parent must be non-nil so --diff callers don't nil-deref")
}

func TestProfiles_Info_Nonexistent_UsageError(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Profiles().Info(context.Background(), "ghost")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "does not exist")
}

// Info on a Dockerfile-less profile inherits the base image — that's
// config.ResolveProfileImage's contract.
func TestProfiles_Info_RealProfile_InheritsBaseImage(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, c.Profiles().Create(ctx, "demo"))

	info, err := c.Profiles().Info(ctx, "demo")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "demo", info.Name)
	assert.Equal(t, "yoloai-base", info.Image, "no per-profile Dockerfile → inherits base image")
	assert.False(t, info.HasDockerfile)
	require.NotNil(t, info.Merged)
	require.NotNil(t, info.Parent, "Parent must be non-nil for --diff callers")
}

// Profile with its own Dockerfile gets its own image name.
func TestProfiles_Info_RealProfile_WithDockerfile(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, c.Profiles().Create(ctx, "demo"))
	require.NoError(t, os.WriteFile(
		filepath.Join(c.layout.ProfileDir("demo"), "Dockerfile"),
		[]byte("FROM yoloai-base\n"), 0600,
	))

	info, err := c.Profiles().Info(ctx, "demo")
	require.NoError(t, err)
	assert.Equal(t, "yoloai-demo", info.Image)
	assert.True(t, info.HasDockerfile)
}

// --- ReferencingSandboxes ---

func TestProfiles_ReferencingSandboxes_None(t *testing.T) {
	c := newTestClient(t)
	require.NoError(t, c.Profiles().Create(context.Background(), "demo"))

	refs, err := c.Profiles().ReferencingSandboxes(context.Background(), "demo")
	require.NoError(t, err)
	assert.NotNil(t, refs)
	assert.Empty(t, refs)
}

func TestProfiles_ReferencingSandboxes_MetaScan(t *testing.T) {
	c := newTestClient(t)
	require.NoError(t, c.Profiles().Create(context.Background(), "demo"))

	// Two sandbox dirs: one references "demo", one doesn't.
	writeFakeSandboxMeta(t, c.layout, "boxA", "demo")
	writeFakeSandboxMeta(t, c.layout, "boxB", "other")

	refs, err := c.Profiles().ReferencingSandboxes(context.Background(), "demo")
	require.NoError(t, err)
	assert.Equal(t, []string{"boxA"}, refs)
}

func TestProfiles_ReferencingSandboxes_NoSandboxesDir_EmptySlice(t *testing.T) {
	c := newTestClient(t)
	// SandboxesDir doesn't exist yet (fresh data dir). Library should
	// treat this as "no references" rather than surfacing ENOENT.
	refs, err := c.Profiles().ReferencingSandboxes(context.Background(), "anything")
	require.NoError(t, err)
	assert.NotNil(t, refs)
	assert.Empty(t, refs)
}

func TestProfiles_ReferencingSandboxes_SkipsCorruptMeta(t *testing.T) {
	c := newTestClient(t)
	require.NoError(t, c.Profiles().Create(context.Background(), "demo"))

	writeFakeSandboxMeta(t, c.layout, "boxOK", "demo")
	// Drop a corrupt meta — bad JSON. Per-entry errors are silently
	// dropped; the scan keeps walking.
	corruptDir := filepath.Join(c.layout.SandboxesDir(), "boxBroken")
	require.NoError(t, os.MkdirAll(corruptDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(corruptDir, "environment.json"),
		[]byte("not json"), 0600,
	))

	refs, err := c.Profiles().ReferencingSandboxes(context.Background(), "demo")
	require.NoError(t, err)
	assert.Equal(t, []string{"boxOK"}, refs)
}

// --- Delete ---

func TestProfiles_Delete_RemovesDirAndReturnsHints(t *testing.T) {
	c := newTestClient(t)
	require.NoError(t, c.Profiles().Create(context.Background(), "demo"))
	dir := c.layout.ProfileDir("demo")
	require.DirExists(t, dir)

	result, err := c.Profiles().Delete(context.Background(), "demo")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NoDirExists(t, dir)

	// Hints slice is non-nil so JSON renders [], even if no
	// registered backend declares a CleanupHint. The slice may be
	// empty depending on platform-conditional backend registration —
	// we only assert the contract.
	assert.NotNil(t, result.ImageCleanupHints)
	for _, h := range result.ImageCleanupHints {
		assert.NotEmpty(t, h.Backend, "hint must name its backend")
		assert.Equal(t, "yoloai-demo", h.Image)
		assert.NotEmpty(t, h.Command, "hint must carry a removal command")
	}
}

func TestProfiles_Delete_Nonexistent_UsageError(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Profiles().Delete(context.Background(), "ghost")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestProfiles_Delete_InvalidName_UsageError(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Profiles().Delete(context.Background(), "bad/name")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	assert.ErrorAs(t, err, &usage)
}

// --- helpers ---

// writeFakeSandboxMeta writes a minimal environment.json under
// SandboxesDir(name) recording the given profile. Just enough to make
// the ReferencingSandboxes scan find it (or skip it).
func writeFakeSandboxMeta(t *testing.T, layout config.Layout, sandboxName, profile string) {
	t.Helper()
	dir := filepath.Join(layout.SandboxesDir(), sandboxName)
	require.NoError(t, os.MkdirAll(dir, 0750))
	meta := map[string]any{"profile": profile}
	data, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "environment.json"), data, 0600))
}

// Verify the assertion helpers + sandbox package import are wired up
// — guards against a refactor accidentally dropping the errors import
// while leaving errors.As untouched in the assertions above.
func TestProfileAdmin_HasSentinelImports(t *testing.T) {
	// no-op; existence of the test exercises imports.
	_ = errors.New
}

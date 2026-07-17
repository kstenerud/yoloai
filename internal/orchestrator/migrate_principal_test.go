package orchestrator

// ABOUTME: Unit tests for the v4->v5 principal-rename migrator — the classify
// ABOUTME: decision matrix, the guarded schema stamp, and Apply's backend ops.

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyPrincipalRename(t *testing.T) {
	const bt = runtime.BackendType("mock")
	tests := []struct {
		name          string
		status        status.Status
		hasInstance   bool
		renameCapable bool
		wantAuth      migrate.Auth
	}{
		// No backend instance (seatbelt): metadata-only, benign.
		{"seatbelt running", status.StatusActive, false, false, migrate.AuthNone},
		{"seatbelt stopped", status.StatusStopped, false, false, migrate.AuthNone},

		// Rename-capable (docker/podman/tart): rename preserves a running instance.
		{"rename running", status.StatusActive, true, true, migrate.AuthNone},
		{"rename stopped", status.StatusStopped, true, true, migrate.AuthNone},
		{"rename suspended", status.StatusSuspended, true, true, migrate.AuthNone},
		{"rename removed", status.StatusRemoved, true, true, migrate.AuthNone},
		{"rename unauditable", status.StatusBroken, true, true, migrate.AuthBlocked},

		// Recreate-only (containerd/apple): a running/suspended instance is refused,
		// a stopped/removed one is recreate-on-next-start behind --yes.
		{"recreate active", status.StatusActive, true, false, migrate.AuthBlocked},
		{"recreate idle", status.StatusIdle, true, false, migrate.AuthBlocked},
		{"recreate suspended", status.StatusSuspended, true, false, migrate.AuthBlocked},
		{"recreate stopped", status.StatusStopped, true, false, migrate.AuthConfirm},
		{"recreate removed", status.StatusRemoved, true, false, migrate.AuthConfirm},
		{"recreate unauditable", status.StatusUnavailable, true, false, migrate.AuthBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op := classifyPrincipalRename("box", bt, tc.status, tc.hasInstance, tc.renameCapable)
			assert.Equal(t, tc.wantAuth, op.Auth, "op=%q", op.Description)
			assert.Equal(t, "box", op.Sandbox)
		})
	}
}

func TestRestampedImageRef(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	tests := []struct {
		name     string
		imageRef string
		want     string
	}{
		{"principal-authored profile image gets scoped", "yoloai-web", "yoloai-cli-web"},
		{"base image is left unscoped", config.BaseImage, config.BaseImage},
		{"empty ref is left alone", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, restampedImageRef(layout, tc.imageRef))
		})
	}
}

func TestStampSchemaAdvancing(t *testing.T) {
	t.Run("advances an unstamped realm", func(t *testing.T) {
		layout := config.NewLayout(t.TempDir())
		require.NoError(t, stampSchemaAdvancing(layout, config.SchemaPrincipalRenamed))
		v, exists, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, config.SchemaPrincipalRenamed, v)
	})

	t.Run("never lowers a higher stamp", func(t *testing.T) {
		layout := config.NewLayout(t.TempDir())
		require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaPrincipalRenamed))
		// The overlay migrator's older target must not drag the stamp back down.
		require.NoError(t, stampSchemaAdvancing(layout, config.SchemaOverlayFlattened))
		v, _, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
		require.NoError(t, err)
		assert.Equal(t, config.SchemaPrincipalRenamed, v)
	})
}

// ---- Apply-level tests with a fake backend ----

// fakeBackend is a minimal runtime.Backend for the migrator tests. It does NOT
// implement runtime.Renamer (recreate-only or seatbelt-like backends). Inspect
// is driven by the fields so DetectStatus yields a chosen status.
type fakeBackend struct {
	keepAlive   runtime.KeepAliveModel
	running     bool
	suspended   bool
	notFound    bool
	removeCalls []string
}

func (f *fakeBackend) Setup(context.Context, config.Layout, string, io.Writer, *slog.Logger, bool) error {
	return nil
}
func (f *fakeBackend) IsReady(context.Context) (bool, error)                { return true, nil }
func (f *fakeBackend) Create(context.Context, runtime.InstanceConfig) error { return nil }
func (f *fakeBackend) Start(context.Context, string) error                  { return nil }
func (f *fakeBackend) Stop(context.Context, string) error                   { return nil }
func (f *fakeBackend) Remove(_ context.Context, name string) error {
	f.removeCalls = append(f.removeCalls, name)
	return nil
}
func (f *fakeBackend) Inspect(context.Context, string) (runtime.InstanceInfo, error) {
	if f.notFound {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}
	return runtime.InstanceInfo{Running: f.running, Suspended: f.suspended}, nil
}
func (f *fakeBackend) Exec(context.Context, string, []string, string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (f *fakeBackend) InteractiveExec(context.Context, string, []string, string, string, runtime.IOStreams) error {
	return nil
}
func (f *fakeBackend) Prune(context.Context, []string, bool, io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, nil
}
func (f *fakeBackend) Close() error           { return nil }
func (f *fakeBackend) DiagHint(string) string { return "" }
func (f *fakeBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{Type: "mock", Capabilities: runtime.BackendCaps{KeepAliveModel: f.keepAlive}}
}

// fakeRenamer is a fakeBackend that also implements runtime.Renamer.
type fakeRenamer struct {
	fakeBackend
	renames [][2]string
}

func (f *fakeRenamer) Rename(_ context.Context, oldName, newName string) error {
	f.renames = append(f.renames, [2]string{oldName, newName})
	return nil
}

// seedSandbox writes an unmigrated (empty-principal) environment.json for name.
// An optional imageRef seeds Environment.ImageRef (pre-v5 bare "yoloai-<X>"
// shape); omitted, it is left empty.
func seedSandbox(t *testing.T, layout config.Layout, name string, imageRef ...string) {
	t.Helper()
	var ref string
	if len(imageRef) > 0 {
		ref = imageRef[0]
	}
	sandboxDir := layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0o750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Version:     3,
		Name:        name,
		BackendType: "mock",
		ImageRef:    ref,
		Dirs:        []store.DirEnvironment{{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy}},
	}))
}

func newPrincipalRenameWith(layout config.Layout, rt runtime.Backend) *PrincipalRename {
	return NewPrincipalRename(layout, layout.SandboxesDir(), func(context.Context, runtime.BackendType) (runtime.Backend, error) {
		return rt, nil
	})
}

func TestPrincipalRename_Apply_RenamesRunningAndRestamps(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedSandbox(t, layout, "box")
	rt := &fakeRenamer{fakeBackend: fakeBackend{running: true}}
	m := newPrincipalRenameWith(layout, rt)

	rep, err := m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.Equal(t, []string{"box"}, rep.Migrated)

	// A running instance is renamed in place, old -> new.
	require.Equal(t, [][2]string{{"yoloai-box", "yoloai-cli-box"}}, rt.renames)
	assert.Empty(t, rt.removeCalls, "rename backend must not remove")

	// The stored principal is re-stamped to the target...
	env, err := store.LoadEnvironment(layout.SandboxDir("box"))
	require.NoError(t, err)
	assert.Equal(t, config.CLIPrincipal, env.Principal)
	// ...and the realm advances to v5.
	v, _, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, config.SchemaPrincipalRenamed, v)

	// Idempotent: a second run finds nothing unmigrated and does not rename again.
	_, err = m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.Len(t, rt.renames, 1, "already-migrated sandbox must be skipped on re-run")
}

func TestPrincipalRename_Apply_RecreateOnlyStoppedNeedsYes(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedSandbox(t, layout, "box")
	rt := &fakeBackend{keepAlive: runtime.KeepAliveGuestOSInit, running: false} // stopped, no Renamer
	m := newPrincipalRenameWith(layout, rt)

	// Without --yes the recreate is refused and nothing is touched.
	_, err := m.Apply(context.Background(), migrate.Decision{})
	require.Error(t, err)
	assert.Empty(t, rt.removeCalls)
	env, err := store.LoadEnvironment(layout.SandboxDir("box"))
	require.NoError(t, err)
	assert.Equal(t, config.PrincipalSegment(""), env.Principal, "refused migration must not re-stamp")

	// With --yes the old instance is removed (recreate-on-next-start) and re-stamped.
	_, err = m.Apply(context.Background(), migrate.Decision{Yes: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"yoloai-box"}, rt.removeCalls)
	env, err = store.LoadEnvironment(layout.SandboxDir("box"))
	require.NoError(t, err)
	assert.Equal(t, config.CLIPrincipal, env.Principal)
}

func TestPrincipalRename_Apply_RecreateOnlyRunningRefused(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedSandbox(t, layout, "box")
	rt := &fakeBackend{keepAlive: runtime.KeepAliveGuestOSInit, running: true} // running, no Renamer
	m := newPrincipalRenameWith(layout, rt)

	_, err := m.Apply(context.Background(), migrate.Decision{Yes: true})
	require.Error(t, err, "a running recreate-only instance must be refused even with --yes")
	assert.Empty(t, rt.removeCalls)
}

func TestPrincipalRename_Apply_SeatbeltRestampOnly(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedSandbox(t, layout, "box")
	rt := &fakeBackend{keepAlive: runtime.KeepAliveHostKeepAlive} // no container/VM
	m := newPrincipalRenameWith(layout, rt)

	_, err := m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.Empty(t, rt.removeCalls, "seatbelt has no backend instance to touch")
	env, err := store.LoadEnvironment(layout.SandboxDir("box"))
	require.NoError(t, err)
	assert.Equal(t, config.CLIPrincipal, env.Principal)
}

func TestPrincipalRename_Apply_RestampsProfileImageRef(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedSandbox(t, layout, "web", "yoloai-web")
	seedSandbox(t, layout, "plain", config.BaseImage)
	rt := &fakeBackend{keepAlive: runtime.KeepAliveHostKeepAlive} // no container/VM to touch
	m := newPrincipalRenameWith(layout, rt)

	_, err := m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)

	// A principal-authored profile image is re-stamped with the target principal.
	env, err := store.LoadEnvironment(layout.SandboxDir("web"))
	require.NoError(t, err)
	assert.Equal(t, "yoloai-cli-web", env.ImageRef)

	// The unscoped base image is left untouched.
	env, err = store.LoadEnvironment(layout.SandboxDir("plain"))
	require.NoError(t, err)
	assert.Equal(t, config.BaseImage, env.ImageRef)
}

func TestPrincipalRename_Plan_RunningRecreateOnlyBlocks(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedSandbox(t, layout, "box")
	rt := &fakeBackend{keepAlive: runtime.KeepAliveGuestOSInit, running: true}
	m := newPrincipalRenameWith(layout, rt)

	plan, err := m.Plan(context.Background())
	require.NoError(t, err)
	require.Len(t, plan.Ops, 1)
	assert.Equal(t, migrate.AuthBlocked, plan.Ops[0].Auth)
}

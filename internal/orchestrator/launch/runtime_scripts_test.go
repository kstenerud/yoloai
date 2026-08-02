// ABOUTME: Tests that the runtime scripts are delivered from the running binary
// ABOUTME: at launch rather than read out of the image, and that the firewall
// ABOUTME: sidecar receives them too (DF156 remedy c).

package launch

import (
	"context"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/store"
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptProviderBackend is a Backend that bakes runtime scripts and can run a
// netns sidecar — the shape docker actually has, and the only shape for which
// both halves of this change apply. Both capabilities are composed in
// deliberately: a fake free to invent a capability combination the product does
// not contain is how a previous version of this workstream came to test a path
// nothing could reach (A15).
type scriptProviderBackend struct {
	runtime.Backend
	writtenTo string
	writeErr  error
	gotSpec   *runtime.NetnsSidecarSpec
}

func (b *scriptProviderBackend) WriteRuntimeScripts(dir string) error {
	if b.writeErr != nil {
		return b.writeErr
	}
	b.writtenTo = dir
	return os.MkdirAll(dir, 0o750)
}

func (b *scriptProviderBackend) RunNetnsSidecar(_ context.Context, spec runtime.NetnsSidecarSpec) error {
	b.gotSpec = &spec
	return nil
}

// bakelessBackend has no image concept, so it already delivers its scripts at
// launch (tart writes them itself; seatbelt runs them as host processes) and must
// be left alone rather than handed a mount it has no use for.
type bakelessBackend struct{ runtime.Backend }

func TestDeliverRuntimeScripts_MountsTheBinaryOwnCopy(t *testing.T) {
	sandboxDir := t.TempDir()
	rt := &scriptProviderBackend{}

	mounts, err := deliverRuntimeScripts(state.Deps{Runtime: rt}, &state.State{SandboxDir: sandboxDir})
	require.NoError(t, err)

	wantDir := store.BinPath(sandboxDir)
	assert.Equal(t, wantDir, rt.writtenTo,
		"scripts must be materialised into the sandbox's own bin/, the directory tart already uses")
	require.Len(t, mounts, 1, "a baking backend must be given the mount, or it keeps reading the image's copies")
	assert.Equal(t, wantDir, mounts[0].HostPath)
	assert.Equal(t, "/yoloai/bin", mounts[0].ContainerPath)
	assert.True(t, mounts[0].ReadOnly, "nothing in the guest has business writing yoloAI's own scripts")
}

func TestDeliverRuntimeScripts_SkipsBackendsThatAlreadyDeliver(t *testing.T) {
	mounts, err := deliverRuntimeScripts(
		state.Deps{Runtime: &bakelessBackend{}},
		&state.State{SandboxDir: t.TempDir()},
	)
	require.NoError(t, err)
	assert.Nil(t, mounts, "tart and seatbelt deliver at launch already; a /yoloai/bin mount there is meaningless")
}

// TestInstallFirewallSidecar_CarriesTheScriptMount is the one that matters, and
// the one a mount-only change silently fails.
//
// A sidecar runs from an IMAGE and inherits none of the target's mounts. So
// delivering the scripts to the agent container does nothing for the firewall
// installer, which runs as a sidecar reading install-firewall.py — and that is
// precisely DF156's sharp case, the one that fails the launch outright instead of
// drifting. Without the mount here the whole remedy looks like it works (fresh
// scripts in the agent container) while the hard-failure path stays broken.
func TestInstallFirewallSidecar_CarriesTheScriptMount(t *testing.T) {
	sandboxDir := t.TempDir()
	testutil.WriteSandboxRecord(t, store.RuntimeConfigFilePath(sandboxDir),
		[]byte(`{"allowed_domains":["api.anthropic.com"]}`))

	rt := &scriptProviderBackend{}
	st := &state.State{Name: "box", SandboxDir: sandboxDir, ImageRef: "yoloai-base:latest"}

	require.NoError(t, installFirewallSidecar(context.Background(), rt, st, "yoloai-cli-box", brokerOutcome{}))

	require.NotNil(t, rt.gotSpec)
	require.Len(t, rt.gotSpec.Mounts, 1,
		"the sidecar reads install-firewall.py from the image unless the script mount is repeated for it")
	assert.Equal(t, store.BinPath(sandboxDir), rt.gotSpec.Mounts[0].HostPath)
	assert.Equal(t, "/yoloai/bin", rt.gotSpec.Mounts[0].ContainerPath)
	assert.True(t, rt.gotSpec.Mounts[0].ReadOnly)
	assert.Equal(t, []string{"python3", "/yoloai/bin/install-firewall.py"}, rt.gotSpec.Argv,
		"the installer is still invoked through the interpreter, not relying on the exec bit surviving the mount")
}

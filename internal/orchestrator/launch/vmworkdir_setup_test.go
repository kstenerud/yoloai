// ABOUTME: ExecuteVMWorkDirSetup must exec the backend's setup commands into the
// ABOUTME: VM and record the SHA it reads back, for a SandboxSide backend, and do
// ABOUTME: nothing for a HostSide one. Written while investigating DF122 (which
// ABOUTME: turned out not to be a bug); kept because this mechanism was untested.
package launch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// recordingVMBackend is a SandboxSide backend that records the commands
// ExecuteVMWorkDirSetup execs into the VM and answers the git rev-parse readback
// with a fixed SHA. The embedded nil Backend panics on any other method, so the
// test proves the setup path touches only Exec.
type recordingVMBackend struct {
	runtime.Backend
	setupCmds []string // what SetupWorkDirInVM was asked to run
	execCmds  [][]string
	baseline  string
}

func (r *recordingVMBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "fake-vm",
		BaseModeName: runtime.IsolationModeVM,
		Capabilities: runtime.BackendCaps{FilesystemLocality: runtime.LocalitySandboxSide},
	}
}

func (r *recordingVMBackend) SetupWorkDirInVM(_, vmLocalPath string) []string {
	// Shape mirrors tart's: rsync from staging, then git init/add/commit.
	r.setupCmds = []string{
		"mkdir -p " + filepath.Dir(vmLocalPath),
		"cd " + vmLocalPath + " && git init && git add -A && git commit --allow-empty -m baseline",
	}
	return r.setupCmds
}

func (r *recordingVMBackend) Exec(_ context.Context, _ string, cmd []string, _ string) (runtime.ExecResult, error) {
	r.execCmds = append(r.execCmds, cmd)
	// The last exec ExecuteVMWorkDirSetup runs is `git ... rev-parse HEAD`.
	if len(cmd) > 0 && cmd[len(cmd)-1] == "HEAD" {
		return runtime.ExecResult{Stdout: r.baseline + "\n"}, nil
	}
	return runtime.ExecResult{}, nil
}

// A deferred (empty) baseline drives the VM-side work-dir setup: the reset flow's
// gated call fires on `Mode == copy && BaselineSHA == ""`. (The recreate path runs
// the same setup unconditionally, which is why the gate turned out not to matter —
// see DF122's retraction — but the setup itself must work, and was untested.)
func TestExecuteVMWorkDirSetup_RunsSetupAndRecordsBaseline(t *testing.T) {
	const wantSHA = "1111111111111111111111111111111111111111"
	rt := &recordingVMBackend{baseline: wantSHA}

	sandboxDir := t.TempDir()
	meta := &store.Environment{
		Name:      "vmbox",
		Principal: config.CLIPrincipal,
		Dirs: []store.DirEnvironment{{
			HostPath:    "/home/user/project",
			Mode:        store.DirModeCopy,
			BaselineSHA: "", // deferred at reset — the case DF122 was about
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	err := ExecuteVMWorkDirSetup(context.Background(), rt, "vmbox", sandboxDir, meta)
	require.NoError(t, err)

	require.NotEmpty(t, rt.setupCmds, "the backend's setup commands must have been requested")
	assert.GreaterOrEqual(t, len(rt.execCmds), len(rt.setupCmds)+1,
		"every setup command plus the rev-parse readback must be exec'd into the VM")

	// The baseline the VM reported is recorded, in memory and on disk.
	assert.Equal(t, wantSHA, meta.Dirs[0].BaselineSHA)
	reloaded, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, wantSHA, reloaded.Dirs[0].BaselineSHA, "the recorded baseline must be persisted")
}

// A HostSide backend baselines on the host, so the VM setup is a no-op — it must
// not exec anything. This is the boundary that keeps the empty-SHA trigger from
// firing on the wrong backend.
func TestExecuteVMWorkDirSetup_HostSideIsNoOp(t *testing.T) {
	rt := &recordingVMBackend{}
	// Override to HostSide by wrapping: simplest is a distinct type, but reusing
	// the recorder with a HostSide descriptor keeps the exec assertion honest.
	hostSide := &hostSideRecorder{recordingVMBackend: rt}

	sandboxDir := t.TempDir()
	meta := &store.Environment{Name: "box", Dirs: []store.DirEnvironment{{HostPath: "/p", Mode: store.DirModeCopy}}}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	require.NoError(t, ExecuteVMWorkDirSetup(context.Background(), hostSide, "box", sandboxDir, meta))
	assert.Empty(t, rt.execCmds, "a HostSide backend must not exec VM setup commands")
}

type hostSideRecorder struct{ *recordingVMBackend }

func (h *hostSideRecorder) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "fake-host",
		BaseModeName: runtime.IsolationModeContainer,
		Capabilities: runtime.BackendCaps{FilesystemLocality: runtime.LocalityHostSide},
	}
}

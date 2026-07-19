//go:build integration

// ABOUTME: Backend-agnostic behavioral conformance suite. Exercises the
// ABOUTME: runtime.Backend contract through interface methods only, so every
// ABOUTME: backend (docker, podman, containerd, tart, seatbelt, apple) verifies
// ABOUTME: one shared table. Sections a backend cannot support are declared
// ABOUTME: skipped (with a reason) rather than forced, keeping results legible.
package runtimetest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
)

// Sleeper creates (but does not start) a long-running instance with cfg applied,
// registers its teardown via t.Cleanup, and returns the instance name. How a
// backend keeps a container/VM alive for exec tests is genuinely backend-
// specific — an OCI "sleep infinity" entrypoint override (docker/podman), a
// sleep image (apple/containerd), or a VM/host process (tart/seatbelt) — so each
// backend supplies its own. The suite owns naming (cfg.Name is pre-set) and the
// behavioral knobs it varies (mounts); the Sleeper fills backend defaults
// (image, entrypoint) and handles any pre-create eviction of stale leftovers.
type Sleeper func(t *testing.T, cfg runtime.InstanceConfig) string

// InterfaceBackend is the per-test fixture a backend hands the conformance suite.
type InterfaceBackend struct {
	Runtime    runtime.Backend
	Ctx        context.Context
	NewSleeper Sleeper

	// SkipMounts and SkipStdio each name a behavioral section this backend
	// cannot honor, with a one-line reason. Non-empty → the suite reports a
	// SKIP for that section (legible result) instead of forcing an inapplicable
	// assertion. Empty → the section runs. Examples: a backend whose state lives
	// directly on the host filesystem has no bind-mount semantics to verify; a
	// backend that does not implement runtime.StdioExecer skips the stdio
	// section (the suite also detects that case automatically).
	SkipMounts string
	SkipStdio  string

	// SharesReadOnlyInstance opts an expensive-to-boot backend (the VM backends)
	// into serving every read-only subtest from ONE shared running instance
	// instead of booting a fresh one per subtest — the dominant cost on tart,
	// where a boot is a multi-GB clone. A sharing backend runs its subtests
	// serially (the shared instance is scoped so its VM slot frees before the
	// mutating subtests boot; parallelising exec against it awaits the Mac's
	// verification that the backend supports concurrent exec). Container backends
	// leave this false: boots are cheap, so they skip sharing (confining the
	// isolation risk to the two backends that benefit) and parallelise instead.
	SharesReadOnlyInstance bool

	// MaxConcurrentInstances caps how many instances of this backend boot at once
	// under parallelism (non-sharing backends). 0 = unbounded. A backend
	// implementing runtime.VMCensusReporter overrides this with the live free-slot
	// count, so the one hard platform limit (tart's macOS 2-VM cap) reads its
	// value from the machine that enforces it, honouring a foreign VM the run
	// cannot shut down, rather than a hard-coded literal.
	MaxConcurrentInstances int
}

// instanceGate bounds how many instances boot concurrently. A nil tokens channel
// means unbounded. It is the one place the per-backend concurrency policy — a
// static cap, or tart's dynamic free-slot census — turns into a limit.
type instanceGate struct{ tokens chan struct{} }

func (g *instanceGate) acquire() {
	if g.tokens != nil {
		g.tokens <- struct{}{}
	}
}

func (g *instanceGate) release() {
	if g.tokens != nil {
		<-g.tokens
	}
}

// newInstanceGate sizes the gate from the backend's policy. A VMCensusReporter
// (tart) overrides the static cap with the live free-slot count (Limit − in-use)
// so parallelism never tries to boot past the platform limit; zero free slots is
// a fail-loud at suite start (with the occupants named) rather than a mid-run
// boot failure or a hang.
func newInstanceGate(t *testing.T, b InterfaceBackend) *instanceGate {
	t.Helper()
	limit := b.MaxConcurrentInstances
	if census, ok, err := runtime.VMCensusFor(context.Background(), b.Runtime); ok {
		require.NoError(t, err, "VM census must be readable to size the concurrency gate")
		free := census.Limit - census.InUse()
		require.Positivef(t, free, "no free VM slots: %d/%d occupied (%s) — free one to run this suite",
			census.InUse(), census.Limit, occupantNames(census))
		limit = free
	}
	if limit <= 0 {
		return &instanceGate{} // unbounded
	}
	return &instanceGate{tokens: make(chan struct{}, limit)}
}

func occupantNames(c runtime.VMCensus) string {
	names := make([]string, 0, len(c.Slots))
	for _, s := range c.Slots {
		if s.VMName != "" {
			names = append(names, s.VMName)
		}
	}
	if len(names) == 0 {
		return "unnamed VM process(es)"
	}
	return strings.Join(names, ", ")
}

// InterfaceSetupFunc connects to a backend and returns a fresh per-test fixture
// with cleanup already registered (e.g. rt.Close via t.Cleanup).
type InterfaceSetupFunc func(t *testing.T) InterfaceBackend

// conformanceInstanceName flattens the subtest name into a legal instance name.
// Subtest names carry a "/" (e.g. "TestAppleConformance/ExecSimple"), illegal in
// a container/VM name. Kept in the shared suite so every backend names instances
// identically.
func conformanceInstanceName(t *testing.T) string {
	t.Helper()
	return "yoloai-test-" + strings.ReplaceAll(t.Name(), "/", "-")
}

// RunInterfaceConformance exercises the universal runtime.Backend contract every
// backend must honor, plus capability-gated sections a backend opts into via the
// InterfaceBackend skip fields.
//
// Subtests fall into three classes. No-instance property checks and mutating
// lifecycle subtests each open their own fixture (setup) and, for a mutating
// subtest, their own instance — preserving per-subtest isolation. The read-only
// exec subtests are the amortisation target: a SharesReadOnlyInstance backend
// runs them serially against ONE shared instance (the big win on VM backends,
// where a boot is a multi-GB clone), while a container backend boots one per
// subtest and runs them in parallel. The instanceGate bounds concurrent boots
// per the backend's policy (a static cap, or tart's live free-slot census).
func RunInterfaceConformance(t *testing.T, setup InterfaceSetupFunc) {
	// sleeper creates a started, long-running instance and returns its name.
	sleeper := func(t *testing.T, b InterfaceBackend, cfg runtime.InstanceConfig) string {
		t.Helper()
		if cfg.Name == "" {
			cfg.Name = conformanceInstanceName(t)
		}
		name := b.NewSleeper(t, cfg)
		require.NoError(t, b.Runtime.Start(b.Ctx, name))
		return name
	}

	// One shared fixture reads the backend's sharing/concurrency policy and, for a
	// VM backend, its live free-slot census; it also hosts the shared read-only
	// instance. Isolated subtests still open their own fixture via setup(t), so a
	// parallel run keeps each subtest's runtime independent.
	probe := setup(t)
	gate := newInstanceGate(t, probe)
	shares := probe.SharesReadOnlyInstance

	// parallelize marks a subtest parallel unless the backend shares one instance:
	// a sharing backend runs serially so the shared instance's VM slot is freed
	// before the next boot, which is what keeps it correct at a single free slot.
	parallelize := func(t *testing.T) {
		if !shares {
			t.Parallel()
		}
	}

	// boot starts an instance and holds a concurrency token for its whole lifetime:
	// acquire, then register the release BEFORE NewSleeper registers its removal so
	// LIFO cleanup frees the slot only after the instance is actually gone.
	boot := func(t *testing.T, b InterfaceBackend, cfg runtime.InstanceConfig) string {
		gate.acquire()
		t.Cleanup(gate.release)
		return sleeper(t, b, cfg)
	}

	// --- Property invariants (no instance) ---

	// Every backend MUST run the work-copy git in confinement (audit C1): git
	// operates on agent-controlled content, and its attribute-bound filter/textconv
	// drivers cannot be disabled without breaking Git LFS/git-crypt, so the only
	// defense is running that git where the agent's planted commands can't reach
	// the host. This is a hard requirement, not a preference: the history-downgrade
	// fallback that once degraded an unconfined backend to copy-strict was deleted
	// (DF119) precisely because this invariant holds, so a backend that violated it
	// would silently reintroduce the RCE (confine-host-side-git.md). A confining
	// backend must also implement GitExecer, which git.NewSandbox dispatches through.
	t.Run("BackendConfinesWorkCopyGit", func(t *testing.T) {
		b := setup(t)
		assert.True(t, runtime.GitRunsInConfinement(b.Runtime),
			"every backend must confine work-copy git (SandboxSide filesystem or GitExecInConfinement); the unconfined fallback was removed in DF119")
		_, isGitExecer := b.Runtime.(runtime.GitExecer)
		assert.True(t, isGitExecer, "a confining backend must implement runtime.GitExecer (git runs in the sandbox)")
	})

	// A SandboxSide backend additionally keeps its work copy inside the sandbox,
	// so baseline creation is deferred to the sandbox (WorkDirSetup). The
	// property-based dispatch in ExecuteVMWorkDirSetup assumes this invariant.
	t.Run("SandboxSideImplementsLocalityOps", func(t *testing.T) {
		b := setup(t)
		if b.Runtime.Descriptor().Capabilities.FilesystemLocality != runtime.LocalitySandboxSide {
			t.Skip("HostSide backend: no in-sandbox locality operations required")
		}
		_, isWorkDirSetup := b.Runtime.(runtime.WorkDirSetup)
		assert.True(t, isWorkDirSetup, "SandboxSide backend must implement runtime.WorkDirSetup (baseline deferred to sandbox)")
	})

	t.Run("InspectNotFound", func(t *testing.T) {
		parallelize(t)
		b := setup(t)
		_, err := b.Runtime.Inspect(b.Ctx, "yoloai-nonexistent-instance-xyz")
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	t.Run("IsReady", func(t *testing.T) {
		parallelize(t)
		b := setup(t)
		ready, err := b.Runtime.IsReady(b.Ctx)
		require.NoError(t, err)
		assert.True(t, ready)
	})

	// --- Mutating lifecycle (each its own instance) ---

	mutating := func(name string, body func(t *testing.T, b InterfaceBackend)) {
		t.Run(name, func(t *testing.T) {
			parallelize(t)
			body(t, setup(t))
		})
	}

	mutating("CreateStartStopRemove", func(t *testing.T, b InterfaceBackend) {
		name := conformanceInstanceName(t)
		gate.acquire()
		t.Cleanup(gate.release)
		created := b.NewSleeper(t, runtime.InstanceConfig{Name: name})

		require.NoError(t, b.Runtime.Start(b.Ctx, created))
		info, err := b.Runtime.Inspect(b.Ctx, created)
		require.NoError(t, err)
		assert.True(t, info.Running)

		require.NoError(t, b.Runtime.Stop(b.Ctx, created))
		info, err = b.Runtime.Inspect(b.Ctx, created)
		require.NoError(t, err)
		assert.False(t, info.Running)

		require.NoError(t, b.Runtime.Remove(b.Ctx, created))
		_, err = b.Runtime.Inspect(b.Ctx, created)
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	mutating("InspectStopped", func(t *testing.T, b InterfaceBackend) {
		name := boot(t, b, runtime.InstanceConfig{})
		require.NoError(t, b.Runtime.Stop(b.Ctx, name))
		info, err := b.Runtime.Inspect(b.Ctx, name)
		require.NoError(t, err)
		assert.False(t, info.Running)
	})

	mutating("StopIdempotent", func(t *testing.T, b InterfaceBackend) {
		name := boot(t, b, runtime.InstanceConfig{})
		require.NoError(t, b.Runtime.Stop(b.Ctx, name))
		assert.NoError(t, b.Runtime.Stop(b.Ctx, name), "second Stop is a no-op")
	})

	mutating("RemoveIdempotent", func(t *testing.T, b InterfaceBackend) {
		gate.acquire()
		t.Cleanup(gate.release)
		name := b.NewSleeper(t, runtime.InstanceConfig{Name: conformanceInstanceName(t)})
		require.NoError(t, b.Runtime.Remove(b.Ctx, name))
		assert.NoError(t, b.Runtime.Remove(b.Ctx, name), "second Remove is a no-op")
	})

	// ExecOnStopped is the DF18 "exec into a stopped instance" error path: a
	// created-then-stopped instance must reject exec rather than hang or panic.
	mutating("ExecOnStopped", func(t *testing.T, b InterfaceBackend) {
		name := boot(t, b, runtime.InstanceConfig{})
		require.NoError(t, b.Runtime.Stop(b.Ctx, name))
		_, err := b.Runtime.Exec(b.Ctx, name, []string{"echo", "hello"}, "")
		assert.Error(t, err, "exec into a stopped instance must error")
	})

	// --- Read-only exec (shared one instance, or one-per-subtest in parallel) ---

	readOnly := []struct {
		name string
		run  func(t *testing.T, b InterfaceBackend, name string)
	}{
		{"InspectRunning", func(t *testing.T, b InterfaceBackend, name string) {
			info, err := b.Runtime.Inspect(b.Ctx, name)
			require.NoError(t, err)
			assert.True(t, info.Running)
		}},
		{"ExecSimple", func(t *testing.T, b InterfaceBackend, name string) {
			res, err := b.Runtime.Exec(b.Ctx, name, []string{"echo", "hello"}, "")
			require.NoError(t, err)
			assert.Equal(t, "hello", res.Stdout)
			assert.Equal(t, 0, res.ExitCode)
		}},
		{"ExecNonZeroExit", func(t *testing.T, b InterfaceBackend, name string) {
			res, err := b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "exit 42"}, "")
			assert.Error(t, err)
			assert.Equal(t, 42, res.ExitCode)
		}},
		{"InteractiveExecZeroExit", func(t *testing.T, b InterfaceBackend, name string) {
			var out strings.Builder
			err := b.Runtime.InteractiveExec(b.Ctx, name, []string{"true"}, "", "",
				runtime.IOStreams{Out: &out, TTY: true})
			assert.NoError(t, err, "exit 0 stays nil")
		}},
		{"InteractiveExecNonZeroExit", func(t *testing.T, b InterfaceBackend, name string) {
			var out strings.Builder
			err := b.Runtime.InteractiveExec(b.Ctx, name, []string{"sh", "-c", "exit 9"}, "", "",
				runtime.IOStreams{Out: &out, TTY: true})
			var execErr *runtime.ExecError
			require.ErrorAs(t, err, &execErr, "TTY exec non-zero exit must surface as *runtime.ExecError")
			assert.Equal(t, 9, execErr.ExitCode)
		}},
	}

	// runStdio runs the stdio section against an instance obtained from `instance`
	// — the shared one when sharing, a fresh boot per sub-subtest otherwise. Gated
	// on SkipStdio / the StdioExecer capability, scoped to its own subtest so a
	// Skip does not abort the surrounding group.
	runStdio := func(t *testing.T, b InterfaceBackend, instance func(t *testing.T) string) {
		if b.SkipStdio != "" {
			t.Skip(b.SkipStdio)
		}
		execer, ok := b.Runtime.(runtime.StdioExecer)
		if !ok {
			t.Skip("backend does not implement runtime.StdioExecer")
		}
		t.Run("PipesOutput", func(t *testing.T) {
			name := instance(t)
			var stdout, stderr strings.Builder
			err := execer.StdioExec(b.Ctx, name, []string{"echo", "hello"}, nil, &stdout, &stderr)
			require.NoError(t, err)
			assert.Equal(t, "hello", strings.TrimSpace(stdout.String()))
		})
		t.Run("NonZeroExit", func(t *testing.T) {
			name := instance(t)
			err := execer.StdioExec(b.Ctx, name, []string{"sh", "-c", "exit 7"}, nil, nil, nil)
			var execErr *runtime.ExecError
			require.ErrorAs(t, err, &execErr, "non-zero exit must surface as *runtime.ExecError")
			assert.Equal(t, 7, execErr.ExitCode)
		})
	}

	if shares {
		// One shared running instance for every read-only subtest; they run
		// serially against it and never mutate it, so the boot cost is paid once.
		t.Run("ReadOnly", func(t *testing.T) {
			b := probe
			shared := boot(t, b, runtime.InstanceConfig{})
			for _, c := range readOnly {
				t.Run(c.name, func(t *testing.T) { c.run(t, b, shared) })
			}
			t.Run("Stdio", func(t *testing.T) {
				runStdio(t, b, func(*testing.T) string { return shared })
			})
		})
	} else {
		for _, c := range readOnly {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				b := setup(t)
				c.run(t, b, boot(t, b, runtime.InstanceConfig{}))
			})
		}
		t.Run("Stdio", func(t *testing.T) {
			t.Parallel()
			b := setup(t)
			runStdio(t, b, func(t *testing.T) string { return boot(t, b, runtime.InstanceConfig{}) })
		})
	}

	// --- Bind-mount section (gated: SkipMounts; each mount case its own instance) ---

	t.Run("Mounts", func(t *testing.T) {
		parallelize(t)
		b := setup(t)
		if b.SkipMounts != "" {
			t.Skip(b.SkipMounts)
		}

		t.Run("ReadWrite", func(t *testing.T) {
			hostDir := t.TempDir()
			name := boot(t, b, runtime.InstanceConfig{
				Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: "/mnt/test", ReadOnly: false}},
			})
			_, err := b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "echo hello > /mnt/test/output.txt"}, "")
			require.NoError(t, err)
			content, err := os.ReadFile(filepath.Join(hostDir, "output.txt")) //nolint:gosec // G304: test suite writes under t.TempDir(); no sudo chown concern
			require.NoError(t, err)
			assert.Contains(t, string(content), "hello")
		})

		t.Run("ReadOnly", func(t *testing.T) {
			hostDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(hostDir, "readonly.txt"), []byte("original"), 0o600)) //nolint:forbidigo // test suite writes under t.TempDir(); no sudo chown concern
			name := boot(t, b, runtime.InstanceConfig{
				Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: "/mnt/test", ReadOnly: true}},
			})
			res, err := b.Runtime.Exec(b.Ctx, name, []string{"cat", "/mnt/test/readonly.txt"}, "")
			require.NoError(t, err)
			assert.Equal(t, "original", res.Stdout)

			_, err = b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "echo modified > /mnt/test/readonly.txt"}, "")
			assert.Error(t, err, "write to a read-only mount must fail")
		})
	})
}

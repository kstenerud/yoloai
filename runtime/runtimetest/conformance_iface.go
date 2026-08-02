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

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
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

	// SandboxTiers supplies the two paths the sandbox-tier section needs for a
	// running instance: the sandbox directory as the host sees it, and the flat
	// view as the guest sees it. Nil → the section skips, and SkipSandboxTiers
	// must say why.
	//
	// It is a fixture field rather than a runtime.Backend method because only
	// the two backends that hand a guest a whole directory can answer it, and
	// adding an interface method for them would put a test's question into the
	// production contract of four backends that have no answer to give.
	SandboxTiers func(name string) (hostDir, guestView string)

	// SkipSandboxTiers names why a backend does not run the tier section. The
	// honest reason for the four bind-per-file backends is that they never share
	// the sandbox directory at all: they bind each needed file individually, so
	// the tiers are not reachable from the guest by any path and the section
	// would assert nothing. Declaring that is the point — the whole workstream
	// exists because tart and seatbelt are the two that share a directory, and a
	// suite that reported six greens here would be hiding that difference rather
	// than certifying it.
	SkipSandboxTiers string

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

// mountTargetBase is where the mount section asks for its bind, with a per-subtest
// suffix appended.
//
// Under /tmp rather than /mnt, and the choice is load-bearing rather than
// cosmetic: /mnt is absent and uncreatable on a macOS guest (SIP-sealed root
// volume) and not writable on a macOS host without root, so it excluded the two
// backends whose mount semantics differ most from the container norm — the exact
// population the suite exists to compare. /tmp is writable on a macOS host,
// present in a macOS guest, and present in every container image. The section is
// not about where a mount lands, so it has no reason to insist on a path only
// Linux containers can honour (DF161).
const mountTargetBase = "/tmp/yoloai-conformance-mnt"

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

// InterfaceSetupFunc connects to a backend and returns the fixture with cleanup
// already registered (e.g. rt.Close via t.Cleanup). It is called once per suite
// on the non-parallel parent t — not per subtest — so a setup that uses t.Setenv
// (IsolatedHome) is compatible with the parallel subtests that reuse the fixture.
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
// lifecycle subtests share the suite's one fixture but each mutating subtest
// boots its own uniquely-named instance — preserving per-instance isolation.
// The read-only exec subtests are the amortisation target: a SharesReadOnlyInstance backend
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

	// setup is called exactly once, here on the non-parallel parent, and every
	// subtest reuses this fixture. It cannot be called per-subtest: backends
	// isolate via IsolatedHome, which calls t.Setenv, and Go forbids t.Setenv in a
	// t.Parallel() test (the env is process-global — a per-subtest Setenv under
	// parallelism never isolated anything, it just races). Sharing one fixture is
	// safe because each subtest still boots its OWN uniquely-named instance; only
	// the backend's Runtime (a concurrency-safe client/connection) is shared.
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
		b := probe
		assert.True(t, runtime.GitRunsInConfinement(b.Runtime),
			"every backend must confine work-copy git (SandboxSide filesystem or GitExecInConfinement); the unconfined fallback was removed in DF119")
		_, isGitExecer := b.Runtime.(runtime.GitExecer)
		assert.True(t, isGitExecer, "a confining backend must implement runtime.GitExecer (git runs in the sandbox)")
	})

	// A SandboxSide backend additionally keeps its work copy inside the sandbox,
	// so baseline creation is deferred to the sandbox (WorkDirSetup). The
	// property-based dispatch in ExecuteVMWorkDirSetup assumes this invariant.
	t.Run("SandboxSideImplementsLocalityOps", func(t *testing.T) {
		b := probe
		if b.Runtime.Descriptor().Capabilities.FilesystemLocality != runtime.LocalitySandboxSide {
			t.Skip("HostSide backend: no in-sandbox locality operations required")
		}
		_, isWorkDirSetup := b.Runtime.(runtime.WorkDirSetup)
		assert.True(t, isWorkDirSetup, "SandboxSide backend must implement runtime.WorkDirSetup (baseline deferred to sandbox)")
	})

	t.Run("InspectNotFound", func(t *testing.T) {
		parallelize(t)
		b := probe
		_, err := b.Runtime.Inspect(b.Ctx, "yoloai-nonexistent-instance-xyz")
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	t.Run("IsReady", func(t *testing.T) {
		parallelize(t)
		b := probe
		ready, err := b.Runtime.IsReady(b.Ctx)
		require.NoError(t, err)
		assert.True(t, ready)
	})

	// --- Mutating lifecycle (each its own instance) ---

	mutating := func(name string, body func(t *testing.T, b InterfaceBackend)) {
		t.Run(name, func(t *testing.T) {
			parallelize(t)
			body(t, probe)
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

	// An image-building backend MUST round-trip the labels Create was given back
	// out through Inspect, because that is the only per-instance record able to
	// outlive the tag the instance was created by — it carries the image lineage
	// a stopped or finished sandbox is judged on (DF156).
	//
	// This is a conformance case rather than a unit test on purpose. The
	// resume-lineage check first shipped covered by six passing tests against a
	// fake that reported an image id AND implemented ProfileImageBuilder — a
	// combination no real backend has — which is why nobody noticed the code path
	// could not be reached. Only a real backend can satisfy this.
	//
	// Gated on ProfileImageBuilder because that is exactly the set with images to
	// have lineage: tart clones VMs and seatbelt runs host processes, and neither
	// has container labels to return.
	mutating("InstanceLabelsRoundTrip", func(t *testing.T, b InterfaceBackend) {
		if _, ok := runtime.ProfileImageBuilderOf(b.Runtime); !ok {
			t.Skip("no image concept: nothing builds images here, so no lineage to record")
		}
		const key, want = "yoloai.test.lineage", "conformance-value"
		name := boot(t, b, runtime.InstanceConfig{Labels: map[string]string{key: want}})
		info, err := b.Runtime.Inspect(b.Ctx, name)
		require.NoError(t, err)
		assert.Equal(t, want, info.Labels[key],
			"Create's labels must come back from Inspect: a lineage record the backend drops is a check that silently never fires")
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
				b := probe
				c.run(t, b, boot(t, b, runtime.InstanceConfig{}))
			})
		}
		t.Run("Stdio", func(t *testing.T) {
			t.Parallel()
			b := probe
			runStdio(t, b, func(t *testing.T) string { return boot(t, b, runtime.InstanceConfig{}) })
		})
	}

	// --- Bind-mount section (gated: SkipMounts; each mount case its own instance) ---

	t.Run("Mounts", func(t *testing.T) {
		parallelize(t)
		b := probe
		if b.SkipMounts != "" {
			t.Skip(b.SkipMounts)
		}

		// Where the guest sees a mount is the backend's answer, not the suite's:
		// tart re-roots every mount under /Users/admin/host/... So ask, through the
		// same interface production asks through (setupAuxDir does this so the
		// recorded MountPath is one that exists in the guest). Exec'ing the
		// requested container path instead would test the suite's assumption about
		// the backend rather than the backend. It is the identity for /tmp on every
		// backend today — and deliberately still routed through the call, because a
		// suite that certifies mount behaviour while bypassing the mount-path
		// interface is how this drifted in the first place (DF161).
		guestPath := func(containerPath string) string {
			return runtime.ResolveGuestMountPathFor(b.Runtime, containerPath)
		}

		// Per-subtest targets: a host-side backend materialises a mount as a
		// symlink at this literal path on the host, and seatbelt's mountSymlinks
		// skips a target that already exists — so two parallel subtests sharing one
		// path would leave the second silently unmounted, passing for the wrong
		// reason or failing for an unrelated one.
		rwTarget, roTarget := mountTargetBase+"-rw", mountTargetBase+"-ro"

		t.Run("ReadWrite", func(t *testing.T) {
			hostDir := t.TempDir()
			name := boot(t, b, runtime.InstanceConfig{
				Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: rwTarget, ReadOnly: false}},
			})
			_, err := b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "echo hello > " + guestPath(rwTarget) + "/output.txt"}, "")
			require.NoError(t, err)
			content, err := os.ReadFile(filepath.Join(hostDir, "output.txt")) //nolint:gosec // G304: test suite writes under t.TempDir(); no sudo chown concern
			require.NoError(t, err)
			assert.Contains(t, string(content), "hello")
		})

		t.Run("ReadOnly", func(t *testing.T) {
			hostDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(hostDir, "readonly.txt"), []byte("original"), 0o600)) //nolint:forbidigo // test suite writes under t.TempDir(); no sudo chown concern
			name := boot(t, b, runtime.InstanceConfig{
				Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: roTarget, ReadOnly: true}},
			})
			res, err := b.Runtime.Exec(b.Ctx, name, []string{"cat", guestPath(roTarget) + "/readonly.txt"}, "")
			require.NoError(t, err)
			assert.Equal(t, "original", res.Stdout)

			// Note for a host-side backend: hostDir is under the per-user temp tree,
			// which seatbelt's profile grants read+write wholesale. This assertion
			// therefore only holds because a read-only mount now emits an explicit
			// deny; it was the failure that exposed that it did not (DF161).
			_, err = b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "echo modified > " + guestPath(roTarget) + "/readonly.txt"}, "")
			assert.Error(t, err, "write to a read-only mount must fail")
		})
	})

	// --- Sandbox-tier section (gated: SandboxTiers; one instance, one invariant) ---

	t.Run("SandboxTiers", func(t *testing.T) {
		parallelize(t)
		assertSandboxTiers(t, probe, boot)
	})
}

// assertSandboxTiers is the sandbox-tier section's body, split out of the suite
// so the suite itself stays under the complexity budget.
//
// The tier invariant, asserted from inside the guest: host/ is unreachable,
// ro/ is readable and not writable, rw/ is writable. It is one instance and
// one subtest because it is one property — a guest that can reach host/ is
// DF136 whichever of these steps notices.
//
// Every path is spelled relative to the guest's flat view, which is what
// makes one assertion serve two backends that reach the invariant by
// different mechanisms: <view>/../host is a directory that does not exist in
// a tart guest (no --dir names it) and a denied path on seatbelt, and the
// guest cannot tell the difference — which is the point.
func assertSandboxTiers(t *testing.T, b InterfaceBackend, boot func(*testing.T, InterfaceBackend, runtime.InstanceConfig) string) {
	t.Helper()
	if b.SandboxTiers == nil {
		require.NotEmpty(t, b.SkipSandboxTiers,
			"a backend that does not run the tier section must say why")
		t.Skip(b.SkipSandboxTiers)
	}

	name := boot(t, b, runtime.InstanceConfig{})
	hostDir, view := b.SandboxTiers(name)

	// Plant a host-tier record and a read-only-tier file, then surface the
	// read-only tier in the view the way a launch does.
	record := filepath.Join(config.HostTierDir(hostDir), config.EnvironmentFileName)
	require.NoError(t, fileutil.MkdirAll(filepath.Dir(record), 0o750))
	require.NoError(t, os.WriteFile(record, []byte(`{"HostPath":"/real"}`), 0o600)) //nolint:forbidigo // the suite's own sandbox dir
	probeFile := filepath.Join(config.ReadOnlyTierDir(hostDir), "tier-probe.txt")
	require.NoError(t, fileutil.MkdirAll(filepath.Dir(probeFile), 0o750))
	require.NoError(t, os.WriteFile(probeFile, []byte("original"), 0o600)) //nolint:forbidigo // the suite's own sandbox dir
	require.NoError(t, config.AssembleGuestView(hostDir))

	// Paths are single-quoted into every shell command: a tart guest sees the
	// tiers under "/Volumes/My Shared Files", and an unquoted path with a
	// space fails as a *parse* error that assert.Error accepts happily — the
	// denial assertions below would then pass without anything being denied.
	// The paths are backend-supplied and contain no quotes, so single-quoting
	// is sufficient (the same argument tart's hostnameSetCommand makes).
	write := func(path, content string) error {
		_, err := b.Runtime.Exec(b.Ctx, name,
			[]string{"sh", "-c", "echo " + content + " > '" + path + "'"}, "")
		return err
	}
	// Reads go through argv with no shell at all, so there is nothing to quote.
	read := func(path string) (runtime.ExecResult, error) {
		return b.Runtime.Exec(b.Ctx, name, []string{"cat", path}, "")
	}

	// host/ — neither readable nor writable. The write is DF136's primitive:
	// rewriting environment.json's HostPath redirects a host-side apply to
	// any path the user can write.
	hostRecord := view + "/../" + config.HostTierName + "/" + config.EnvironmentFileName
	_, err := read(hostRecord)
	assert.Error(t, err, "the guest must not be able to read a host-tier record (DF136)")
	assert.Error(t, write(hostRecord, "tampered"),
		"the guest must not be able to write a host-tier record (DF136)")
	onDisk, err := os.ReadFile(record) //nolint:gosec // G304: the suite's own sandbox dir
	require.NoError(t, err)
	assert.Equal(t, `{"HostPath":"/real"}`, string(onDisk), "the record must be byte-identical after the denied write")

	// ro/ — readable through the flat view, and not writable through it.
	// Reading it flat is half the invariant: a tier the guest cannot reach
	// is not read-only, it is broken, and the guest scripts join every path
	// from this one root.
	res, err := read(view + "/tier-probe.txt")
	require.NoError(t, err, "the read-only tier must be readable at the flat view path")
	assert.Contains(t, res.Stdout, "original")
	assert.Error(t, write(view+"/tier-probe.txt", "tampered"),
		"the guest must not be able to write the read-only tier through the view (DF148)")
	after, err := os.ReadFile(probeFile) //nolint:gosec // G304: the suite's own sandbox dir
	require.NoError(t, err)
	assert.Equal(t, "original", string(after), "the read-only tier's contents must be unchanged")

	// rw/ — writable, and landing on the host where the host expects it.
	// Without this the section would pass on a backend that shared nothing,
	// and — the case that actually happened — on a shell that could not
	// parse any of the paths above.
	require.NoError(t, write(view+"/tier-canary.txt", "ok"),
		"the read-write tier must be writable from the guest")
	canary, err := os.ReadFile(filepath.Join(config.ReadWriteTierDir(hostDir), "tier-canary.txt"))
	require.NoError(t, err, "a guest write to the view must land in the read-write tier on the host")
	assert.Contains(t, string(canary), "ok")
}

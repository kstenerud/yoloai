//go:build integration

// ABOUTME: Verifies the SharesReadOnlyInstance lever of the conformance harness
// ABOUTME: on Linux with an in-memory fake backend: the read-only subtests boot
// ABOUTME: one shared instance instead of one each. The real VM backends prove
// ABOUTME: it end to end on the mac; this pins the boot-count contract cheaply.

package runtimetest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// fakeConfBackend is a minimal in-memory runtime.Backend that satisfies exactly
// the behaviour the conformance suite asserts (echo, exit codes, lifecycle,
// not-found), counting Create calls so a test can prove how many instances a run
// booted. It skips the mount and stdio sections. HostSide + GitExecInConfinement
// makes it pass BackendConfinesWorkCopyGit and skip the SandboxSide locality
// check.
type fakeConfBackend struct {
	shares  bool
	mu      sync.Mutex
	running map[string]bool
	creates int
	names   []string
}

var (
	_ runtime.Backend   = (*fakeConfBackend)(nil)
	_ runtime.GitExecer = (*fakeConfBackend)(nil)
)

func newFakeConfBackend(shares bool) *fakeConfBackend {
	return &fakeConfBackend{shares: shares, running: map[string]bool{}}
}

func (f *fakeConfBackend) fixture(ctx context.Context) InterfaceBackend {
	return InterfaceBackend{
		Runtime:                f,
		Ctx:                    ctx,
		SharesReadOnlyInstance: f.shares,
		SkipMounts:             "fake backend has no filesystem to mount",
		SkipStdio:              "fake backend does not exercise stdio",
		NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
			require.NoError(t, f.Create(ctx, cfg))
			t.Cleanup(func() { _ = f.Remove(ctx, cfg.Name) })
			return cfg.Name
		},
	}
}

func (f *fakeConfBackend) Create(_ context.Context, cfg runtime.InstanceConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running[cfg.Name] = false
	f.creates++
	f.names = append(f.names, cfg.Name)
	return nil
}

func (f *fakeConfBackend) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running[name] = true
	return nil
}

func (f *fakeConfBackend) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.running[name]; ok {
		f.running[name] = false
	}
	return nil
}

func (f *fakeConfBackend) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, name)
	return nil
}

func (f *fakeConfBackend) Inspect(_ context.Context, name string) (runtime.InstanceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	running, ok := f.running[name]
	if !ok {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}
	return runtime.InstanceInfo{Running: running}, nil
}

// Exec emulates just enough of a shell: `echo X` prints X, `sh -c "exit N"`
// exits N (a non-zero exit returns an *ExecError, matching real backends), and
// exec against a stopped instance errors (the DF18 path).
func (f *fakeConfBackend) Exec(_ context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	f.mu.Lock()
	running := f.running[name]
	f.mu.Unlock()
	if !running {
		return runtime.ExecResult{}, fmt.Errorf("exec into %q: %w", name, runtime.ErrNotRunning)
	}
	if code, ok := parseExit(cmd); ok {
		if code != 0 {
			return runtime.ExecResult{ExitCode: code}, &runtime.ExecError{ExitCode: code}
		}
		return runtime.ExecResult{ExitCode: 0}, nil
	}
	if len(cmd) >= 1 && cmd[0] == "echo" {
		return runtime.ExecResult{Stdout: strings.Join(cmd[1:], " "), ExitCode: 0}, nil
	}
	return runtime.ExecResult{ExitCode: 0}, nil
}

func (f *fakeConfBackend) InteractiveExec(_ context.Context, name string, cmd []string, _, _ string, _ runtime.IOStreams) error {
	f.mu.Lock()
	running := f.running[name]
	f.mu.Unlock()
	if !running {
		return fmt.Errorf("interactive exec into %q: %w", name, runtime.ErrNotRunning)
	}
	if code, ok := parseExit(cmd); ok && code != 0 {
		return &runtime.ExecError{ExitCode: code}
	}
	return nil
}

// parseExit recognises `sh -c "exit N"` and returns N.
func parseExit(cmd []string) (int, bool) {
	if len(cmd) == 3 && cmd[0] == "sh" && cmd[1] == "-c" {
		fields := strings.Fields(cmd[2])
		if len(fields) == 2 && fields[0] == "exit" {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func (f *fakeConfBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type: "fakeconf",
		Capabilities: runtime.BackendCaps{
			// Zero-value FilesystemLocality is HostSide, so the SandboxSide
			// locality check skips; GitExecInConfinement makes the confinement
			// invariant pass without a real sandbox filesystem.
			GitExecInConfinement: true,
		},
	}
}

func (f *fakeConfBackend) GitExec(_ context.Context, _, _, _ string, _ ...string) (string, error) {
	return "", nil
}

func (f *fakeConfBackend) IsReady(_ context.Context) (bool, error) { return true, nil }
func (f *fakeConfBackend) Setup(_ context.Context, _ config.Layout, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	return nil
}
func (f *fakeConfBackend) Close() error { return nil }
func (f *fakeConfBackend) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, nil
}
func (f *fakeConfBackend) Logs(_ context.Context, _ string, _ int) string { return "" }
func (f *fakeConfBackend) DiagHint(string) string                         { return "" }
func (f *fakeConfBackend) TmuxSocket(string) string                       { return "" }
func (f *fakeConfBackend) AttachCommand(string, int, int, runtime.IsolationMode) []string {
	return nil
}

// TestConformanceHarness_SharesOneInstanceAcrossReadSubtests verifies the
// SharesReadOnlyInstance lever on Linux: the read-only subtests must collapse to
// ONE shared instance while a non-sharing run boots one apiece. Both fakes pass
// the identical assertion table (a run that fails any assertion fails this test),
// so this proves the shared path is behaviourally correct, not merely cheaper.
func TestConformanceHarness_SharesOneInstanceAcrossReadSubtests(t *testing.T) {
	ctx := context.Background()

	// Each nested run completes fully — including a non-sharing run's parallel
	// subtests — before its t.Run returns, so the create counts are final here.
	sharing := newFakeConfBackend(true)
	t.Run("sharing", func(t *testing.T) {
		RunInterfaceConformance(t, func(*testing.T) InterfaceBackend { return sharing.fixture(ctx) })
	})

	isolated := newFakeConfBackend(false)
	t.Run("isolated", func(t *testing.T) {
		RunInterfaceConformance(t, func(*testing.T) InterfaceBackend { return isolated.fixture(ctx) })
	})

	assert.Less(t, sharing.creates, isolated.creates,
		"sharing must boot strictly fewer instances than the one-per-subtest path")

	// The only difference between the two runs is the read-only group: sharing
	// serves all of it from one instance, isolated boots one per subtest. So the
	// gap equals (read-only subtests − 1). If a read-only subtest is added, this
	// number moves with it — that is the point of pinning it.
	const readOnlyBootsSaved = 4 // 5 read-only subtests → 1 shared instance
	assert.Equal(t, readOnlyBootsSaved, isolated.creates-sharing.creates,
		"the read-only subtests must all share one instance")

	assert.Equal(t, 1, countContaining(sharing.names, "ReadOnly"),
		"exactly one instance is booted under the shared ReadOnly subtree")
}

func countContaining(names []string, sub string) int {
	n := 0
	for _, s := range names {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

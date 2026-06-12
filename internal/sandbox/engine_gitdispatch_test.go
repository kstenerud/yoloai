package sandbox

// ABOUTME: Regression guard — every work-copy git Engine method must dispatch git
// ABOUTME: through the runtime (sandbox scope), never silently fall back to host git.
//
// Why this test exists: a work-copy git op that runs *host* git stays green on
// unit tests and on bind-mount backends (docker/podman/seatbelt), because for
// them the work copy IS on the host. Only a GitExecer backend (Tart, which runs
// git inside the VM) makes "dispatched vs host-git" observable — and there is no
// real-Tart run coverage in `make check` (DF18). Two real bugs hid here: a
// method using git.NewHost on the work copy (wrong scope), and a method that
// threaded the runtime but never opened it (missing e.TryEnsure, so e.runtime
// stayed nil and git.NewSandbox(nil,…) fell back to host). This test reproduces
// Tart's defining trait — git dispatched to the runtime — with a fake GitExecer
// backend, so both failure modes fail `make check` on any machine, no VM needed.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// gitDispatchRec records the git invocations a GitExecer backend receives.
type gitDispatchRec struct {
	mu      sync.Mutex
	calls   [][]string
	respond func(args []string) (string, error) // canned output; nil → "" success
}

func (r *gitDispatchRec) record(args []string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), args...))
	respond := r.respond
	r.mu.Unlock()
	if respond != nil {
		return respond(args)
	}
	return "", nil
}

func (r *gitDispatchRec) saw(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}
	return false
}

func (r *gitDispatchRec) dump() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, c := range r.calls {
		fmt.Fprintf(&b, "\n  git %s", strings.Join(c, " "))
	}
	if b.Len() == 0 {
		return " (no git dispatched to the runtime)"
	}
	return b.String()
}

// gitDispatchHook is consulted by the registered backend's GitExec. It is set
// per-subtest; the table runs single-threaded, mirroring the lazyOpenmock
// pattern in engine_test.go (the registry is process-wide).
var gitDispatchHook *gitDispatchRec

// gitDispatchMock is a GitExecer backend (like Tart) that records git
// invocations instead of running them, so we can assert work-copy git was
// dispatched here rather than to host git.
type gitDispatchMock struct{ mockRuntime }

func (m *gitDispatchMock) GitExec(_ context.Context, _ string, _ string, args ...string) (string, error) {
	if gitDispatchHook == nil {
		return "", errMockNotImplemented
	}
	return gitDispatchHook.record(args)
}

var gitDispatchOnce sync.Once

func registerGitDispatchMock(t *testing.T) {
	t.Helper()
	gitDispatchOnce.Do(func() {
		runtime.Register(
			func(context.Context, config.Layout) (runtime.Runtime, error) {
				return &gitDispatchMock{}, nil
			},
			runtime.BackendDescriptor{Type: "gitdispatchmock"},
		)
	})
}

// seedCopySandbox writes a minimal :copy sandbox with a baseline so
// loadDiffContext succeeds and the work-copy methods reach their git calls.
func seedCopySandbox(t *testing.T, layout config.Layout, name string) {
	t.Helper()
	sandboxDir := layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "work"), 0750))
	meta := &store.Environment{
		Name:        name,
		AgentType:   "claude",
		BackendType: "gitdispatchmock",
		CreatedAt:   time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    "/tmp/project",
			MountPath:   "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "abc1230000000000000000000000000000000000",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
}

// TestEngine_WorkCopyGit_DispatchesToRuntime asserts every work-copy git Engine
// method routes git through the runtime (GitExecer) rather than host git. A
// GitExecer backend is registered and the Engine is built LAZILY (NewEngine,
// not NewEngineWithRuntime) — that is essential: a forgotten e.TryEnsure leaves
// e.runtime nil and git.NewSandbox(nil,…) silently falls back to host, which
// only the lazy path exposes. If a method falls back to host git, the fake
// records nothing for that op and the assertion fails.
func TestEngine_WorkCopyGit_DispatchesToRuntime(t *testing.T) {
	registerGitDispatchMock(t)
	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))
	const name = "dispatchbox"
	seedCopySandbox(t, layout, name)
	e := NewEngine("gitdispatchmock", slog.Default(), strings.NewReader(""), WithLayout(layout))
	t.Cleanup(func() { _ = e.Close() })
	ctx := context.Background()

	// Makes `git log <baseline>..HEAD` return one commit so ListCommitsWithStats
	// reaches its per-commit `diff --stat` loop (the loop that had the bug).
	oneCommitLog := func(args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "log") {
			return "def4560000000000000000000000000000000000 a change\n", nil
		}
		return "", nil
	}

	cases := []struct {
		name    string
		respond func(args []string) (string, error)
		call    func() error
		// want is a substring that MUST appear in a git command dispatched to the
		// runtime. "" means "any git dispatch" (the method's first git is its own
		// work-copy read, so any dispatch proves it didn't fall back to host).
		want string
	}{
		{"GenerateCommitDiff", nil, func() error {
			_, err := e.GenerateCommitDiff(ctx, name, "deadbee", false)
			return err
		}, "deadbee"}, // the per-commit diff must run against the work copy in-runtime
		{"ListCommitsWithStats", oneCommitLog, func() error {
			_, err := e.ListCommitsWithStats(ctx, name)
			return err
		}, "--stat"}, // the stat loop (the original bug) must dispatch, not just the find-side log
		{"ListCommits", nil, func() error {
			_, err := e.ListCommits(ctx, name)
			return err
		}, "log"},
		{"HasUncommittedChanges", nil, func() error {
			_, err := e.HasUncommittedChanges(ctx, name)
			return err
		}, ""},
		{"BaselineLog", nil, func() error {
			_, err := e.BaselineLog(ctx, name)
			return err
		}, ""},
		{"WorkdirTags", nil, func() error {
			_, err := e.WorkdirTags(ctx, name, false)
			return err
		}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &gitDispatchRec{respond: tc.respond}
			gitDispatchHook = rec
			defer func() { gitDispatchHook = nil }()

			_ = tc.call() // method may error on the fake's canned output; we assert dispatch, not success

			if tc.want == "" {
				require.NotEmpty(t, rec.calls,
					"%s dispatched no git to the runtime — it fell back to host git "+
						"(wrong scope via git.NewHost, or e.runtime nil from a missing TryEnsure)", tc.name)
				return
			}
			require.True(t, rec.saw(tc.want),
				"%s did not dispatch a git command containing %q to the runtime "+
					"(host-git fallback? wrong scope or missing TryEnsure). dispatched:%s",
				tc.name, tc.want, rec.dump())
		})
	}
}

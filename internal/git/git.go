// ABOUTME: Package git provides a unified git executor for host and sandbox scopes.
// ABOUTME: The execer interface abstracts how a single git invocation runs; Git
// ABOUTME: exposes all git operations as methods, threaded with ctx.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// execer runs one git invocation in workDir for a scope.
// stdin is nil for commands that produce no input; non-nil for `git apply`/`git am`.
// Returns stdout on success; on non-zero exit returns ("", *runtime.ExecError);
// on other failure returns ("", wrapped error).
type execer interface {
	run(ctx context.Context, workDir string, stdin []byte, args ...string) (stdout string, err error)
}

// Git is the unified git executor. Construct it with NewHost, NewTestHostWithEnv
// (tests), or NewSandbox. All operations accept a context as their first argument.
type Git struct {
	e       execer
	tempDir string // yoloai-owned temp root (layout.TempDir()); empty → os.TempDir() fallback
}

// NewHost returns a host-scoped Git whose subprocess env is derived from layout
// (PATH/HOME/TMPDIR/SUDO_UID — see sysexec.GitEnv).
func NewHost(layout config.Layout) *Git {
	return &Git{e: hostExec{env: layout.Env().EnvForGitInvocation()}, tempDir: layout.TempDir()}
}

// NewTestHostWithEnv returns a host-scoped Git with an explicit, already-curated env.
// TEST-ONLY: production code uses NewHost.
func NewTestHostWithEnv(env []string) *Git {
	return &Git{e: hostExec{env: env}}
}

// NewSandbox returns a sandbox-scoped Git for a sandbox's copy-mode work copy,
// with the executor *injected* by runtime.GitRunsInConfinement — decided once,
// here. When git runs in the sandbox's confinement (Tart's VM, or a container
// backend, so an agent-planted .git driver can't execute on the host — audit
// C1), it dispatches through the backend's GitExecer; otherwise (seatbelt, nil
// runtime) it runs host git. Call sites and the executors never branch on
// locality again. The host-TARGET apply ops (writing the user's real repo) use
// NewHost, not this.
func NewSandbox(layout config.Layout, rt runtime.Backend, name string) *Git {
	env := layout.Env().EnvForGitInvocation()
	if runtime.GitRunsInConfinement(rt) {
		return &Git{e: sandboxExec{env: env, rt: rt, layout: layout, name: name}, tempDir: layout.TempDir()}
	}
	return &Git{e: hostExec{env: env}, tempDir: layout.TempDir()}
}

// Run executes a git command in workDir and returns stdout.
func (g *Git) Run(ctx context.Context, workDir string, args ...string) (string, error) {
	return g.e.run(ctx, workDir, nil, args...)
}

// RunInput executes a git command in workDir, feeding stdin as the command's
// standard input. Used by git apply / git am.
func (g *Git) RunInput(ctx context.Context, workDir string, stdin []byte, args ...string) (string, error) {
	return g.e.run(ctx, workDir, stdin, args...)
}

// RunCmd executes a git command in workDir and returns an error on failure.
// The error message includes the git subcommand name and stderr for easy diagnosis.
func (g *Git) RunCmd(ctx context.Context, dir string, args ...string) error {
	_, err := g.Run(ctx, dir, args...)
	if err != nil {
		var ee *runtime.ExecError
		if errors.As(err, &ee) {
			if ee.Stderr != "" {
				return fmt.Errorf("git %s: %s: %w", args[0], ee.Stderr, err)
			}
			return fmt.Errorf("git %s: %w", args[0], err)
		}
		return err
	}
	return nil
}

// ─── execer implementations ──────────────────────────────────────────────────

// hostExec runs git on the host filesystem with a curated env.
type hostExec struct{ env []string }

func (h hostExec) run(ctx context.Context, workDir string, stdin []byte, args ...string) (string, error) {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", workDir}, args...)
	cmd := sysexec.CommandContext(ctx, h.env, "git", fullArgs...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdoutBuf.String(), &runtime.ExecError{
				ExitCode: exitErr.ExitCode(),
				Stderr:   strings.TrimSpace(stderrBuf.String()),
			}
		}
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return stdoutBuf.String(), nil
}

// sandboxExec is the in-confinement strategy: it dispatches git through the
// backend (GitExecer) so it runs against the work copy INSIDE the sandbox, where
// an agent-controlled .git/config filter/diff/fsmonitor driver can only execute
// in the agent's own confinement, never on the host (audit C1). It is
// constructed only for GitRunsInConfinement backends (see NewSandbox), so it
// never re-checks locality.
//
// The work-copy git ops are all stdin-free (add/diff/status/log/rev-parse/
// format-patch); a stdin-bearing op (apply/am) targets the user's REAL repo via
// NewHost, never this executor, so the host fallback below is defensive only.
type sandboxExec struct {
	env    []string
	rt     runtime.Backend
	layout config.Layout
	name   string
}

func (s sandboxExec) run(ctx context.Context, workDir string, stdin []byte, args ...string) (string, error) {
	if stdin != nil {
		return (hostExec{env: s.env}).run(ctx, workDir, stdin, args...)
	}
	ge, ok := s.rt.(runtime.GitExecer)
	if !ok {
		return "", fmt.Errorf("yoloai bug: backend %s runs git in confinement but does not implement GitExecer", s.rt.Descriptor().Type)
	}

	// The work copy lives at workDir on the host but is bind-mounted into the
	// sandbox at a (possibly different) path; in-confinement git must run against
	// that in-sandbox path. Resolve the instance, the agent's container user, and
	// the in-sandbox path from the sandbox record.
	sandboxDir := s.layout.SandboxDir(s.name)
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox %q for in-confinement git: %w", s.name, err)
	}
	instance := store.InstanceName(meta.Principal, s.name)
	user := store.ContainerUser(meta, s.layout.HostUID)
	return ge.GitExec(ctx, instance, user, confinementWorkPath(meta, sandboxDir, workDir), args...)
}

// confinementWorkPath maps a host work-copy path (store.WorkDir) to the path the
// same files are reachable at inside the sandbox — the dir's resolved mount path
// (container target for docker/podman/containerd; VM-local path for Tart). When
// workDir matches no tracked dir it is returned unchanged: a backend whose
// GitExecer re-translates host paths (Tart) still copes, and it surfaces a clear
// in-sandbox error otherwise rather than silently running on the host.
func confinementWorkPath(meta *store.Environment, sandboxDir, workDir string) string {
	for i := range meta.Dirs {
		d := &meta.Dirs[i]
		if store.WorkDir(sandboxDir, d.HostPath) == workDir {
			if d.MountPath != "" {
				return d.MountPath
			}
			return d.HostPath
		}
	}
	return workDir
}

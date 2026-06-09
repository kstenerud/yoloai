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
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// execer runs one git invocation in workDir for a scope.
// stdin is nil for commands that produce no input; non-nil for `git apply`/`git am`.
// Returns stdout on success; on non-zero exit returns ("", *runtime.ExecError);
// on other failure returns ("", wrapped error).
type execer interface {
	run(ctx context.Context, workDir string, stdin []byte, args ...string) (stdout string, err error)
}

// Git is the unified git executor. Construct it with NewHost, NewHostWithEnv,
// or NewSandbox. All operations accept a context as their first argument.
type Git struct{ e execer }

// NewHost returns a host-scoped Git whose subprocess env is derived from layout
// (PATH/HOME/TMPDIR/SUDO_UID — see sysexec.GitEnv).
func NewHost(layout config.Layout) *Git {
	return &Git{hostExec{env: sysexec.GitEnv(layout.Env)}}
}

// NewHostWithEnv returns a host-scoped Git with an explicit, already-curated env.
// Use in tests (testutil.GitEnv) and transitional workspace wrappers.
// Prefer NewHost in production code.
func NewHostWithEnv(env []string) *Git {
	return &Git{hostExec{env: env}}
}

// NewSandbox returns a sandbox-scoped Git. If rt implements runtime.GitExecer
// (e.g. Tart, which runs git in-VM), invocations dispatch there; otherwise they
// fall back to host git using sysexec.GitEnv(layout.Env).
func NewSandbox(layout config.Layout, rt runtime.Runtime, name string) *Git {
	return &Git{sandboxExec{
		env:  sysexec.GitEnv(layout.Env),
		rt:   rt,
		name: name,
	}}
}

// ─── low-level ───────────────────────────────────────────────────────────────

// Cmd builds a raw *exec.Cmd for git in dir with hooks disabled. Use for the
// few call sites in workspace/tags.go that wire stdin/stdout themselves; prefer
// Run/RunCmd or the higher-level ops in all new code.
func (g *Git) Cmd(dir string, args ...string) *exec.Cmd {
	// Only the host execer exposes a raw Cmd; sandbox scope doesn't support it.
	// Unwrap to hostExec if possible, otherwise panic to surface a misuse.
	if h, ok := g.e.(hostExec); ok {
		fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
		return sysexec.Command(h.env, "git", fullArgs...)
	}
	panic("git.Git.Cmd is only supported for host-scoped executors")
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

// sandboxExec dispatches to GitExecer (e.g. Tart) when available, otherwise
// falls back to hostExec. Sandbox git ops do not pass stdin (apply/am are
// host-only operations in the current code paths).
type sandboxExec struct {
	env  []string
	rt   runtime.Runtime
	name string
}

func (s sandboxExec) run(ctx context.Context, workDir string, stdin []byte, args ...string) (string, error) {
	if ge, ok := s.rt.(runtime.GitExecer); ok {
		// GitExecer has no stdin parameter; sandbox ops are currently stdin-free.
		// If a future caller passes stdin here, fall through to the host path.
		if stdin == nil {
			return ge.GitExec(ctx, s.name, workDir, args...)
		}
	}
	return (hostExec{env: s.env}).run(ctx, workDir, stdin, args...)
}

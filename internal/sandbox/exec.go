// ABOUTME: Engine-level exec verbs — interactive (PTY) and stdio-piped command
// ABOUTME: execution inside a sandbox, plus the raw container-log tail.

package sandbox

import (
	"context"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// InteractiveExec runs cmd inside the sandbox's container with a PTY allocated,
// piping io to the terminal. The sandbox must be Active or Idle. It owns the
// container/user/workdir resolution so the public Sandbox.Exec (PTY path) is a
// thin delegation. A non-zero inner exit surfaces as the runtime's *ExecError,
// which the library boundary translates to the public *ExecExitError.
func (e *Engine) InteractiveExec(ctx context.Context, name string, cmd []string, io runtime.IOStreams) error {
	info, err := e.Inspect(ctx, name)
	if err != nil {
		return err
	}
	if info.Status != StatusActive && info.Status != StatusIdle {
		return fmt.Errorf("sandbox %q: %w", name, ErrContainerNotRunning)
	}
	user := ContainerUser(info.Environment, e.layout.HostUID)
	return e.runtime.InteractiveExec(ctx, store.InstanceName(e.layout.Principal, name), cmd,
		user, info.Environment.Workdir.MountPath, io)
}

// StdioExec runs cmd inside the sandbox's container with raw stdio piped to the
// supplied reader/writers (no PTY) — the line-oriented shape the MCP proxy
// bridges JSON-RPC over. Returns a *UsageError when the backend doesn't
// implement stdio exec (Tart/Seatbelt don't).
func (e *Engine) StdioExec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	execer, ok := e.runtime.(runtime.StdioExecer)
	if !ok {
		return yoerrors.NewUsageError("backend %s does not support stdio exec", e.runtime.Descriptor().Type)
	}
	return execer.StdioExec(ctx, store.InstanceName(e.layout.Principal, name), cmd, stdin, stdout, stderr)
}

// ContainerLogs returns the tail of the sandbox's raw container log (roughly
// tailLines lines). Returns "" when the container is gone or logs can't be
// fetched — this is best-effort diagnostics, distinct from the structured agent
// log stream.
func (e *Engine) ContainerLogs(ctx context.Context, name string, tailLines int) string {
	return runtime.LogsFor(ctx, e.runtime, store.InstanceName(e.layout.Principal, name), tailLines)
}

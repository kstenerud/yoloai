// ABOUTME: Implements runtime.ProcessLauncher for Docker — the non-blocking
// ABOUTME: Launch verb that starts a process and returns a Process handle.
package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/kstenerud/yoloai/runtime"
)

// compile-time assertion: *Runtime must satisfy ProcessLauncher.
var _ runtime.ProcessLauncher = (*Runtime)(nil)

// substrateReadyMarker is the in-container path of the readiness marker the
// keepalive entrypoint writes once root provisioning is complete (mirror of
// store.SubstrateReadyMarker, the host-relative path under the sandbox dir).
// entrypoint.py hard-codes the same path; keep them in sync.
const substrateReadyMarker = "/yoloai/logs/.substrate-ready"

// Ready reports whether the instance has finished root provisioning and can
// accept a launched session-runner. It checks, in-container, for the marker the
// keepalive entrypoint writes immediately before exec'ing the holder — so the
// substrate owns the readiness convention, not the caller. The probe always
// exits 0 (a missing marker is "not ready", not an exec error); a genuine exec
// failure (e.g. the container is not yet accepting execs) is returned as error.
func (r *Runtime) Ready(ctx context.Context, name string) (bool, error) {
	probe := "if [ -f " + substrateReadyMarker + " ]; then echo READY; fi"
	res, err := r.Exec(ctx, name, []string{"sh", "-c", probe}, "")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) == "READY", nil
}

// Launch starts a process inside the named running instance and returns a
// non-blocking Process handle. Unlike Exec/InteractiveExec it returns
// immediately after attaching; the caller drives I/O and calls Wait to
// collect the exit status.
//
// When spec.Detached is true the process is started without attaching stdio —
// it survives the caller's disconnect. Streams() returns empty/nil readers and
// the writer is nil; the process must redirect its own output to files inside
// the substrate.
func (r *Runtime) Launch(ctx context.Context, name string, spec runtime.ProcSpec) (runtime.Process, error) {
	if spec.Detached {
		return r.launchDetached(ctx, name, spec)
	}
	return r.launchAttached(ctx, name, spec)
}

// launchDetached starts a process with Detach:true so it survives the caller's
// disconnect. No stdio is attached; the returned Process has empty streams.
func (r *Runtime) launchDetached(ctx context.Context, name string, spec runtime.ProcSpec) (runtime.Process, error) {
	opts := container.ExecOptions{
		Cmd:          spec.Argv,
		User:         spec.User,
		WorkingDir:   spec.Cwd,
		Env:          spec.Env,
		Tty:          false,
		AttachStdin:  false,
		AttachStdout: false,
		AttachStderr: false,
	}
	execResp, err := r.client.ContainerExecCreate(ctx, name, opts)
	if err != nil {
		return nil, fmt.Errorf("launch detached exec create: %w", err)
	}
	if err := r.client.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return nil, fmt.Errorf("launch detached exec start: %w", err)
	}
	return &dockerProcess{
		client:   r.client,
		execID:   execResp.ID,
		detached: true,
	}, nil
}

// launchAttached starts a process with stdio attached, returning a Process
// whose Streams() carry the live I/O pipes.
func (r *Runtime) launchAttached(ctx context.Context, name string, spec runtime.ProcSpec) (runtime.Process, error) {
	opts := container.ExecOptions{
		Cmd:          spec.Argv,
		User:         spec.User,
		WorkingDir:   spec.Cwd,
		Env:          spec.Env,
		Tty:          spec.TTY,
		AttachStdin:  spec.Stdin,
		AttachStdout: true,
		AttachStderr: true,
	}
	if spec.TTY {
		// Ensure TERM is set — mirrors createExec behaviour (§12 of docker.go).
		hasTERM := false
		for _, e := range spec.Env {
			if strings.HasPrefix(e, "TERM=") {
				hasTERM = true
				break
			}
		}
		if !hasTERM {
			opts.Env = append(opts.Env, "TERM=xterm-256color")
		}
	}

	execResp, err := r.client.ContainerExecCreate(ctx, name, opts)
	if err != nil {
		return nil, fmt.Errorf("launch exec create: %w", err)
	}

	resp, err := r.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{Tty: spec.TTY})
	if err != nil {
		return nil, fmt.Errorf("launch exec attach: %w", err)
	}

	streams, err := buildProcessStreams(resp, spec)
	if err != nil {
		resp.Close()
		return nil, err
	}

	return &dockerProcess{
		client:  r.client,
		execID:  execResp.ID,
		resp:    resp,
		streams: streams,
	}, nil
}

// buildProcessStreams wires the hijacked docker response into RuntimeProcessStreams.
// For TTY, stdout is the single raw pty stream and stderr is nil. For non-TTY,
// a goroutine demultiplexes the docker multiplexed stream into two pipes.
func buildProcessStreams(resp dockertypes.HijackedResponse, spec runtime.ProcSpec) (runtime.ProcessStreams, error) {
	var streams runtime.ProcessStreams

	if spec.TTY {
		streams.Stdout = resp.Reader
	} else {
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()
		go func() {
			_, copyErr := stdcopy.StdCopy(stdoutW, stderrW, resp.Reader)
			stdoutW.CloseWithError(copyErr)
			stderrW.CloseWithError(copyErr)
		}()
		streams.Stdout = stdoutR
		streams.Stderr = stderrR
	}

	if spec.Stdin {
		streams.Stdin = &hijackWriteCloser{resp: resp}
	}

	return streams, nil
}

// hijackWriteCloser exposes the write half of a docker hijacked connection as
// an io.WriteCloser. Close calls CloseWrite to send EOF to the container process
// without tearing down the read side.
type hijackWriteCloser struct {
	resp dockertypes.HijackedResponse
}

func (h *hijackWriteCloser) Write(p []byte) (int, error) {
	return h.resp.Conn.Write(p)
}

func (h *hijackWriteCloser) Close() error {
	return h.resp.CloseWrite()
}

// dockerProcess is the concrete runtime.Process returned by Launch.
type dockerProcess struct {
	client   *dockerclient.Client
	execID   string
	resp     dockertypes.HijackedResponse
	streams  runtime.ProcessStreams
	detached bool // true when started detached; resp is zero-value and must not be closed
}

// ID returns the docker exec ID for this process.
func (p *dockerProcess) ID() string { return p.execID }

// Streams returns the process's I/O streams.
func (p *dockerProcess) Streams() runtime.ProcessStreams { return p.streams }

// Wait polls ContainerExecInspect until the process exits or ctx is cancelled,
// then closes the hijacked connection and returns the exit status.
// docker exec inspect carries no signal information, so Signaled is always false.
func (p *dockerProcess) Wait(ctx context.Context) (runtime.ExitStatus, error) {
	const pollInterval = 50 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return runtime.ExitStatus{}, ctx.Err()
		default:
		}
		inspect, err := p.client.ContainerExecInspect(ctx, p.execID)
		if err != nil {
			return runtime.ExitStatus{}, fmt.Errorf("exec inspect: %w", err)
		}
		if !inspect.Running {
			if !p.detached {
				p.resp.Close()
			}
			return runtime.ExitStatus{Code: inspect.ExitCode}, nil
		}
		select {
		case <-ctx.Done():
			return runtime.ExitStatus{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

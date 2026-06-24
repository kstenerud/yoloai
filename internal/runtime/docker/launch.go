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

	"github.com/kstenerud/yoloai/internal/runtime"
)

// compile-time assertion: *Runtime must satisfy ProcessLauncher.
var _ runtime.ProcessLauncher = (*Runtime)(nil)

// Launch starts a process inside the named running instance and returns a
// non-blocking Process handle. Unlike Exec/InteractiveExec it returns
// immediately after attaching; the caller drives I/O and calls Wait to
// collect the exit status.
func (r *Runtime) Launch(ctx context.Context, name string, spec runtime.ProcSpec) (runtime.Process, error) {
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
			stdoutW.CloseWithError(copyErr) //nolint:errcheck // pipe close; read error surfaced via pipe reads
			stderrW.CloseWithError(copyErr) //nolint:errcheck // pipe close; read error surfaced via pipe reads
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
	client  *dockerclient.Client
	execID  string
	resp    dockertypes.HijackedResponse
	streams runtime.ProcessStreams
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
			p.resp.Close()
			return runtime.ExitStatus{Code: inspect.ExitCode}, nil
		}
		select {
		case <-ctx.Done():
			return runtime.ExitStatus{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

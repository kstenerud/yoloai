//go:build linux

package microvm

// ABOUTME: Minimal QEMU guest-agent (QGA) client over the virtio-serial UNIX socket.
// ABOUTME: Raw JSON line protocol (net.Dial + encoding/json) — no external bindings.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// qgaSession is one connection to a VM's guest agent. QGA speaks newline-delimited
// JSON: each request is one object, each response one object on its own line.
type qgaSession struct {
	conn net.Conn
	r    *bufio.Reader
}

// dialQGA opens a guest-agent session and syncs it (clearing any partial state
// left in the agent's buffer from a previous connection).
func dialQGA(ctx context.Context, sockPath string) (*qgaSession, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	s := &qgaSession{conn: conn, r: bufio.NewReader(conn)}
	// guest-sync echoes a caller id; a matching reply means the channel is aligned.
	if _, err := s.call("guest-sync", map[string]any{"id": 0x51474121}); err != nil {
		s.close()
		return nil, fmt.Errorf("guest-sync: %w", err)
	}
	return s, nil
}

func (s *qgaSession) close() { _ = s.conn.Close() }

// call issues one QGA command and returns its raw "return" payload.
func (s *qgaSession) call(execute string, args any) (json.RawMessage, error) {
	req := map[string]any{"execute": execute}
	if args != nil {
		req["arguments"] = args
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := s.conn.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	line, err := s.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp struct {
		Return json.RawMessage `json:"return"`
		Error  *struct {
			Desc string `json:"desc"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode QGA response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("QGA error: %s", resp.Error.Desc)
	}
	return resp.Return, nil
}

// qgaPing reports whether the guest agent answers — the readiness signal that the
// VM has booted far enough to run commands.
func qgaPing(ctx context.Context, sockPath string) error {
	s, err := dialQGA(ctx, sockPath)
	if err != nil {
		return err
	}
	defer s.close()
	_, err = s.call("guest-ping", nil)
	return err
}

// qgaExec runs argv in the guest via guest-exec and blocks until it exits,
// returning trimmed stdout and the exit code. The first element of argv is the
// path; the rest are arguments.
func qgaExec(ctx context.Context, sockPath string, argv []string) (stdout string, exitCode int, err error) {
	s, err := dialQGA(ctx, sockPath)
	if err != nil {
		return "", 0, err
	}
	defer s.close()

	ret, err := s.call("guest-exec", map[string]any{
		"path":           argv[0],
		"arg":            argv[1:],
		"capture-output": true,
	})
	if err != nil {
		return "", 0, err
	}
	var started struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(ret, &started); err != nil {
		return "", 0, fmt.Errorf("decode guest-exec pid: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		default:
		}
		ret, err := s.call("guest-exec-status", map[string]any{"pid": started.PID})
		if err != nil {
			return "", 0, err
		}
		var st struct {
			Exited   bool   `json:"exited"`
			ExitCode int    `json:"exitcode"`
			OutData  string `json:"out-data"`
			ErrData  string `json:"err-data"`
		}
		if err := json.Unmarshal(ret, &st); err != nil {
			return "", 0, fmt.Errorf("decode guest-exec-status: %w", err)
		}
		if st.Exited {
			out, _ := base64.StdEncoding.DecodeString(st.OutData)
			return string(out), st.ExitCode, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
}

package seatbelt

// ABOUTME: Reaps leaked seatbelt host tmux servers whose sandbox dir is gone
// ABOUTME: (DF74) — the host-orphan half of `yoloai system prune` for seatbelt.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
)

// tmuxServer is one running tmux server process: its PID and the `-S` socket
// path it was launched with. The socket path encodes the owning sandbox
// (<sandboxesRoot>/<name>/tmux/tmux.sock), even after the socket file itself is
// deleted with the sandbox dir — which is exactly the leak this reaps.
type tmuxServer struct {
	pid    int
	socket string
}

// enumerateTmuxServers lists the running host tmux server processes. It is a var
// so tests can substitute a controllable enumerator; the platform default parses
// `ps` on macOS (seatbelt's host tmux) and is a no-op elsewhere.
var enumerateTmuxServers = platformTmuxServers

// killTmuxServer reaps one leaked tmux server. It is a var so tests can stub the
// actual kill; the default asks tmux to shut the server down via its socket and,
// when the socket is gone (the sandbox dir was deleted out from under it), falls
// back to signalling the PID directly.
var killTmuxServer = defaultKillTmuxServer

// Prune implements runtime.Backend. Seatbelt has no central instance registry,
// but it runs tmux as a host process, so a server can leak when the sandbox dir
// (and its socket) are deleted before Stop's best-effort kill-server lands
// (DF74). This reaps such orphans by enumerating host tmux servers directly and
// diffing against the sandbox registry — the identity-keyed-sweep principle
// (D114), the same shape as the broker and netns reapers.
func (r *Runtime) Prune(_ context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{Items: r.reapOrphanTmux(knownInstances, dryRun, output)}, nil
}

// reapOrphanTmux enumerates host tmux servers and reaps any whose socket points
// under this data dir's SandboxesDir() but whose sandbox is not in the known set
// (the loadable-metadata sandboxes from the same prune scan). Best-effort: an
// enumeration failure is a warning, not fatal to the prune.
//
// Scoping (DF45 sibling): the socket path is matched against THIS data dir's
// SandboxesDir(), so a server belonging to another data dir sharing the host is
// left alone — sufficient for the single-data-dir default; precise per-principal
// scoping is deferred to the D62 multi-principal daemon.
func (r *Runtime) reapOrphanTmux(knownInstances []string, dryRun bool, output io.Writer) []runtime.PruneItem {
	servers, err := enumerateTmuxServers()
	if err != nil {
		fmt.Fprintf(output, "Warning: tmux server sweep failed: %v\n", err) //nolint:errcheck // best-effort progress
		return nil
	}

	root := r.layout.SandboxesDir()
	// The known set carries principal-prefixed instance names; the socket path
	// encodes the on-disk sandbox dir name. Normalize the known set to dir names
	// so the pure selector compares like with like.
	prefix := config.InstancePrefix(r.layout.Principal)
	knownDirs := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		knownDirs[strings.TrimPrefix(name, prefix)] = true
	}

	var items []runtime.PruneItem
	for _, srv := range selectOrphanTmux(servers, knownDirs, root) {
		name, _ := sandboxDirFromSocket(srv.socket, root)
		if !dryRun {
			if killErr := killTmuxServer(r.execEnv, srv); killErr != nil {
				fmt.Fprintf(output, "Warning: reap tmux server %s (pid %d): %v\n", name, srv.pid, killErr) //nolint:errcheck // best-effort progress
				continue
			}
		}
		items = append(items, runtime.PruneItem{Kind: "tmux", Name: fmt.Sprintf("%s pid %d", name, srv.pid)})
	}
	return items
}

// selectOrphanTmux returns the tmux servers whose socket lives under
// sandboxesRoot but whose owning sandbox dir is not in knownDirs — the DF74
// decision, factored out (pure) for testing. A server whose socket is outside
// this data dir's sandboxes (a different data dir, DF45 sibling) or whose
// sandbox is known/live is left alone.
func selectOrphanTmux(servers []tmuxServer, knownDirs map[string]bool, sandboxesRoot string) []tmuxServer {
	var orphans []tmuxServer
	for _, srv := range servers {
		name, ok := sandboxDirFromSocket(srv.socket, sandboxesRoot)
		if !ok {
			continue // socket not under this data dir's sandboxes
		}
		if knownDirs[name] {
			continue // a live or known sandbox owns it
		}
		orphans = append(orphans, srv)
	}
	return orphans
}

// sandboxDirFromSocket returns the sandbox dir name a seatbelt tmux socket
// belongs to, or ok=false when the path is not a
// <sandboxesRoot>/<name>/tmux/tmux.sock under this root. Uses the same layout
// the backend writes (TmuxSocket), so it stays in lockstep with it. filepath.Rel
// yields a "../…" path for any socket outside sandboxesRoot, which is rejected —
// this is what scopes the sweep to this data dir.
func sandboxDirFromSocket(socket, sandboxesRoot string) (string, bool) {
	rel, err := filepath.Rel(sandboxesRoot, socket)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || parts[1] != config.TmuxDirName || parts[2] != tmuxSocketName {
		return "", false
	}
	if parts[0] == "" || parts[0] == ".." || parts[0] == "." {
		return "", false
	}
	return parts[0], true
}

// parseTmuxServerLine parses one `pid args…` line from `ps -axo pid=,args=` into
// a tmuxServer. It matches only tmux SERVER processes — argv[0]'s basename is
// "tmux" and argv carries the `new-session` verb (the launch command the server
// process retains) — and extracts the `-S <socket>` path. Client processes
// (`wait-for`, `attach`) lack `new-session` and are ignored; killing the server
// takes them down anyway.
func parseTmuxServerLine(line string) (tmuxServer, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return tmuxServer{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return tmuxServer{}, false
	}
	argv := fields[1:]
	if filepath.Base(argv[0]) != "tmux" || !slices.Contains(argv, "new-session") {
		return tmuxServer{}, false
	}
	for i, tok := range argv {
		if tok == "-S" && i+1 < len(argv) {
			return tmuxServer{pid: pid, socket: argv[i+1]}, true
		}
	}
	return tmuxServer{}, false
}

// defaultKillTmuxServer shuts a leaked tmux server down: first the graceful path
// (kill-server over its socket, which also tears down its session and panes),
// then — when the socket is gone, the common DF74 case — a direct PID signal.
func defaultKillTmuxServer(execEnv []string, srv tmuxServer) error {
	if sysexec.Command(execEnv, "tmux", "-S", srv.socket, "kill-server").Run() == nil {
		return nil
	}
	return killTmuxPID(srv.pid)
}

// killTmuxPID asks the process to terminate (SIGTERM), then forces it (SIGKILL)
// if it survives a short grace period — the same escalation the broker reaper
// uses. A tmux server exits cleanly on SIGTERM; SIGKILL is the backstop.
func killTmuxPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return nil
}

// pidAlive reports whether the process still exists (signal 0 probes existence
// without delivering a signal).
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

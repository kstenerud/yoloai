package seatbelt

// ABOUTME: Reaps leaked seatbelt host processes (tmux server + the detached
// ABOUTME: sandbox-setup.py/status-monitor.py tree) whose sandbox dir is gone
// ABOUTME: (DF74/DF75) — the host-orphan half of `yoloai system prune`.

import (
	"context"
	"fmt"
	"io"
	"os"
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

// sandboxProc is one running host process belonging to a seatbelt sandbox: its
// PID, the sandbox dir name its argv points into, and (for a tmux server only)
// the `-S` socket path. Seatbelt runs all of these on the host — the tmux
// server, plus the detached sandbox-setup.py and status-monitor.py — so any of
// them can outlive a deleted sandbox dir and leak (DF74/DF75).
type sandboxProc struct {
	pid    int
	name   string // owning sandbox dir name (encoded in the argv path)
	socket string // tmux -S socket; "" for a non-tmux process
}

// enumerateSandboxProcs lists the running host processes that belong to a
// seatbelt sandbox under sandboxesRoot. It is a var so tests can substitute a
// controllable enumerator; the platform default parses `ps` on macOS (seatbelt's
// host processes) and is a no-op elsewhere.
var enumerateSandboxProcs = platformSandboxProcs

// killSandboxProc reaps one leaked seatbelt host process. It is a var so tests
// can stub the actual kill; the default shuts a tmux server down via its socket
// (which also takes its panes) and otherwise — or when the socket is gone —
// signals the PID directly.
var killSandboxProc = defaultKillSandboxProc

// Prune implements runtime.Backend. Seatbelt has no central instance registry,
// but it runs its sandbox as HOST processes, so they can leak when the sandbox
// dir is deleted before Stop's best-effort teardown lands (DF74/DF75). This
// reaps such orphans by enumerating seatbelt host processes directly and diffing
// against the sandbox registry — the identity-keyed-sweep principle (D114), the
// same shape as the broker and netns reapers.
func (r *Runtime) Prune(_ context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{Items: r.reapOrphanProcs(knownInstances, dryRun, output)}, nil
}

// reapOrphanProcs enumerates seatbelt host processes and reaps any whose argv
// points under this data dir's SandboxesDir() but whose sandbox is not in the
// known set (the loadable-metadata sandboxes from the same prune scan). This
// covers the whole leaked process group — the tmux server AND the detached
// sandbox-setup.py/status-monitor.py, the latter of which otherwise keeps
// writing into (and thereby resurrecting) the deleted sandbox dir (DF75).
// Best-effort: an enumeration failure is a warning, not fatal to the prune.
//
// Scoping (DF45 sibling): argv paths are matched against THIS data dir's
// SandboxesDir(), so a process belonging to another data dir sharing the host is
// left alone — sufficient for the single-data-dir default; precise per-principal
// scoping is deferred to the D62 multi-principal daemon.
func (r *Runtime) reapOrphanProcs(knownInstances []string, dryRun bool, output io.Writer) []runtime.PruneItem {
	root := r.layout.SandboxesDir()
	procs, err := enumerateSandboxProcs(root)
	if err != nil {
		fmt.Fprintf(output, "Warning: seatbelt process sweep failed: %v\n", err) //nolint:errcheck // best-effort progress
		return nil
	}

	// The known set carries principal-prefixed instance names; the argv path
	// encodes the on-disk sandbox dir name. Normalize the known set to dir names
	// so the pure selector compares like with like.
	prefix := config.InstancePrefix(r.layout.Principal)
	knownDirs := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		knownDirs[strings.TrimPrefix(name, prefix)] = true
	}

	self := os.Getpid()
	var items []runtime.PruneItem
	for _, proc := range selectOrphanProcs(procs, knownDirs) {
		if proc.pid == self {
			continue // never reap the running prune itself
		}
		if !dryRun {
			if killErr := killSandboxProc(r.execEnv, proc); killErr != nil {
				fmt.Fprintf(output, "Warning: reap seatbelt process %s (pid %d): %v\n", proc.name, proc.pid, killErr) //nolint:errcheck // best-effort progress
				continue
			}
		}
		kind := "process"
		if proc.socket != "" {
			kind = "tmux"
		}
		items = append(items, runtime.PruneItem{Kind: kind, Name: fmt.Sprintf("%s pid %d", proc.name, proc.pid)})
	}
	return items
}

// selectOrphanProcs returns the seatbelt host processes whose owning sandbox dir
// is not in knownDirs — the DF74/DF75 decision, factored out (pure) for testing.
// A process whose sandbox is known/live is left alone; processes outside this
// data dir's sandboxes were already filtered out during enumeration.
func selectOrphanProcs(procs []sandboxProc, knownDirs map[string]bool) []sandboxProc {
	var orphans []sandboxProc
	for _, proc := range procs {
		if knownDirs[proc.name] {
			continue // a live or known sandbox owns it
		}
		orphans = append(orphans, proc)
	}
	return orphans
}

// parseSandboxProcLine parses one `pid args…` line from `ps -axo pid=,args=`
// into a sandboxProc, or ok=false when the process does not belong to a sandbox
// under sandboxesRoot. A process belongs to a sandbox when some argv token is a
// path under <sandboxesRoot>/<name>/… — true for the tmux server (its -S socket
// and -f config), the python setup/monitor (their sandbox-dir args), and the
// tmux panes (their log paths). A tmux server is additionally recognised (argv[0]
// basename "tmux" + the `new-session` verb) so its socket can be captured for a
// graceful kill-server.
func parseSandboxProcLine(line, sandboxesRoot string) (sandboxProc, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return sandboxProc{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return sandboxProc{}, false
	}
	argv := fields[1:]

	var name string
	for _, tok := range argv {
		if n, ok := sandboxNameFromPath(tok, sandboxesRoot); ok {
			name = n
			break
		}
	}
	if name == "" {
		return sandboxProc{}, false
	}

	proc := sandboxProc{pid: pid, name: name}
	if filepath.Base(argv[0]) == "tmux" && slices.Contains(argv, "new-session") {
		for i, tok := range argv {
			if tok == "-S" && i+1 < len(argv) {
				proc.socket = argv[i+1]
				break
			}
		}
	}
	return proc, true
}

// sandboxNameFromPath returns the sandbox dir name a path token belongs to, or
// ok=false when the token is not an absolute path under sandboxesRoot. The name
// is the first path component beneath the root (<sandboxesRoot>/<name>/…).
// filepath.Rel yields a "../…" result for anything outside the root, which is
// rejected — this is what scopes the sweep to this data dir.
func sandboxNameFromPath(token, sandboxesRoot string) (string, bool) {
	rel, err := filepath.Rel(sandboxesRoot, token)
	if err != nil {
		return "", false // token not under (or not comparable to) the root
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	name := rel
	if i := strings.IndexByte(rel, filepath.Separator); i >= 0 {
		name = rel[:i]
	}
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	return name, true
}

// defaultKillSandboxProc reaps one leaked seatbelt process. A tmux server is shut
// down via kill-server over its socket (which also tears down its session and
// panes); when that fails — the socket is gone, the common DF74 case — or the
// process is not a tmux server, it is signalled directly.
func defaultKillSandboxProc(execEnv []string, proc sandboxProc) error {
	if proc.socket != "" {
		if sysexec.Command(execEnv, "tmux", "-S", proc.socket, "kill-server").Run() == nil {
			return nil
		}
	}
	return killPID(proc.pid)
}

// killPID asks the process to terminate (SIGTERM), then forces it (SIGKILL) if
// it survives a short grace period — the same escalation the broker reaper uses.
// A process already gone (ESRCH) counts as reaped: the sweep may list a tmux
// pane that its server's kill-server already took down.
func killPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return nil // already gone
		}
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

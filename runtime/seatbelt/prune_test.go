// ABOUTME: Unit tests for the DF74/DF75 seatbelt host-process reaper — the pure
// ABOUTME: decision (which leaked processes to reap) plus the reap wiring, with
// ABOUTME: no real processes and no root, mirroring netns_sweep_test.go.

package seatbelt

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sock(root, name string) string {
	return filepath.Join(root, name, config.TmuxDirName, tmuxSocketName)
}

func TestParseSandboxProcLine(t *testing.T) {
	root := "/data/sandboxes"
	tests := []struct {
		name       string
		line       string
		wantOK     bool
		wantPID    int
		wantName   string
		wantSocket string
	}{
		{
			name:       "tmux server (has -S socket)",
			line:       "74415 /opt/homebrew/bin/tmux -S /data/sandboxes/x/tmux/tmux.sock -f /data/sandboxes/x/tmux/tmux.conf new-session -d -s main",
			wantOK:     true,
			wantPID:    74415,
			wantName:   "x",
			wantSocket: "/data/sandboxes/x/tmux/tmux.sock",
		},
		{
			name:     "status-monitor.py (no socket, but sandbox-dir args)",
			line:     "10202 python3 /data/sandboxes/sbcheck/bin/status-monitor.py /data/sandboxes/sbcheck/runtime-config.json /data/sandboxes/sbcheck/agent-status.json /data/sandboxes/sbcheck/tmux/tmux.sock",
			wantOK:   true,
			wantPID:  10202,
			wantName: "sbcheck",
		},
		{
			name:     "sandbox-setup.py",
			line:     "9859 python3 /data/sandboxes/y/bin/sandbox-setup.py seatbelt /data/sandboxes/y",
			wantOK:   true,
			wantPID:  9859,
			wantName: "y",
		},
		{
			name:     "tmux pane writing the agent log",
			line:     "9869 sh -c cat >> /data/sandboxes/sbcheck/logs/agent.log",
			wantOK:   true,
			wantPID:  9869,
			wantName: "sbcheck",
		},
		{
			name:     "wait-for client (tmux, no new-session → not a server)",
			line:     "74691 /opt/homebrew/bin/tmux -S /data/sandboxes/x/tmux/tmux.sock wait-for yoloai-exit",
			wantOK:   true,
			wantPID:  74691,
			wantName: "x",
			// no socket captured: not a server, so it is reaped by PID like any other
		},
		{
			name:   "process under a different data dir rejected",
			line:   "500 /opt/homebrew/bin/tmux -S /other/sandboxes/x/tmux/tmux.sock new-session -d",
			wantOK: false,
		},
		{
			name:   "process with no sandbox path rejected",
			line:   "600 /usr/bin/ssh karl@host",
			wantOK: false,
		},
		{
			name:   "non-numeric pid rejected",
			line:   "notapid python3 /data/sandboxes/x/bin/status-monitor.py",
			wantOK: false,
		},
		{
			name:   "empty line rejected",
			line:   "",
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSandboxProcLine(tc.line, root)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantPID, got.pid)
				assert.Equal(t, tc.wantName, got.name)
				assert.Equal(t, tc.wantSocket, got.socket)
			}
		})
	}
}

func TestSandboxNameFromPath(t *testing.T) {
	root := "/data/sandboxes"
	tests := []struct {
		name     string
		token    string
		wantName string
		wantOK   bool
	}{
		{"tmux socket path", sock(root, "x"), "x", true},
		{"sandbox dir itself", filepath.Join(root, "y"), "y", true},
		{"hyphenated deep path", filepath.Join(root, "ysmk-abc-011", "bin", "status-monitor.py"), "ysmk-abc-011", true},
		{"outside root (legacy data dir)", "/other/sandboxes/x/tmux/tmux.sock", "", false},
		{"root itself", root, "", false},
		{"relative token", "cat", "", false},
		{"non-path flag token", "-d", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, ok := sandboxNameFromPath(tc.token, root)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantName, name)
		})
	}
}

func TestSelectOrphanProcs(t *testing.T) {
	procs := []sandboxProc{
		{pid: 100, name: "orphan", socket: "/s/orphan/tmux/tmux.sock"}, // not known → reap
		{pid: 101, name: "orphan"},                                     // its monitor → reap
		{pid: 102, name: "live", socket: "/s/live/tmux/tmux.sock"},     // known → keep
		{pid: 103, name: "live"},                                       // known → keep
		{pid: 104, name: "x"},                                          // not known → reap
	}
	known := map[string]bool{"live": true}

	var pids []int
	for _, o := range selectOrphanProcs(procs, known) {
		pids = append(pids, o.pid)
	}
	assert.Equal(t, []int{100, 101, 104}, pids)
}

func TestReapOrphanProcs_NormalizesKnownReapsGroupAndSkipsSelf(t *testing.T) {
	r := &Runtime{layout: config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)}
	sandboxes := r.layout.SandboxesDir()

	procs := []sandboxProc{
		{pid: 1, name: "orphan", socket: sock(sandboxes, "orphan")}, // tmux server → reap (kind tmux)
		{pid: 2, name: "orphan"},                                    // its monitor → reap (kind process)
		{pid: 3, name: "keep", socket: sock(sandboxes, "keep")},     // known (prefixed) → keep
		{pid: os.Getpid(), name: "orphan"},                          // never reap the prune itself
	}
	restore := stubEnum(procs)
	defer restore()

	var killed []int
	restoreKill := stubKill(&killed)
	defer restoreKill()

	// The known set carries principal-prefixed instance names, as classifySandboxes
	// emits; reapOrphanProcs must normalize them back to dir names.
	known := []string{store.InstanceName(r.layout.Principal, "keep")}

	// Dry run: reports the orphan group but kills nothing.
	items := r.reapOrphanProcs(known, true, io.Discard)
	require.Len(t, items, 2)
	assert.Equal(t, "tmux", string(items[0].Kind))
	assert.Equal(t, "process", string(items[1].Kind))
	assert.Empty(t, killed)

	// Real run: reaps the orphan tmux server + its monitor, never self or keep.
	items = r.reapOrphanProcs(known, false, io.Discard)
	require.Len(t, items, 2)
	assert.Equal(t, []int{1, 2}, killed)
}

func TestReapOrphanProcs_EnumerationFailureIsNonFatal(t *testing.T) {
	r := &Runtime{layout: config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)}
	restore := stubEnumErr()
	defer restore()
	assert.Nil(t, r.reapOrphanProcs(nil, false, io.Discard))
}

// --- test doubles -----------------------------------------------------------

func stubEnum(procs []sandboxProc) func() {
	prev := enumerateSandboxProcs
	enumerateSandboxProcs = func(string) ([]sandboxProc, error) { return procs, nil }
	return func() { enumerateSandboxProcs = prev }
}

func stubEnumErr() func() {
	prev := enumerateSandboxProcs
	enumerateSandboxProcs = func(string) ([]sandboxProc, error) { return nil, assert.AnError }
	return func() { enumerateSandboxProcs = prev }
}

func stubKill(killed *[]int) func() {
	prev := killSandboxProc
	killSandboxProc = func(_ []string, proc sandboxProc) error {
		*killed = append(*killed, proc.pid)
		return nil
	}
	return func() { killSandboxProc = prev }
}

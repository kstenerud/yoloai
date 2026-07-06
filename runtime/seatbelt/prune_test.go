// ABOUTME: Unit tests for the DF74 seatbelt tmux-orphan reaper — the pure
// ABOUTME: decision (which leaked tmux servers to reap) plus the reap wiring,
// ABOUTME: with no real tmux and no root, mirroring netns_sweep_test.go.

package seatbelt

import (
	"io"
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

func TestParseTmuxServerLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantOK     bool
		wantPID    int
		wantSocket string
	}{
		{
			name:       "server with absolute tmux path",
			line:       "74415 /opt/homebrew/bin/tmux -S /data/sandboxes/x/tmux/tmux.sock -f /data/sandboxes/x/tmux/tmux.conf new-session -d -s main -x 200 -y 50",
			wantOK:     true,
			wantPID:    74415,
			wantSocket: "/data/sandboxes/x/tmux/tmux.sock",
		},
		{
			name:       "server with bare tmux name",
			line:       "14138 tmux -S /data/sandboxes/y/tmux/tmux.sock -f /data/sandboxes/y/tmux/tmux.conf new-session -d -s main",
			wantOK:     true,
			wantPID:    14138,
			wantSocket: "/data/sandboxes/y/tmux/tmux.sock",
		},
		{
			// wait-for is a client, not a server: no new-session verb → ignored.
			name:   "wait-for client rejected",
			line:   "74691 /opt/homebrew/bin/tmux -S /data/sandboxes/x/tmux/tmux.sock wait-for yoloai-exit",
			wantOK: false,
		},
		{
			name:   "non-tmux process rejected",
			line:   "9859 python3 /data/sandboxes/x/bin/sandbox-setup.py seatbelt new-session",
			wantOK: false,
		},
		{
			name:   "tmux server without -S rejected",
			line:   "500 tmux new-session -d -s main",
			wantOK: false,
		},
		{
			name:   "non-numeric pid rejected",
			line:   "notapid tmux -S /s/tmux/tmux.sock new-session",
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
			got, ok := parseTmuxServerLine(tc.line)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantPID, got.pid)
				assert.Equal(t, tc.wantSocket, got.socket)
			}
		})
	}
}

func TestSandboxDirFromSocket(t *testing.T) {
	root := "/data/sandboxes"
	tests := []struct {
		name     string
		socket   string
		wantName string
		wantOK   bool
	}{
		{"canonical", sock(root, "x"), "x", true},
		{"hyphenated name", sock(root, "ysmk-abc-seatbelt-011"), "ysmk-abc-seatbelt-011", true},
		{"outside root (legacy data dir)", "/other/sandboxes/x/tmux/tmux.sock", "", false},
		{"wrong socket filename", filepath.Join(root, "x", config.TmuxDirName, "other.sock"), "", false},
		{"wrong subdir", filepath.Join(root, "x", "notmux", tmuxSocketName), "", false},
		{"too shallow", filepath.Join(root, tmuxSocketName), "", false},
		{"nested too deep", filepath.Join(root, "a", "b", config.TmuxDirName, tmuxSocketName), "", false},
		{"root itself", root, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, ok := sandboxDirFromSocket(tc.socket, root)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantName, name)
		})
	}
}

func TestSelectOrphanTmux(t *testing.T) {
	root := "/data/sandboxes"
	legacyRoot := "/home/user/.yoloai/sandboxes" // a different data dir sharing the host

	servers := []tmuxServer{
		{pid: 100, socket: sock(root, "orphan")},         // under root, not known → reap
		{pid: 101, socket: sock(root, "live")},           // under root, known → keep
		{pid: 102, socket: sock(legacyRoot, "x")},        // other data dir → out of scope, keep
		{pid: 103, socket: sock(root, "x")},              // stacked-leak duplicate name → reap
		{pid: 104, socket: sock(root, "x")},              // stacked-leak duplicate name → reap
		{pid: 105, socket: "/tmp/random/tmux/tmux.sock"}, // unrelated tmux → ignore
	}
	known := map[string]bool{"live": true}

	orphans := selectOrphanTmux(servers, known, root)

	var pids []int
	for _, o := range orphans {
		pids = append(pids, o.pid)
	}
	assert.Equal(t, []int{100, 103, 104}, pids)
}

func TestReapOrphanTmux_NormalizesKnownAndKills(t *testing.T) {
	r := &Runtime{layout: config.NewLayout(t.TempDir())}
	sandboxes := r.layout.SandboxesDir()

	servers := []tmuxServer{
		{pid: 1, socket: sock(sandboxes, "orphan")}, // not known → reap
		{pid: 2, socket: sock(sandboxes, "keep")},   // known (prefixed) → keep
		{pid: 4, socket: sock("/elsewhere", "x")},   // other data dir → keep
	}
	restore := stubEnum(servers)
	defer restore()

	var killed []int
	restoreKill := stubKill(&killed)
	defer restoreKill()

	// The known set carries principal-prefixed instance names, as classifySandboxes
	// emits; reapOrphanTmux must normalize them back to dir names to match sockets.
	known := []string{store.InstanceName(r.layout.Principal, "keep")}

	// Dry run: reports the orphan but kills nothing.
	items := r.reapOrphanTmux(known, true, io.Discard)
	require.Len(t, items, 1)
	assert.Equal(t, "tmux", string(items[0].Kind))
	assert.Contains(t, items[0].Name, "orphan")
	assert.Empty(t, killed)

	// Real run: reaps exactly the orphan.
	items = r.reapOrphanTmux(known, false, io.Discard)
	require.Len(t, items, 1)
	assert.Equal(t, []int{1}, killed)
}

func TestReapOrphanTmux_EnumerationFailureIsNonFatal(t *testing.T) {
	r := &Runtime{layout: config.NewLayout(t.TempDir())}
	restore := stubEnumErr()
	defer restore()
	assert.Nil(t, r.reapOrphanTmux(nil, false, io.Discard))
}

// --- test doubles -----------------------------------------------------------

func stubEnum(servers []tmuxServer) func() {
	prev := enumerateTmuxServers
	enumerateTmuxServers = func() ([]tmuxServer, error) { return servers, nil }
	return func() { enumerateTmuxServers = prev }
}

func stubEnumErr() func() {
	prev := enumerateTmuxServers
	enumerateTmuxServers = func() ([]tmuxServer, error) { return nil, assert.AnError }
	return func() { enumerateTmuxServers = prev }
}

func stubKill(killed *[]int) func() {
	prev := killTmuxServer
	killTmuxServer = func(_ []string, srv tmuxServer) error {
		*killed = append(*killed, srv.pid)
		return nil
	}
	return func() { killTmuxServer = prev }
}

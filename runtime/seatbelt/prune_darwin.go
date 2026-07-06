// ABOUTME: macOS enumeration of running host tmux server processes via `ps`
// ABOUTME: (seatbelt runs tmux on the host) — feeds the DF74 tmux-orphan reaper.
//go:build darwin

package seatbelt

import (
	"bufio"
	"bytes"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// psEnv is the minimal explicit environment for the `ps` census: sysexec never
// inherits the ambient env, and `ps` only needs a PATH to resolve.
var psEnv = []string{"PATH=/usr/bin:/bin:/usr/local/bin"}

// platformTmuxServers lists host tmux server processes by parsing
// `ps -axo pid=,args=` (macOS has no /proc). Each line is filtered to tmux
// servers by parseTmuxServerLine.
func platformTmuxServers() ([]tmuxServer, error) {
	out, err := sysexec.Command(psEnv, "ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	var servers []tmuxServer
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if srv, ok := parseTmuxServerLine(sc.Text()); ok {
			servers = append(servers, srv)
		}
	}
	return servers, sc.Err()
}

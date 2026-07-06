// ABOUTME: macOS enumeration of a seatbelt sandbox's host processes via `ps`
// ABOUTME: (seatbelt runs them on the host) — feeds the DF74/DF75 orphan reaper.
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

// platformSandboxProcs lists the host processes belonging to a seatbelt sandbox
// under sandboxesRoot by parsing `ps -axo pid=,args=` (macOS has no /proc). Each
// line is filtered and classified by parseSandboxProcLine.
func platformSandboxProcs(sandboxesRoot string) ([]sandboxProc, error) {
	out, err := sysexec.Command(psEnv, "ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	var procs []sandboxProc
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if proc, ok := parseSandboxProcLine(sc.Text(), sandboxesRoot); ok {
			procs = append(procs, proc)
		}
	}
	return procs, sc.Err()
}

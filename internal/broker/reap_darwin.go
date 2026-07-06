// ABOUTME: macOS enumeration of running `__inject` injector processes via `ps`
// ABOUTME: (no /proc), matching the running binary's basename + the __inject marker.
//go:build darwin

package broker

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// enumerateInjectorPIDs lists injector PIDs by parsing `ps -axo pid=,args=`.
// A line matches when argv[0]'s basename equals the running binary's basename
// and some argv token is the inject verb. macOS has no /proc, so this is the
// portable equivalent of the Linux cmdline walk. Not yet verified on a Mac —
// tracked in the mac-verification queue.
func platformInjectorPIDs() ([]int, error) {
	out, err := exec.Command("ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	selfBase := selfBinaryBase()
	var pids []int
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		pid, ok := parsePsLine(sc.Text(), selfBase)
		if ok {
			pids = append(pids, pid)
		}
	}
	return pids, sc.Err()
}

// parsePsLine parses one `pid args…` line: the PID, then argv[0] basename == selfBase
// and an argv token equal to InjectVerb.
func parsePsLine(line, selfBase string) (int, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	if filepath.Base(fields[1]) != selfBase {
		return 0, false
	}
	if slices.Contains(fields[2:], InjectVerb) {
		return pid, true
	}
	return 0, false
}

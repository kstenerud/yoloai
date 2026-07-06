// ABOUTME: Linux enumeration of running `__inject` injector processes via /proc,
// ABOUTME: matching the running binary's basename + the __inject argv marker.
//go:build linux

package broker

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
)

// platformInjectorPIDs walks /proc for processes whose argv is
// `<our-binary> __inject`. It matches argv[1] == InjectVerb and argv[0]'s
// basename against the running executable's basename, so it reaps only injectors
// spawned by this binary (a `(deleted)` exe still carries the original path in
// argv, so a rebuilt binary's orphan is still matched).
func platformInjectorPIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	selfBase := selfBinaryBase()
	var pids []int
	for _, e := range entries {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil {
			continue // not a PID dir
		}
		data, rerr := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if rerr != nil {
			continue // process vanished / not readable
		}
		if injectorArgvMatches(data, selfBase) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// injectorArgvMatches reports whether a NUL-separated /proc cmdline is
// `<selfBase> __inject …` — argv[0] basename equals selfBase and argv[1] is the
// inject verb.
func injectorArgvMatches(cmdline []byte, selfBase string) bool {
	parts := bytes.Split(cmdline, []byte{0})
	if len(parts) < 2 {
		return false
	}
	if filepath.Base(string(parts[0])) != selfBase {
		return false
	}
	return string(parts[1]) == InjectVerb
}

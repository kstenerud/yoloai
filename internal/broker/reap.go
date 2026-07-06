// ABOUTME: ReapOrphanInjectors reaps leaked `__inject` broker processes whose
// ABOUTME: sandbox is gone — the host-orphan half of `yoloai system prune` (DF71).
package broker

import (
	"os"
	"path/filepath"
)

// enumerateInjectorPIDs lists the PIDs of running injector processes. It is a var
// so tests can substitute a controllable enumerator (the platform default walks
// /proc on Linux, `ps` on macOS).
var enumerateInjectorPIDs = platformInjectorPIDs

// selfBinaryBase is the basename of the running executable, falling back to
// "yoloai" if it can't be resolved. Used to scope the injector sweep to
// processes spawned by this binary.
func selfBinaryBase() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "yoloai"
	}
	return filepath.Base(exe)
}

// ReapOrphanInjectors kills every running injector (`<binary> __inject`) process
// whose PID is not in keep, and returns the PIDs it reaped (or, under dryRun,
// would reap). keep is the set of PIDs recorded by live sandboxes' injector.json
// — the callers build it from broker.LoadRecord over the sandbox registry.
//
// This is the backstop for a broker orphaned by a crash, SIGKILL, or the
// create-replace path: because the injector is Setsid-detached (it must outlive
// the CLI) and its PID lives only in injector.json, once that record is deleted
// the per-sandbox Stop can no longer find the process. Enumerating the running
// injectors directly and diffing against the live set reaps it regardless (D114).
//
// Scoping caveat (DF45 sibling): an injector carries no data-dir in its argv, so
// this is correct for the single-data-dir default but could over-reap a broker
// belonging to another data dir sharing the host. Deferred to the D62
// multi-principal daemon.
func ReapOrphanInjectors(keep map[int]bool, dryRun bool) (reaped []int, err error) {
	pids, err := enumerateInjectorPIDs()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	for _, pid := range pids {
		if pid == self || keep[pid] {
			continue
		}
		if !dryRun {
			killProcess(pid)
		}
		reaped = append(reaped, pid)
	}
	return reaped, nil
}

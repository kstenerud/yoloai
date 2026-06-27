// ABOUTME: VM-slot census for tart — detects which macOS VMs occupy the
// ABOUTME: Virtualization.framework concurrency limit and whether each is a
// ABOUTME: live (owned) sandbox or an orphaned process leaked by a crashed
// ABOUTME: `tart run`. Lets doctor explain a reached VM limit and how to clear it.

package tart

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
)

// maxConcurrentMacVMs is Apple's Virtualization.framework cap on simultaneously
// running macOS guests. A reached limit only resets when a VM stops — or, for a
// process leaked by a crashed launcher, when that process is killed or the host
// reboots. See checkVMLimitError for the reactive (boot-time) counterpart.
const maxConcurrentMacVMs = 2

// vmXPCProcessSubstr matches the Virtualization.framework per-VM helper process.
// One such process exists per running VM and holds the VM's disk image open,
// which is how we map a process back to its VM (and how we spot orphans whose
// launcher died but whose VM process — and slot — survives).
const vmXPCProcessSubstr = "com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine"

// vmDiskPathRe extracts the VM name from an open ~/.tart/vms/<name>/… path.
var vmDiskPathRe = regexp.MustCompile(`\.tart/vms/([^/]+)/`)

// vmProcess is a raw, pre-classification view of one hypervisor VM process.
type vmProcess struct {
	PID     int
	VMName  string // resolved from the open disk image; "" if undeterminable
	Deleted bool   // disk image is gone (open-but-unlinked) — a crashed temp VM
}

// VMCensus reports the macOS VM slots currently in use and classifies each as
// an owned sandbox or an orphaned (leaked) process. It implements
// runtime.VMCensusReporter. Detection is best-effort: a failed pgrep/lsof/ps
// yields an empty census rather than an error.
func (r *Runtime) VMCensus(ctx context.Context) (runtime.VMCensus, error) {
	procs := detectVMProcesses(ctx, r.execEnv)
	owners := detectTartRunOwners(ctx, r.execEnv)
	return classifyVMSlots(procs, owners, maxConcurrentMacVMs), nil
}

// classifyVMSlots is the pure core: given the hypervisor VM processes and the
// set of VM names with a live `tart run` launcher, it labels each slot
// owned/orphan. A VM is owned only when its launcher is still alive and its
// disk image still exists; otherwise the process is a leaked orphan holding a
// slot. Slots are ordered owned-first, then by PID, for stable display.
func classifyVMSlots(procs []vmProcess, liveOwners map[string]bool, limit int) runtime.VMCensus {
	slots := make([]runtime.VMSlot, 0, len(procs))
	for _, p := range procs {
		owned := !p.Deleted && p.VMName != "" && liveOwners[p.VMName]
		slots = append(slots, runtime.VMSlot{
			PID:     p.PID,
			VMName:  p.VMName,
			Owned:   owned,
			Deleted: p.Deleted,
		})
	}
	sort.SliceStable(slots, func(i, j int) bool {
		if slots[i].Owned != slots[j].Owned {
			return slots[i].Owned // owned before orphan
		}
		return slots[i].PID < slots[j].PID
	})
	return runtime.VMCensus{Limit: limit, Slots: slots}
}

// detectVMProcesses lists the host's Virtualization.framework per-VM processes
// and keeps only those belonging to tart, resolving each to its VM name via the
// disk image it holds open. The XPC process is shared by every app that uses
// Virtualization.framework (Claude.app, Docker Desktop, …), so a process is
// counted only when it positively holds a ~/.tart/vms/ disk open — we must
// never report another app's VM as a killable tart orphan. Foreign VMs are
// also typically Linux guests, which don't count against the macOS VM limit.
func detectVMProcesses(ctx context.Context, env []string) []vmProcess {
	out, err := sysexec.CommandContext(ctx, env, "pgrep", "-f", vmXPCProcessSubstr).Output()
	if err != nil {
		return nil
	}
	var procs []vmProcess
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		name, deleted, isTart := vmNameFromLsof(ctx, env, pid)
		if !isTart {
			continue // another app's VM — not ours to count or kill
		}
		procs = append(procs, vmProcess{PID: pid, VMName: name, Deleted: deleted})
	}
	return procs
}

// vmNameFromLsof resolves a VM process to its tart VM name by inspecting the
// ~/.tart/vms/<name>/ files it holds open. isTart is true only when such a file
// is found — the definitive signal that the process is a tart VM rather than
// some other app's. The disk image is authoritative; nvram is a fallback. A
// "(deleted)" marker means the image was removed out from under a still-running
// process — the signature of a crashed temp VM.
func vmNameFromLsof(ctx context.Context, env []string, pid int) (name string, deleted, isTart bool) {
	out, err := sysexec.CommandContext(ctx, env, "lsof", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", false, false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if !strings.Contains(line, ".tart/vms/") {
			continue
		}
		m := vmDiskPathRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		del := strings.Contains(line, "(deleted)")
		if strings.Contains(line, "disk.img") {
			return m[1], del, true // authoritative
		}
		if name == "" {
			name, deleted, isTart = m[1], del, true // fallback (e.g. nvram.bin)
		}
	}
	return name, deleted, isTart
}

// detectTartRunOwners returns the set of VM names that have a live `tart run`
// launcher process. A VM in this set is an owned sandbox, not an orphan.
func detectTartRunOwners(ctx context.Context, env []string) map[string]bool {
	out, err := sysexec.CommandContext(ctx, env, "ps", "-axo", "command=").Output()
	if err != nil {
		return nil
	}
	owners := map[string]bool{}
	for line := range strings.SplitSeq(string(out), "\n") {
		if name := tartRunVMName(strings.TrimSpace(line)); name != "" {
			owners[name] = true
		}
	}
	return owners
}

// tartRunVMName extracts the VM name (final positional arg) from a
// `tart run [flags] <name>` command line. Returns "" if the line is not a
// tart run invocation or carries no positional argument.
func tartRunVMName(cmdline string) string {
	fields := strings.Fields(cmdline)
	runIdx := -1
	for i := 0; i+1 < len(fields); i++ {
		if (fields[i] == "tart" || strings.HasSuffix(fields[i], "/tart")) && fields[i+1] == "run" {
			runIdx = i + 1
			break
		}
	}
	if runIdx < 0 {
		return ""
	}
	for i := len(fields) - 1; i > runIdx; i-- {
		if !strings.HasPrefix(fields[i], "-") {
			return fields[i]
		}
	}
	return ""
}

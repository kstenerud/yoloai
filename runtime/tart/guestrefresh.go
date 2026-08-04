// ABOUTME: Repairs a tart guest's stale VirtioFS page cache for host paths the host just
// ABOUTME: rewrote, by msync(MS_INVALIDATE)ing them in the guest until they match (DF175).
package tart

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// guestPython is the guest's system interpreter. The macOS base VM ships it with
// the Command Line Tools, which sandbox-setup.py already depends on.
const guestPython = "/usr/bin/python3"

// guestRefreshAttempts bounds how many separate guest invocations one refresh may
// cost. More than one is not defensiveness: a 20-byte file grown to 256 KiB was
// still wrong after FOUR invalidate passes inside a single guest process, and
// correct after one pass in a second, separate process. Whatever the guest settles
// between processes, an in-process loop does not reach it — so the host drives the
// loop and each invocation does exactly one pass.
const guestRefreshAttempts = 3

// guestRefreshSettle is how long to wait before the NEXT pass re-reads a path an
// earlier pass just invalidated. Without it the attempts above are worth far less
// than their count suggests: each pass invalidates and then re-hashes immediately,
// and the guest's revalidation is a **~1 s quantised tick**, so all three passes
// can land inside one interval, observe the same pre-tick state, and report STALE
// for a file that is about to be correct. Measured: 2 of 3 replacements over a
// path the guest had read reported STALE, and every one of them read the correct
// bytes seconds later with no further pass — the invalidation had worked and only
// the verdict was early (DF181).
//
// It therefore has to EXCEED the tick, not merely be non-zero: a sub-tick wait can
// fall inside the same interval and see exactly what the previous pass saw.
//
// This is on the ordinary repair path, not just a rare one — a genuinely stale file
// fails the first pass by construction — so it is latency every such `files put`
// pays. That is the trade: about a second, against a command that exits non-zero
// and tells the user to restart the sandbox.
const guestRefreshSettle = 1100 * time.Millisecond

// guestRefreshProgram runs in the guest with argv of (sha256, path) pairs. It
// prints one "<verdict>\t<path>" line per pair: OK when the guest's bytes already
// hash to what the host wrote, STALE when they still do not after one invalidate,
// ERR:<detail> when the file could not be examined at all.
//
// It uses ctypes rather than mmap.flush() because flush() issues msync(MS_SYNC),
// which pushes dirty pages OUT. What is needed is MS_INVALIDATE, which throws
// cached pages AWAY so the next read refetches from the host. The two are opposite
// directions and only one is reachable from the stdlib.
const guestRefreshProgram = `
import ctypes, ctypes.util, hashlib, os, sys

MS_INVALIDATE = 0x0002
PROT_READ = 0x01
MAP_SHARED = 0x0001
MAP_FAILED = ctypes.c_void_p(-1).value

libc = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)
libc.mmap.restype = ctypes.c_void_p
libc.mmap.argtypes = [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_int,
                      ctypes.c_int, ctypes.c_int, ctypes.c_longlong]
libc.munmap.argtypes = [ctypes.c_void_p, ctypes.c_size_t]
libc.msync.argtypes = [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_int]

def invalidate(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        size = os.fstat(fd).st_size
        if size == 0:
            return
        addr = libc.mmap(None, size, PROT_READ, MAP_SHARED, fd, 0)
        if addr == MAP_FAILED or addr is None:
            raise OSError(ctypes.get_errno(), "mmap")
        try:
            if libc.msync(ctypes.c_void_p(addr), size, MS_INVALIDATE) != 0:
                raise OSError(ctypes.get_errno(), "msync")
        finally:
            libc.munmap(ctypes.c_void_p(addr), size)
    finally:
        os.close(fd)

def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()

args = sys.argv[1:]
for i in range(0, len(args) - 1, 2):
    want, path = args[i], args[i + 1]
    try:
        if digest(path) == want:
            print("OK\t" + path)
            continue
        invalidate(path)
        print(("OK" if digest(path) == want else "STALE") + "\t" + path)
    except Exception as exc:
        print("ERR:%s\t%s" % (str(exc).replace("\t", " "), path))
`

// settleBeforeRetry waits for the guest's revalidation tick to advance. The seam
// exists so tests can assert that a wait was requested, and how long for, without
// paying it — but note what that can and cannot show: a fake guest proves the loop
// waits, never that the wait outlasts a real tick. Only hardware shows that
// (`design/research/macos-isolation-spike/df181_timing.sh`).
func (r *Runtime) settleBeforeRetry(d time.Duration) {
	if r.settle != nil {
		r.settle(d)
		return
	}
	time.Sleep(d)
}

// RefreshGuestFiles verifies each file in the guest and repairs the stale ones,
// returning those still mismatched. See runtime.GuestFileRefresher for why this
// is the backend's job and why the host supplies the digest.
func (r *Runtime) RefreshGuestFiles(ctx context.Context, name string, files []runtime.GuestFileDigest) ([]runtime.GuestFileDigest, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if !r.isRunning(ctx, name) {
		return nil, runtime.ErrNotRunning
	}

	sandboxDir := r.sandboxDirForName(name)
	type target struct {
		digest    runtime.GuestFileDigest
		guestPath string
	}
	pending := make([]target, 0, len(files))
	for _, f := range files {
		guestPath, err := guestPathFor(sandboxDir, f.HostPath)
		if err != nil {
			return nil, err
		}
		pending = append(pending, target{digest: f, guestPath: guestPath})
	}

	for attempt := 0; attempt < guestRefreshAttempts && len(pending) > 0; attempt++ {
		// Not before the first pass: a path the guest already agrees on must still
		// cost one check and no delay, which is the common case by far.
		if attempt > 0 {
			r.settleBeforeRetry(guestRefreshSettle)
		}
		argv := make([]string, 0, 3+2*len(pending))
		argv = append(argv, guestPython, "-c", guestRefreshProgram)
		for _, t := range pending {
			argv = append(argv, t.digest.SHA256, t.guestPath)
		}
		res, err := r.ExecRaw(ctx, name, argv, "")
		if err != nil {
			return nil, fmt.Errorf("refresh guest view of %d file(s): %w", len(pending), err)
		}

		verdicts := parseRefreshVerdicts(res.Stdout)
		next := pending[:0:0]
		for _, t := range pending {
			// A path with no verdict stays pending rather than counting as
			// repaired: silence is the one answer that must never read as
			// success here, since the whole defect is that a wrong answer
			// arrives without complaint.
			if verdicts[t.guestPath] == "OK" {
				continue
			}
			next = append(next, t)
		}
		pending = next
	}

	stale := make([]runtime.GuestFileDigest, 0, len(pending))
	for _, t := range pending {
		stale = append(stale, t.digest)
	}
	return stale, nil
}

// parseRefreshVerdicts maps guest path to verdict. Paths under the share contain
// spaces ("/Volumes/My Shared Files/..."), so the verdict comes first and the
// separator is a tab — splitting on whitespace would truncate every path.
func parseRefreshVerdicts(stdout string) map[string]string {
	verdicts := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		verdict, path, found := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !found || path == "" {
			continue
		}
		verdicts[path] = verdict
	}
	return verdicts
}

// guestPathFor translates a host path inside the sandbox directory to the path
// the guest sees. Only the two shared tiers reach the guest at all; the host-only
// part of the sandbox directory has no --dir and so has no guest path (DF136).
func guestPathFor(sandboxDir, hostPath string) (string, error) {
	for _, tier := range []string{config.ReadWriteTierName, config.ReadOnlyTierName} {
		rel, err := filepath.Rel(filepath.Join(sandboxDir, tier), hostPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.Join(sharedDirVMPath, tier, rel), nil
	}
	return "", fmt.Errorf("path is not in a tier shared with the guest: %s", hostPath)
}

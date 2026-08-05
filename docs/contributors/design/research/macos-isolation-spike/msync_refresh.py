#!/usr/bin/env python3
# ABOUTME: Guest-side probe: drop a macOS guest's stale VirtioFS page cache for named paths by
# ABOUTME: msync(MS_INVALIDATE)ing a read-only mapping of each, then report what the guest reads.
#
# Runs INSIDE a tart (macOS) guest. Stdlib only. Takes paths as argv, prints one line per path:
#
#     <sha256> <size> <path>
#
# DF175: a macOS guest's VirtioFS client serves stale file DATA after a host-side write, on a path
# the guest has already read. Attributes (size, mtime) refresh on the ~1s tick while the bytes do
# not, so nothing the guest can stat will reveal it. Independently reported by h1d3mun3/augur#135,
# which traces it to a guest vnode that is never reclaimed and establishes this lever.
#
# WHY ctypes AND NOT mmap.flush(). CPython's mmap.flush() issues msync(MS_SYNC) — it pushes dirty
# pages OUT. What is needed is MS_INVALIDATE, which throws cached pages AWAY so the next read
# refetches from the host. The two are opposite directions and only one of them is reachable from
# the stdlib, so the mapping is made through libc directly and the address is ours to pass.
#
# WHY THE HOST'S HASH IS AN ARGUMENT. One msync is not always enough: a 20-byte file grown to
# 256 KiB read back with the RIGHT size and the WRONG bytes after one pass, and needed a second.
# Neither size nor hash-stability is a safe stop condition — the first pass produced a size that
# already matched the host and a hash that looked settled. Only the host knows what the bytes
# should be, so it says, and the guest retries until it agrees or gives up loudly. That makes
# this verify-and-repair: a silent wrong answer becomes an explicit MISMATCH.

import ctypes
import ctypes.util
import hashlib
import os
import sys

MS_INVALIDATE = 0x0002  # <sys/mman.h>, Darwin
PROT_READ = 0x01
MAP_SHARED = 0x0001
MAP_FAILED = ctypes.c_void_p(-1).value
MAX_PASSES = 4

_libc = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)
_libc.mmap.restype = ctypes.c_void_p
_libc.mmap.argtypes = [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_int,
                       ctypes.c_int, ctypes.c_int, ctypes.c_longlong]
_libc.munmap.restype = ctypes.c_int
_libc.munmap.argtypes = [ctypes.c_void_p, ctypes.c_size_t]
_libc.msync.restype = ctypes.c_int
_libc.msync.argtypes = [ctypes.c_void_p, ctypes.c_size_t, ctypes.c_int]


def invalidate(path):
    """Drop the guest's cached pages for path. Returns None, or an error string."""
    fd = os.open(path, os.O_RDONLY)
    try:
        size = os.fstat(fd).st_size
        if size == 0:
            return None  # nothing mapped, nothing to drop
        addr = _libc.mmap(None, size, PROT_READ, MAP_SHARED, fd, 0)
        if addr == MAP_FAILED or addr is None:
            return "mmap: %s" % os.strerror(ctypes.get_errno())
        try:
            if _libc.msync(ctypes.c_void_p(addr), size, MS_INVALIDATE) != 0:
                return "msync: %s" % os.strerror(ctypes.get_errno())
        finally:
            _libc.munmap(ctypes.c_void_p(addr), size)
        return None
    finally:
        os.close(fd)


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


def refresh(path, want):
    """Invalidate and re-read until the guest's bytes hash to want. Returns (verdict, passes)."""
    got = digest(path)
    if got == want:
        return "ALREADY-FRESH", 0
    for attempt in range(1, MAX_PASSES + 1):
        err = invalidate(path)
        if err:
            return "ERROR:" + err, attempt
        got = digest(path)
        if got == want:
            return "REFRESHED", attempt
    return "MISMATCH:" + got, MAX_PASSES


def main():
    args = sys.argv[1:]
    if not args or len(args) % 2:
        sys.stderr.write("usage: msync_refresh.py <sha256> <path> [<sha256> <path> ...]\n")
        return 2
    rc = 0
    for i in range(0, len(args), 2):
        want, path = args[i], args[i + 1]
        try:
            verdict, passes = refresh(path, want)
        except OSError as exc:
            verdict, passes = "ERROR:" + str(exc), 0
        if not verdict.startswith(("REFRESHED", "ALREADY-FRESH")):
            rc = 1
        print("%s passes=%d %s" % (verdict, passes, path))
    return rc


if __name__ == "__main__":
    sys.exit(main())

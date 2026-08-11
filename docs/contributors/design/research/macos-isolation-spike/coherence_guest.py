#!/usr/bin/env python3
# ABOUTME: Guest half of the macOS host->guest coherence harness. For each host action it
# ABOUTME: records, separately, when read()/stat()/readdir() each first observe the change.
#
# Runs inside a tart (macOS) or apple `container` (Linux) guest. Stdlib only, py3.8+.
#
# WHY THREE PREDICATES. An earlier version bound ONE predicate to each shape — content for
# overwrite, stat for delete, listdir for readdir — and reported "NEVER" for three tart
# shapes. That was wrong, and wrong in the direction that flatters the harness: the changes
# were arriving in about a second, but via st_size and readdir, while read() served stale
# bytes and stat() kept reporting a deleted file. Binding one predicate per shape measures
# `action x predicate` and reports it as a property of the action. So the predicate is now
# an explicit axis: every applicable one is polled for every shape, and each reports its own
# first-observation time. "-" means that predicate never observed it inside the deadline.
#
# The guest also reports its OWN elapsed time, measured from the start of the round — i.e.
# from BEFORE the host acts. The host cannot derive this: its clock starts when it acts, so
# any settle delay it inserts is silently subtracted from what it reports. On these shares
# the underlying revalidation is a ~1s tick anchored to the guest's first lookup, which the
# host-side number hides completely.

import os
import sys
import time
from typing import Callable

SHAPES = (
    "create",
    "overwrite_inplace",
    "overwrite_rename",
    "mkdir",
    "symlink",
    "delete",
    "append",
)


def _read(path: str) -> str:
    try:
        with open(path) as fh:
            return fh.read().strip()
    except OSError:
        return ""


def _size(path: str) -> int:
    try:
        return os.stat(path).st_size
    except OSError:
        return -1


def predicates(shape: str, d: str, i: int) -> dict[str, Callable[[], bool]]:
    """Return {name: callable} — every way a guest could notice this host action."""
    if shape == "create":
        p = os.path.join(d, "GO_%d" % i)
        return {"stat": lambda: os.path.exists(p),
                "readdir": lambda: ("GO_%d" % i) in _listdir(d)}
    if shape in ("overwrite_inplace", "overwrite_rename"):
        p = os.path.join(d, "BAR_%d" % i)
        return {"read": lambda: _read(p) == "go",
                "stat": lambda: _size(p) == 2}
    if shape == "mkdir":
        p = os.path.join(d, "DIR_%d" % i)
        return {"stat": lambda: os.path.isdir(p),
                "readdir": lambda: ("DIR_%d" % i) in _listdir(d)}
    if shape == "symlink":
        p = os.path.join(d, "LNK_%d" % i)
        return {"stat": lambda: os.path.islink(p),
                "readdir": lambda: ("LNK_%d" % i) in _listdir(d)}
    if shape == "delete":
        p = os.path.join(d, "DEL_%d" % i)
        return {"stat": lambda: not os.path.exists(p),
                "readdir": lambda: ("DEL_%d" % i) not in _listdir(d)}
    if shape == "append":
        p = os.path.join(d, "APP_%d" % i)
        return {"read": lambda: _read(p).endswith("go"),
                "stat": lambda: _size(p) > 0}
    raise SystemExit("unknown shape %r" % shape)


def _listdir(d: str) -> list[str]:
    try:
        return os.listdir(d)
    except OSError:
        return []


def main() -> None:
    d = sys.argv[1]
    poll = float(sys.argv[2]) if len(sys.argv) > 2 else 0.001
    deadline_s = float(sys.argv[3]) if len(sys.argv) > 3 else 20.0

    plan = [ln.strip() for ln in _read(os.path.join(d, "PLAN")).splitlines() if ln.strip()]
    started = os.path.join(d, ".STARTED.part")
    with open(started, "w") as fh:
        fh.write("guest up: %s rounds=%d poll=%.4f\n" % (os.uname()[0], len(plan), poll))
    os.replace(started, os.path.join(d, "STARTED"))  # atomic: the host polls for this

    for i, shape in enumerate(plan):
        preds = predicates(shape, d, i)
        first: dict[str, float | None] = {name: None for name in preds}
        t0 = time.time()
        while time.time() - t0 < deadline_s:
            for name, fn in preds.items():
                if first[name] is None and fn():
                    first[name] = (time.time() - t0) * 1000.0
            if all(v is not None for v in first.values()):
                break
            time.sleep(poll)
        body = " ".join("%s=%s" % (n, "-" if v is None else "%.1f" % v)
                        for n, v in sorted(first.items()))
        part = os.path.join(d, "ACK_%d.part" % i)
        with open(part, "w") as fh:
            fh.write(body + "\n")
        os.replace(part, os.path.join(d, "ACK_%d" % i))

    with open(os.path.join(d, "DONE"), "w") as fh:
        fh.write("done\n")


if __name__ == "__main__":
    main()

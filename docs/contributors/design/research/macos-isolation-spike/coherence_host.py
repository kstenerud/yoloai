#!/usr/bin/env python3
# ABOUTME: Host half of the macOS host->guest file-coherence harness — performs each
# ABOUTME: barrier-shape action and times how long the guest takes to observe it.
#
# Settles T1/T2/T3: which host->guest signalling shapes reach a tart or apple guest, how
# fast, and which never arrive. Needs no root. Usage:
#
#   python3 coherence_host.py --backend tart  --sandbox mac-research-tart  [--reps 5]
#   python3 coherence_host.py --backend apple --sandbox mac-research-apple [--poll 0.001]
#
# Both ends measure: the host times the full round trip on ONE clock (write -> guest sees
# -> guest ACK visible to host), and the guest reports its own elapsed time. Round trip is
# the honest number; anything at or below the guest poll interval is unresolved, not fast.

import argparse
import glob
import os
import shutil
import subprocess
import sys
import time
import uuid

from coherence_guest import SHAPES  # single source of truth for the shape list

GUEST_DIRS = {
    # sandbox-relative host path -> path the guest sees
    "tart": "/Volumes/My Shared Files/rw/logs",
    "apple": "/yoloai/logs",
}
LIB = os.path.expanduser("~/.yoloai/library/sandboxes")


def guest_exec(backend, sandbox, argv, background=False, log=None):
    """Run argv inside the guest. Bypasses `yoloai exec` so a 'done' agent still works."""
    if backend == "tart":
        cmd = ["tart", "exec", "yoloai-cli-" + sandbox] + argv
    elif backend == "apple":
        cmd = ["container", "exec", "yoloai-cli-" + sandbox] + argv
    else:
        raise SystemExit("unknown backend " + backend)
    if background:
        # Never discard the guest's stderr: a guest that dies on startup is otherwise
        # indistinguishable from a guest that is merely slow, and both look like
        # "STARTED never appeared".
        fh = open(log, "wb")
        return subprocess.Popen(cmd, stdout=fh, stderr=subprocess.STDOUT)
    return subprocess.run(cmd, capture_output=True, text=True)


def host_action(shape, d, i):
    """Perform the host side of one round. Mirrors satisfied() in coherence_guest.py."""
    if shape == "create":
        open(os.path.join(d, "GO_%d" % i), "w").write("go")
    elif shape == "overwrite_inplace":
        # truncate + write: same inode, new length. The shape measured to never propagate.
        with open(os.path.join(d, "BAR_%d" % i), "w") as fh:
            fh.write("go")
    elif shape == "overwrite_rename":
        # write temp + rename: new inode. THE open question (T1).
        tmp = os.path.join(d, ".tmp_bar_%d" % i)
        with open(tmp, "w") as fh:
            fh.write("go")
        os.replace(tmp, os.path.join(d, "BAR_%d" % i))
    elif shape == "mkdir":
        os.mkdir(os.path.join(d, "DIR_%d" % i))
    elif shape == "symlink":
        os.symlink("/nonexistent-target", os.path.join(d, "LNK_%d" % i))
    elif shape == "delete":
        os.remove(os.path.join(d, "DEL_%d" % i))
    elif shape == "append":
        with open(os.path.join(d, "APP_%d" % i), "a") as fh:
            fh.write("go")
    else:
        raise SystemExit("unknown shape " + shape)


def precondition(shape, d, i):
    """Files that must exist BEFORE the guest starts, so no negative cache is involved."""
    if shape in ("overwrite_inplace", "overwrite_rename"):
        open(os.path.join(d, "BAR_%d" % i), "w").write("wait")
    elif shape == "delete":
        open(os.path.join(d, "DEL_%d" % i), "w").write("here")
    elif shape == "append":
        open(os.path.join(d, "APP_%d" % i), "w").write("")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--backend", required=True, choices=sorted(GUEST_DIRS))
    ap.add_argument("--sandbox", required=True)
    ap.add_argument("--reps", type=int, default=5, help="rounds per shape")
    ap.add_argument("--poll", type=float, default=0.001, help="guest poll interval (s)")
    ap.add_argument("--deadline", type=float, default=20.0, help="per-round give-up (s)")
    ap.add_argument("--shapes", default=",".join(SHAPES))
    ap.add_argument("--settle", type=float, default=0.2,
                    help="pause before each host action; SUBTRACTED from host rtt, so sweep it")
    args = ap.parse_args()

    shapes = [s for s in args.shapes.split(",") if s]
    # A FRESH directory name per run, never reused. Deleting and recreating one path can
    # leave a tart guest resolving the old, deleted inode — which presents as the guest
    # failing to start on a script that is plainly there on the host.
    run_id = uuid.uuid4().hex[:8]
    logs_host = os.path.join(LIB, args.sandbox, "rw", "logs")
    host_dir = os.path.join(logs_host, "coh-" + run_id)
    guest_dir = GUEST_DIRS[args.backend] + "/coh-" + run_id

    # A poller left over from an aborted run keeps spinning; clear it before starting.
    guest_exec(args.backend, args.sandbox, ["pkill", "-f", "coherence_guest.py"])
    for old in glob.glob(os.path.join(logs_host, "coh-*")):
        shutil.rmtree(old, ignore_errors=True)
    os.makedirs(host_dir)

    plan = [s for s in shapes for _ in range(args.reps)]
    with open(os.path.join(host_dir, "PLAN"), "w") as fh:
        fh.write("\n".join(plan) + "\n")
    for i, shape in enumerate(plan):
        precondition(shape, host_dir, i)

    # Ship the guest script through the share and run it from there.
    shutil.copy(os.path.join(os.path.dirname(os.path.abspath(__file__)), "coherence_guest.py"),
                os.path.join(host_dir, "coherence_guest.py"))

    guest_log = os.path.join(host_dir, "guest-stderr.log")
    proc = guest_exec(args.backend, args.sandbox,
                      ["python3", guest_dir + "/coherence_guest.py", guest_dir,
                       str(args.poll), str(args.deadline)],
                      background=True, log=guest_log)

    t0 = time.time()
    while not os.path.exists(os.path.join(host_dir, "STARTED")):
        if time.time() - t0 > 120:
            proc.kill()
            err = open(guest_log).read().strip() if os.path.exists(guest_log) else ""
            sys.exit("FAIL: guest never started (STARTED never appeared)\n  guest output: %s"
                     % (err or "<empty — the exec itself produced nothing>"))
        time.sleep(0.005)
    print("guest: " + open(os.path.join(host_dir, "STARTED")).read().strip())
    print("backend=%s sandbox=%s reps=%d guest_poll=%.4fs deadline=%.0fs"
          % (args.backend, args.sandbox, args.reps, args.poll, args.deadline))
    print()

    results = {s: [] for s in shapes}
    for i, shape in enumerate(plan):
        time.sleep(args.settle)  # NOTE: host rtt = guest_elapsed - settle. Sweep to expose a tick.
        t = time.time()
        host_action(shape, host_dir, i)
        ack = os.path.join(host_dir, "ACK_%d" % i)
        while not os.path.exists(ack):
            if time.time() - t > args.deadline + 15:
                proc.kill()
                sys.exit("FAIL: guest stopped ACKing at round %d (%s)" % (i, shape))
            time.sleep(0.001)
        rtt = (time.time() - t) * 1000.0
        # The ACK's existence and its contents are two separate events; on a share the
        # host can win the race to the empty file. Wait for a parseable body.
        while True:
            body = open(ack).read()
            if body.endswith("\n") and body.split():  # complete record only
                break
            if time.time() - t > args.deadline + 20:
                proc.kill()
                sys.exit("FAIL: ACK_%d never became readable (%s)" % (i, shape))
            time.sleep(0.001)
        per = {}
        for tok in body.split():
            name, _, val = tok.partition("=")
            per[name] = None if val == "-" else float(val)
        results[shape].append((per, rtt))

    proc.wait(timeout=30)

    hdr = "%-20s %-8s %10s %10s %10s" % ("SHAPE", "rounds", "read_ms", "stat_ms", "readdir_ms")
    print(hdr)
    print("-" * len(hdr))
    for sh in shapes:
        rows = results[sh]
        # The real invariant. The previous version asserted that three exhaustive filters
        # over `rows` summed to len(rows) — true for every possible input, including a run
        # that silently dropped rounds. This one can actually fail.
        assert len(rows) == args.reps, "shape %s: %d rounds recorded, expected %d" % (
            sh, len(rows), args.reps)
        cells = []
        for name in ("read", "stat", "readdir"):
            vals = [p[name] for p, _ in rows if name in p]
            if not vals:
                cells.append("n/a")
            elif all(v is None for v in vals):
                cells.append("NEVER")
            else:
                got = sorted(v for v in vals if v is not None)
                miss = len(vals) - len(got)
                cells.append("%.0f%s" % (got[len(got) // 2], "*" if miss else ""))
        print("%-20s %-8d %10s %10s %10s" % (sh, len(rows), cells[0], cells[1], cells[2]))
    print()
    print("Times are GUEST-side medians, measured from the start of the round (before the host")
    print("acted) — so they include the --settle %.2fs pause and are NOT host-observed latency."
          % args.settle)
    print("A host-side round trip would report (guest_ms - settle_ms), which hides any tick")
    print("anchored to the guest's first lookup. Sweep --settle to see whether a figure moves.")
    print('"NEVER" means that PREDICATE never observed the change within %.0fs — not that the'
          % args.deadline)
    print("change failed to arrive; check the other columns before concluding anything.")
    print('"*" marks a cell where some rounds never observed it.')


if __name__ == "__main__":
    main()

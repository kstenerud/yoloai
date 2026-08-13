#!/usr/bin/env python3
# ABOUTME: C3 of the mac-channel round — is `--network-none` actually enforced on
# ABOUTME: the apple, tart and seatbelt backends, measured through yoloAI's own
# ABOUTME: launch path rather than through a hand-built container (DF198, A37).

"""C3: does `--network-none` hold on every backend, as the shipped help claims?

`internal/cli/helpcmd/help/security.md:74` says `--network-none` "holds on every
backend" and `design/netpolicy.md` calls it "a hard boundary on every backend". The
static reading says otherwise: `runtime/apple/apple.go:235` names NetworkMode only in
a comment about the "isolated" case and never tests "none", and `runtime/tart/`
contains no NetworkMode reference at all.

**A37 governs this item.** The claim is about the product, so every sandbox here is
created by the `yoloai` binary with the flags a user would type. Nothing is built by
hand.

The instrument's boundary, stated because it is not free: the apple/tart base images
on this host predate the current tree, and the run stamps the build-inputs checksum so
`Setup` reuses them instead of rebuilding (an unrelated `container build` failure on
this host blocks a fresh build). That is sound for *this* question — whether the flag
reaches the backend is decided by host-side Go in `runtime/`, not by image contents —
and it would not be sound for a question about what runs inside the sandbox.
"""

import os
import subprocess
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "..", "..", "scripts"))
from research_harness_v2 import Harness, Measurement  # noqa: E402

YOLOAI = "/tmp/yc1/yoloai"
WORKDIR = "/tmp/yc1/wd"
DEST = "http://1.1.1.1"

ENV = dict(os.environ, PATH="/usr/local/bin:/opt/homebrew/bin:" + os.environ.get("PATH", ""))


def sh(*args: str, timeout: int = 900) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout, env=ENV)


def destroy(name: str) -> None:
    # By name only, never --all: this host carries unrelated sandboxes belonging to
    # the owner, and a research run must not be able to reach them.
    sh(YOLOAI, "destroy", "--abandon-unapplied", name)


def create(name: str, backend: str, none: bool, os_flag: str | None = None) -> subprocess.CompletedProcess[str]:
    destroy(name)
    args = [YOLOAI, "new", name, WORKDIR, "--backend", backend, "--agent", "test"]
    if os_flag:
        args += ["--os", os_flag]
    if none:
        args.append("--network-none")
    return sh(*args)


def reaches(name: str) -> bool:
    """True only if a curl inside the sandbox got an HTTP status back."""
    p = sh(YOLOAI, "exec", name, "--", "sh", "-c",
           f'curl -s -m 8 -o /dev/null -w "%{{http_code}}" {DEST} 2>/dev/null || echo CURLFAIL')
    out = p.stdout.strip().splitlines()
    tail = out[-1].strip() if out else ""
    return tail.isdigit() and tail != "000"


BACKENDS = [
    ("apple", None),
    ("tart", "mac"),
    ("seatbelt", None),
]


def main() -> int:
    h = Harness("C3", "is --network-none enforced on apple, tart and seatbelt, through yoloAI's own launch path?")

    h.require("the yoloai binary under test exists", os.path.exists(YOLOAI),
              detail="built from the working tree at the round's HEAD")
    h.require("a workdir exists", os.path.isdir(WORKDIR))

    arm: dict[str, str | None] = {"name": None}
    probe = h.probe("the sandbox reaches an external address",
                    lambda: reaches(str(arm["name"])))

    results: dict[str, Measurement | None] = {}
    for backend, os_flag in BACKENDS:
        open_name = f"c3-{backend}-open"
        none_name = f"c3-{backend}-none"

        # Baseline: the SAME probe, on the same backend, with the mechanism absent.
        # A backend whose sandbox simply cannot reach the network would otherwise
        # pass a containment claim for free (A22).
        c_open = create(open_name, backend, none=False, os_flag=os_flag)
        started_open = "created" in (c_open.stdout + c_open.stderr).lower()
        h.control(f"{backend}: a no-flag sandbox starts", started_open,
                  detail=(c_open.stdout + c_open.stderr).strip().splitlines()[-1][:120] if not started_open else "")
        if not started_open:
            h.void_arm(backend, f"{backend}: the baseline sandbox did not start, so nothing on this backend is measurable")
            destroy(open_name)
            continue

        arm["name"] = open_name
        if backend == BACKENDS[0][0]:
            probe.baseline(want=True,
                           detail=f"{backend}, no --network-none — the positive control the claim below inverts")
        else:
            h.control(f"{backend}: the no-flag sandbox reaches out", probe.fn(),
                      detail="per-backend positive control; the probe's baseline is taken once, on the first backend")
        destroy(open_name)

        # The mechanism.
        c_none = create(none_name, backend, none=True, os_flag=os_flag)
        blob = (c_none.stdout + c_none.stderr)
        started_none = "created" in blob.lower()
        h.measure(f"{backend}: --network-none sandbox starts", started_none,
                  detail="" if started_none else blob.strip().splitlines()[-1][:160])
        if not started_none:
            results[backend] = None
            h.measure(f"{backend}: --network-none outcome", "REFUSED-TO-START",
                      detail="not a silent pass and not enforcement either — see the finding")
            destroy(none_name)
            continue

        arm["name"] = none_name
        results[backend] = probe.sample(f"{backend}: under --network-none",
                                        detail="same probe, same backend, same destination")
        destroy(none_name)

    for backend, m in results.items():
        if m is not None:
            h.expect(f"--network-none removes egress on {backend}", m, want=False)

    h.not_tried(
        "containerd, docker and podman. `runtime/containerd/lifecycle.go:527` admits the same gap in "
        "its own comment and is unmeasured here; docker and podman pass the mode to the engine and "
        "are read, not run",
        "IPv6 specifically. The apple arm's guest holds a global v6 address as well as a v4 one, and "
        "the probe only dials a v4 literal — so the v6 half is confirmed present and never exercised",
        "whether the sandbox can be reached FROM outside, which is the other half of what a user "
        "reading 'no network at all' would assume",
        "the seatbelt failure's mechanism. This run records only that the sandbox does not start; "
        "which SBPL operation class breaks it is C4's subject",
        "a fresh base image. Both VM backends reuse an image predating this tree — see the "
        "instrument's boundary in the module docstring",
        "n. One host, one run per arm",
    )

    ok = h.report()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

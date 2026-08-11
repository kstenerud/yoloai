#!/usr/bin/env python3
# ABOUTME: R13 — does the netdev key work inside rootless podman's own netns, and does
# ABOUTME: it survive the one backend that genuinely recycles veth names? R11 answered
# ABOUTME: the inheritance question on hand-built veths; this is the real thing.

"""R13: the netdev key on the backend that recycles names.

Every run behind the netdev recommendation (R10, R11, R12) is docker or a hand-built
veth pair. Rootless podman is the backend those cannot speak for, and it is the one
that matters most for the lifecycle question: `k3`/`k3b` measured that it hands out
`veth0` every time, and that a new sandbox inherits a departed one's name **while a
sibling keeps its own**.

R11 found that a netdev chain does *not* transfer to a new device under the old name.
That was measured on devices I created myself, precisely because docker's names never
collide — so it establishes the mechanism and not the backend. If it holds here, the
inheritance hazard is closed on the only backend that can produce it.

**A second container is kept running throughout.** Rootless podman's netns is torn down
with its last sandbox, which would remove the chain for an unrelated reason and turn
the interesting arm into a tautology. The keeper holds the netns open so the question
under test is name reuse, not netns teardown.

Every probe is TCP. ICMP does not traverse this backend's stack at all, and three runs
in this workstream recorded a reassuring "blocked" that was free for exactly that
reason.

Run it as: `python3 r13_rootless_netdev.py` (no sudo — that is the point)
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r13_net"
BOX_A = "yb_r13_a"
KEEPER = "yb_r13_keeper"
BOX_C = "yb_r13_c"
TABLE = "yb_r13"
SUBNET = "10.208.0.0/24"
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["podman", "unshare", "--rootless-netns", "nft", "delete", "table", "netdev", TABLE])
    # One at a time, and `-f` on the network. A single `podman rm -f A B C` where A was
    # already removed mid-run leaves B and C behind, and the next run then dies on
    # "network already exists" — which reads like a podman problem rather than a
    # cleanup bug. It cost this run one void execution.
    for box in (BOX_A, KEEPER, BOX_C):
        _quiet(["podman", "rm", "-f", box])
    _quiet(["podman", "network", "rm", "-f", NET])


def in_netns(h: Harness, argv: list[str], *, stdin: str | None = None,
             check: bool = True) -> subprocess.CompletedProcess[str]:
    """Run a command inside rootless podman's network namespace.

    No sudo anywhere: the user owns this namespace, which is why this backend needs no
    privileged path at all. That is a property worth stating rather than assuming.
    """
    return h.run(["podman", "unshare", "--rootless-netns", *argv], stdin=stdin, check=check)


def veth_of(h: Harness, box: str) -> str:
    """The rootless-netns-side veth for a container, found by ifindex rather than by
    name. `r5` hardcoded `podman1` while the network was on `podman2`, read a zero
    counter, and it looked exactly like the enforcement failing."""
    iflink = h.run(["podman", "exec", box, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    links = in_netns(h, ["ip", "-o", "link"]).stdout
    for line in links.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no interface with ifindex {iflink} in the rootless netns")


def reaches(h: Harness, box: str, host: str) -> bool:
    return (
        h.run(["podman", "exec", box, "curl", "-s", "-m", "5", "-o", "/dev/null",
               f"http://{host}/"], check=False).returncode
        == 0
    )


def counter(h: Harness) -> int:
    out = in_netns(h, ["nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'packets (\d+).*comment "nd-deny"', out)
    return int(m.group(1)) if m else 0


def chain_exists(h: Harness) -> bool:
    return in_netns(h, ["nft", "list", "table", "netdev", TABLE], check=False).returncode == 0


def load_chain(h: Harness, veth: str) -> None:
    in_netns(
        h,
        ["nft", "-f", "-"],
        stdin=f"""
table netdev {TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        ip daddr {DENIED} counter drop comment "nd-deny"
    }}
}}
""",
    )


def main() -> int:
    h = Harness("R13", "Does the netdev key work unprivileged in rootless podman's netns, "
                       "and does it resist that backend's veth-name reuse?")
    try:
        cleanup()
        h.run(["podman", "network", "create", "--subnet", SUBNET, NET])
        for box in (KEEPER, BOX_A):
            h.run(["podman", "run", "-d", "--name", box, "--network", NET,
                   "alpine", "sleep", "600"])
            h.run(["podman", "exec", box, "apk", "add", "--no-cache", "curl"])

        veth_a = veth_of(h, BOX_A)
        veth_keeper = veth_of(h, KEEPER)
        h.require("A and the keeper have distinct veths", veth_a != veth_keeper,
                  f"A={veth_a} keeper={veth_keeper}")

        h.control("A reaches the denied host with no policy", reaches(h, BOX_A, DENIED))
        h.control("the keeper reaches it too", reaches(h, KEEPER, DENIED))

        load_chain(h, veth_a)
        a_blocked = h.measure("the netdev chain blocks A, installed with no sudo",
                              not reaches(h, BOX_A, DENIED))
        h.control("the netdev rule fired", counter(h) > 0, f"{counter(h)} packets")
        h.control("the keeper is untouched", reaches(h, KEEPER, DENIED),
                  "the discrimination control: the chain is bound to A's veth only")
        h.expect("a netdev chain enforces inside the rootless netns, unprivileged",
                 a_blocked, want=True)

        # -- A departs; the keeper holds the netns open ------------------------
        h.run(["podman", "rm", "-f", BOX_A])
        h.measure("the chain outlives the sandbox it was installed for", chain_exists(h))

        # -- C arrives and takes A's name -------------------------------------
        h.run(["podman", "run", "-d", "--name", BOX_C, "--network", NET,
               "alpine", "sleep", "600"])
        h.run(["podman", "exec", BOX_C, "apk", "add", "--no-cache", "curl"])
        veth_c = veth_of(h, BOX_C)
        reused = h.measure("C took A's veth name", veth_c == veth_a,
                           f"A was {veth_a}, C is {veth_c}")
        # This is the precondition, not the finding. If C got a fresh name the
        # inheritance question was never actually asked, and saying so beats
        # reporting a pass that measured nothing.
        h.expect("this backend recycles the name, so the hazard is reachable at all",
                 reused, want=True)

        before = counter(h)
        c_reach = reaches(h, BOX_C, DENIED)
        after = counter(h)
        # Keep the bool AND the Measurement. v1 wants the object in `expect` and the
        # value everywhere else, and the two mistakes are symmetrical: pass the object
        # to a conditional and it is always truthy (r11), pass the bool to `expect` and
        # it refuses. mypy caught this one statically and the harness caught it again
        # at run time, which is the belt the whole library exists to be.
        inherited_val = (not c_reach) and after > before
        inherited = h.measure(
            "C inherits A's policy through the reused name",
            inherited_val,
            f"reachable={c_reach}, counter {before} -> {after}. Both are required: a "
            "blocked probe with a frozen counter would mean something else stopped it",
        )
        h.expect("a departed sandbox's netdev chain does NOT capture its successor",
                 inherited, want=False)

        h.not_tried(
            "netns TEARDOWN. The keeper deliberately holds the netns open, so this says "
            "nothing about the last-sandbox case where the whole namespace goes — which "
            "the plan already records as needing reinstall per bring-up",
            "whether C is ENFORCED. It is not captured by A's chain, which is the question "
            "asked; installing C's own chain and checking it works is a different arm and "
            "was not run",
            "the ifindex, again. C reuses the NAME; whether nft bound to the old index is "
            "the mechanism behind the result and nothing here reads it",
            "pasta vs slirp4netns. This host's rootless podman put a bridge and veth in the "
            "netns for a named network; the default no-network path had only tap0, and that "
            "shape is untested",
            "a hostile guest. The agent cannot enter this netns (CAP_SYS_ADMIN absent, path "
            "invisible) per r2, but nothing here re-tests that against a netdev chain",
            "IPv6, UDP, allowlist sets, and revocation — all measured on docker only",
            "concurrency, and more than one departing sandbox",
        )
        return 0 if h.report() else 1
    finally:
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)

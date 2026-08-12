#!/usr/bin/env python3
# ABOUTME: V6b — can a foreign netdev chain on the same device shadow ours, so our deny is
# ABOUTME: loaded, correct-looking and never reached? The Linux analogue of the macOS
# ABOUTME: anchor-eval finding, and the one fault neither subscription covers.

"""V6b: the fault that is invisible to both subscriptions and to an inventory check.

V1 and V1b between them cover every way enforcement stops that this round could construct:
ruleset faults arrive on the nftables group, device faults on the link group, both
attributably. **Neither covers a chain that is present, correct, attached — and never
reached**, because the thing that shadows it is a change to *someone else's* table. An event
does arrive; it names a table we have no reason to care about.

That fault is not hypothetical, it is the sharpest result in the macOS half of this
workstream. `pf-anchor-eval.txt`: a loaded anchor pf never evaluates because the main
ruleset lost its `anchor "com.apple/*"` line, **all three of D132's health checks report
healthy**, and a denied destination answers 301. R8's bounds name the Linux version as
untested: *"Arm 1's inert case is a missing kernel module, not a rule being shadowed;
whether the two produce the same counter signature is an assumption."*

The Linux shape to test is a second netdev ingress chain on the **same device** at a
**lower priority number** — evaluated first — containing an `accept`. Two outcomes, and they
lead to different designs:

* **Netfilter semantics hold**: a base chain's `accept` is that chain's verdict, evaluation
  continues to the next base chain, and only a terminal `drop` stops traversal. Then our deny
  is still reached and this whole class is closed on Linux by construction — a much stronger
  position than macOS's.
* **The accept is terminal across chains**: then a foreign chain silently disables us, no
  event names our table, our counters read zero, and `rule 0 + veth rx climbing` is the only
  signature — which is exactly the poll V1b just made unnecessary, coming back for one case.

Also measured, because it is the same question from the attacker's side: whether a foreign
`drop` at higher priority masks our counter. If it does, our counters undercount and a
counter-based detector reads the wrong thing even when enforcement is intact.

**The ground truth is reachability, not the counter.** A counter is not attribution (A34),
so each arm asserts what the guest can actually reach and uses counters only to say which
rule decided it.

Run it as: `sudo -v && python3 v6b_foreign_chain_shadowing.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

NS = "yb_v6b_ns"
VETH = "yb_v6b_v"
PEER = "yb_v6b_p"
OURS = "yb_v6b_ours"
FOREIGN = "yb_v6b_foreign"
HOST_IP = "10.221.0.1"
NS_IP = "10.221.0.2"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    for t in (OURS, FOREIGN):
        _quiet(["sudo", "nft", "delete", "table", "netdev", t])
    _quiet(["sudo", "ip", "link", "del", VETH])
    _quiet(["sudo", "ip", "netns", "del", NS])


def build_link(h: Harness) -> None:
    h.run(["sudo", "ip", "link", "add", VETH, "type", "veth", "peer", "name", PEER])
    h.run(["sudo", "ip", "link", "set", PEER, "netns", NS])
    h.run(["sudo", "ip", "addr", "add", f"{HOST_IP}/24", "dev", VETH])
    h.run(["sudo", "ip", "link", "set", VETH, "up"])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "addr", "add", f"{NS_IP}/24", "dev", PEER])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "link", "set", PEER, "up"])


def reaches(h: Harness) -> bool:
    return (
        h.run(
            ["sudo", "ip", "netns", "exec", NS, "ping", "-c", "2", "-W", "2", HOST_IP],
            check=False,
        ).returncode
        == 0
    )


def counter(h: Harness, table: str, comment: str) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", table], check=False).stdout
    m = re.search(rf'packets (\d+).*comment "{comment}"', out)
    return int(m.group(1)) if m else 0


def load_ours(h: Harness) -> None:
    h.run(["sudo", "nft", "-f", "-"], stdin=f"""
table netdev {OURS} {{
    chain c_ingress {{
        type filter hook ingress device "{VETH}" priority 0; policy accept;
        ip daddr {HOST_IP} counter drop comment "ours-deny"
    }}
}}
""")


def load_foreign(h: Harness, priority: int, verdict: str) -> bool:
    """A second netdev ingress chain on the SAME device. Lower priority runs first."""
    return h.run(["sudo", "nft", "-f", "-"], check=False, stdin=f"""
table netdev {FOREIGN} {{
    chain f_ingress {{
        type filter hook ingress device "{VETH}" priority {priority}; policy accept;
        ip daddr {HOST_IP} counter {verdict} comment "foreign-{verdict}"
    }}
}}
""").returncode == 0


def main() -> int:
    h = Harness("V6b", "Can a foreign netdev chain on the same device shadow ours?")
    try:
        cleanup()
        h.run(["sudo", "ip", "netns", "add", NS])
        build_link(h)

        reach = h.probe("the guest reaches the denied host", lambda: reaches(h))
        reach.baseline(want=True, detail="no chain at all; without this every block is free")

        load_ours(h)
        h.expect("our chain blocks, alone on the device",
                 reach.sample("ours only"), want=False)
        h.control("our deny fired", counter(h, OURS, "ours-deny") > 0,
                  f"{counter(h, OURS, 'ours-deny')} packets")

        # -- can a second base chain even attach to the same device? --------
        attached = h.measure(
            "a SECOND netdev ingress chain attaches to the same device",
            load_foreign(h, -100, "accept"),
            "if the kernel refuses this, the whole shadowing class is closed at the door",
        )
        if not attached.value:
            h.void_arm("shadow", "the kernel refused a second netdev chain on the device")
            h.measure("so the shadowing class is", "unreachable — one chain per device",
                      "a stronger guarantee than macOS, where anchors nest by design")
        else:
            ours_before = counter(h, OURS, "ours-deny")
            still_blocked = h.measure(
                "still blocked with a foreign higher-priority ACCEPT in front",
                not reaches(h),
                arm="shadow",
            )
            h.control(
                "the foreign chain really is evaluating traffic",
                counter(h, FOREIGN, "foreign-accept") > 0,
                f"{counter(h, FOREIGN, 'foreign-accept')} packets — without this the "
                "foreign chain is inert and its failure to shadow proves nothing",
                arm="shadow",
            )
            h.measure(
                "and our deny still counted, so it was still reached",
                counter(h, OURS, "ours-deny") > ours_before,
                f"{ours_before} -> {counter(h, OURS, 'ours-deny')}",
                arm="shadow",
            )
            h.expect(
                "a foreign accept at higher priority does NOT shadow our deny",
                still_blocked,
                want=True,
                arm="shadow",
                unbaselined="the baseline is the no-chain state above; this arm adds a "
                            "chain rather than removing the mechanism, so the "
                            "discriminating before/after is 'ours only' vs 'ours plus "
                            "foreign', both recorded",
            )

            # -- the other direction: a foreign DROP masking our counter ----
            _quiet(["sudo", "nft", "delete", "table", "netdev", FOREIGN])
            load_foreign(h, -100, "drop")
            ours_before2 = counter(h, OURS, "ours-deny")
            blocked2 = not reaches(h)
            h.control("still blocked with a foreign DROP in front", blocked2, arm="shadow")
            h.control("the foreign drop fired", counter(h, FOREIGN, "foreign-drop") > 0,
                      f"{counter(h, FOREIGN, 'foreign-drop')} packets", arm="shadow")
            masked = h.measure(
                "a foreign terminal DROP stops our counter advancing",
                counter(h, OURS, "ours-deny") == ours_before2,
                f"ours {ours_before2} -> {counter(h, OURS, 'ours-deny')}. If masked, a "
                "counter-based detector reads zero on a sandbox whose traffic is being "
                "dropped by someone else — enforcement intact, signature identical to "
                "inert",
                arm="shadow",
            )
            h.measure("so a counter-based detector would call that sandbox",
                      "INERT (wrongly)" if masked.value else "healthy",
                      "the reason V1b's subscription design is worth having: it never "
                      "infers enforcement from a counter at all")

        h.not_tried(
            "the EGRESS hook. Every chain here is ingress; whether egress base chains "
            "interact differently is unasked",
            "a foreign chain installed by something real — firewalld, a CNI plugin, another "
            "container runtime. This is a hand-built adversary and the shapes real software "
            "installs may differ",
            "priority collisions: two chains at the SAME priority on one device, where "
            "ordering is unspecified and a design cannot rely on it",
            "whether a foreign chain can be installed by anything less privileged than "
            "root, which decides whether this is an attack or only an accident",
            "the nftables `netdev` egress hook, `flowtable` offload, and XDP, all of which "
            "sit at or before ingress and none of which is examined",
            "containers. Hand-built veth, per R11's reasoning",
            "whether OUR chain could equally shadow someone else's, which is the same "
            "question pointed at the blast radius of shipping this",
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

#!/usr/bin/env python3
# ABOUTME: W6 — can a root agent on the apple backend be denied CAP_NET_ADMIN after
# ABOUTME: the in-guest allowlist is installed, by dropping it from the bounding set?
# ABOUTME: If it can, DF179 closes on apple with no host component at all.

"""W6: the question DF179 never asks.

[DF179](../../findings-unresolved.md) records that `--network-isolated` grants the
sandbox `NET_ADMIN` on apple, podman and containerd, so the agent can flush its own
allowlist — measured, on this backend, with a destination becoming reachable again.
Its *What would actually close it* reasons entirely about **where enforcement sits**:
docker gets a netns-sharing sidecar, podman could, containerd needs the host netns,
and *"apple has no shareable host netns so it needs host `pf`"*. That sentence is the
whole justification for `macos-pf-privileged-path.md` existing for the apple backend.

**It never asks whether the capability can be taken away after the rules are written.**
Which is odd, because the answer is already in this tree twice: the docker path ends
with *"the entrypoint configures rules while running as root, then drops privileges —
the agent never has `CAP_NET_ADMIN`"* (`design/security.md:89`), and
`design/network-isolation.md:233` carries a test asserting the capability is out of the
container's **bounding set**. Dropping from the bounding set is not the same move as
dropping privileges: it is irreversible for every descendant, so it holds against an
agent that is *still root*, which is exactly the case docker sidesteps with `gosu` and
apple cannot.

So the question this run answers is narrow and it is not a design proposal: **on this
backend's kernel, does `PR_CAPBSET_DROP` of `CAP_NET_ADMIN` actually stop a root process
from removing an already-installed allowlist, and can it be walked around?**

**The probe is behavioural, not an exit status.** `iptables -F` returning non-zero is
not the property — the property is *a destination the allowlist denied becomes reachable
again*. Three runs in this corpus (`pf-spoof` runs 2 and 3, `pf-no-state` run 4) recorded
a confident inverse of the truth from a command that failed for a reason unrelated to the
mechanism, so the probe here reaches for a packet.

**The escapes are run in the same arm rather than argued about.** A root process denied a
capability has three obvious moves — ask `sudo`, enter a new user namespace and become
root there, or enter a new user namespace *and* a new network namespace. The third one
succeeds by construction and is self-harm rather than an escape; it is measured so the
file says which of the three it is.

**And the backend's own exec path is a separate arm, because it is the hole.** The
bounding set is a property of a process tree. `container exec` does not descend from the
entrypoint — the daemon spawns it — so if it is handed the container's configured
capability set, the drop is invisible to it. The agent cannot reach `container exec` from
inside the guest, so that is not an escape for the threat model this workstream targets;
it decides something else, which is whether a mechanism built on this holds for every
process yoloAI itself starts in the sandbox.

Instrument boundary: nothing here is timed. The host needs no privilege at all — every
command is `container`, which the invoking user already drives. That is itself part of
the result.

Run it as: `python3 w6_cap_bounding_set.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base"
BOX = "yb-w6-exec"
ALLOWED = "1.1.1.1"
DENIED = "1.0.0.1"

# The one variable. The probe below reads it, so the baseline and the sample provably come
# from the same callable rather than from two checks that look equivalent — which is the
# shape that produced pf-no-state run 5.
MODE = {"drop": False}

RESULTS: dict[str, str] = {}


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["container", "stop", BOX])
    _quiet(["container", "rm", BOX])


def guest_script(drop: bool) -> str:
    """The whole experiment, inside one guest, because the drop must be an ANCESTOR.

    `container exec` starts a process the daemon parents, so it can never be inside a
    bounding set the entrypoint narrowed. Everything that must observe the drop therefore
    runs in this script's own process tree.
    """
    # The agent runs from a FILE, not from a -c string. An earlier version inlined it and
    # the `awk '{print $2}'` inside closed the shell quoting around it, so the agent step
    # never ran at all and the harness voided — which is what v2 is for.
    launch = (
        "capsh --drop=cap_net_admin -- -c 'sh /tmp/agent.sh'"
        if drop
        else "sh /tmp/agent.sh"
    )

    return f"""
set -u
# curl PRINTS 000 on failure and also EXITS non-zero, so a `|| echo 000` fallback appends
# a second one and the field reads 000000. Swallow the status instead.
probe() {{ curl -s -o /dev/null -m 5 -w '%{{http_code}}' "http://$1/" 2>/dev/null; echo; }}

# EVERYTHING that must observe the drop lives in this file, because the drop is a property
# of a process tree and the outer script is not in it. Run 1 put the escapes out here and
# duly recorded `sudo` defeating a capability it had never lost — the run voided on an
# unrelated control, which is the only reason that did not ship.
cat > /tmp/agent.sh <<'AGENT_EOF'
probe() {{ curl -s -o /dev/null -m 5 -w '%{{http_code}}' "http://$1/" 2>/dev/null; echo; }}
printf 'AGENT_UID=%s\\n' "$(id -u)"
printf 'AGENT_CAPBND=%s\\n' "$(grep CapBnd /proc/self/status | tr -s ' \\t' ' ' | cut -d' ' -f2)"

# The agent's move. `-P ACCEPT` matters as much as `-F`: flushing the rules alone leaves
# the policy at DROP, which takes the sandbox's whole network down and makes every later
# probe read 000 for the wrong reason.
iptables -F OUTPUT >/dev/null 2>&1
iptables -P OUTPUT ACCEPT >/dev/null 2>&1
printf 'AGENT_DENIED_AFTER=%s\\n' "$(probe {DENIED})"
printf 'AGENT_ALLOWED_AFTER=%s\\n' "$(probe {ALLOWED})"

# Escapes, each judged by a packet rather than by an exit status.
sudo -n iptables -F OUTPUT >/dev/null 2>&1; sudo -n iptables -P OUTPUT ACCEPT >/dev/null 2>&1
printf 'ESC_SUDO_DENIED=%s\\n' "$(probe {DENIED})"
unshare -U -r -- iptables -F OUTPUT >/dev/null 2>&1
unshare -U -r -- iptables -P OUTPUT ACCEPT >/dev/null 2>&1
printf 'ESC_USERNS_DENIED=%s\\n' "$(probe {DENIED})"
# A private netns is expected to succeed and to be worthless: it is not the netns the
# policy is in, and it has no route out. Recorded so the file says which of the three
# this is rather than asserting it.
unshare -U -r -n -- ip link set lo up >/dev/null 2>&1 \
  && printf 'ESC_OWN_NETNS=ok\\n' || printf 'ESC_OWN_NETNS=refused\\n'
printf 'AGENT_DONE\\n'
AGENT_EOF

printf 'UID=%s\\n' "$(id -u)"
printf 'CAPBND_START=%s\\n' "$(grep CapBnd /proc/self/status | tr -s ' \\t' ' ' | cut -d' ' -f2)"

# --- install the in-guest allowlist, the shape entrypoint.py installs --------------
iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -d {ALLOWED} -j ACCEPT
iptables -P OUTPUT DROP
printf 'INSTALL_RC=%s\\n' "$?"

printf 'ALLOWED_UNDER_POLICY=%s\\n' "$(probe {ALLOWED})"
printf 'DENIED_UNDER_POLICY=%s\\n' "$(probe {DENIED})"

# --- the agent runs here, with or without the capability in its bounding set --------
{launch}
printf 'DONE\\n'
"""


def run_guest() -> dict[str, str]:
    """One container, one script, parsed into fields. Reads MODE — see its comment."""
    proc = _quiet(
        [
            "container", "run", "--rm",
            "--entrypoint", "/bin/sh",
            "--cap-add", "NET_ADMIN",
            IMAGE, "-c", guest_script(MODE["drop"]),
        ]
    )
    fields = {}
    for line in (proc.stdout + proc.stderr).splitlines():
        m = re.match(r"^([A-Z_]+)=(.*)$", line.strip())
        if m:
            fields[m.group(1)] = m.group(2)
    fields["_raw"] = proc.stdout + proc.stderr
    RESULTS[("drop" if MODE["drop"] else "nodrop")] = fields["_raw"]
    return fields


def agent_defeated_the_allowlist() -> bool:
    """THE probe. Did the denied destination become reachable after the agent tried?

    Behavioural on purpose: `iptables -F` exiting non-zero is not the property, and this
    directory has three runs that concluded the inverse of the truth from a command that
    failed for an unrelated reason.
    """
    f = run_guest()
    if "AGENT_DENIED_AFTER" not in f:
        raise HarnessError(
            "the guest script never reported AGENT_DENIED_AFTER; the run did not reach "
            f"the agent step at all. Raw output:\n{f.get('_raw', '')[-2000:]}"
        )
    return f["AGENT_DENIED_AFTER"] != "000"


def main() -> int:
    h = Harness(
        "W6",
        "Does dropping CAP_NET_ADMIN from the bounding set stop a root agent on the "
        "apple backend from removing its own allowlist?",
    )
    try:
        cleanup()
        h.require(
            "the apple container daemon is reachable",
            _quiet(["container", "ls"]).returncode == 0,
            "run `container system start` first",
        )
        h.require(
            f"the {IMAGE} image exists",
            IMAGE in _quiet(["container", "image", "ls"]).stdout,
        )

        probe = h.probe("the agent removed its own allowlist and reached a denied host",
                        agent_defeated_the_allowlist)

        # -- BASELINE: the mechanism absent. This is DF179, reproduced, and it is what
        # makes every "refused" below mean something.
        MODE["drop"] = False
        probe.baseline(
            want=True,
            detail="no bounding-set drop — DF179's mechanism, reproduced on this backend",
        )
        nodrop = RESULTS["nodrop"]
        f_nodrop = dict(re.findall(r"^([A-Z_]+)=(.*)$", nodrop, re.M))

        h.control(
            "the guest is root",
            f_nodrop.get("UID") == "0",
            f"uid={f_nodrop.get('UID')}",
        )
        h.control(
            "CAP_NET_ADMIN is in the container's bounding set to begin with",
            _bit_set(f_nodrop.get("CAPBND_START", "0"), 12),
            f"CapBnd={f_nodrop.get('CAPBND_START')} (bit 12 = CAP_NET_ADMIN)",
        )
        h.control(
            "the allowlist actually enforced before the agent touched it",
            f_nodrop.get("DENIED_UNDER_POLICY") == "000"
            and f_nodrop.get("ALLOWED_UNDER_POLICY") != "000",
            f"denied={f_nodrop.get('DENIED_UNDER_POLICY')} "
            f"allowed={f_nodrop.get('ALLOWED_UNDER_POLICY')}",
        )

        # -- SAMPLE: the same callable, one variable changed.
        MODE["drop"] = True
        dropped = probe.sample("with CAP_NET_ADMIN dropped from the bounding set")
        f_drop = dict(re.findall(r"^([A-Z_]+)=(.*)$", RESULTS["drop"], re.M))

        h.control(
            "the allowlist enforced in the dropped arm too, before the agent ran",
            f_drop.get("DENIED_UNDER_POLICY") == "000"
            and f_drop.get("ALLOWED_UNDER_POLICY") != "000",
            f"denied={f_drop.get('DENIED_UNDER_POLICY')} "
            f"allowed={f_drop.get('ALLOWED_UNDER_POLICY')}",
        )
        h.control(
            "the agent was STILL ROOT when it was refused",
            f_drop.get("AGENT_UID") == "0",
            "otherwise this measures gosu, which is docker's answer and not the question",
        )
        h.control(
            "the sandbox still had a network at the moment it was refused",
            f_drop.get("AGENT_ALLOWED_AFTER") not in (None, "000"),
            f"allowed={f_drop.get('AGENT_ALLOWED_AFTER')} immediately after the failed "
            "flush — so the 000 on the denied host is the allowlist, not a dead box",
        )
        h.control(
            "the agent's arm ran to the end",
            "AGENT_DONE" in RESULTS["drop"],
            "a truncated arm would report fewer escapes as silently 'not reached'",
        )

        h.measure(
            "the agent's own bounding set in the dropped arm",
            f_drop.get("AGENT_CAPBND"),
            f"against {f_nodrop.get('CAPBND_START')} at container start; bit 12 clear "
            "means CAP_NET_ADMIN is unreachable for it and every descendant",
        )
        h.measure(
            "escape: sudo, then the denied host",
            f_drop.get("ESC_SUDO_DENIED"),
            "000 = still contained. sudo cannot exceed a bounding set",
        )
        h.measure(
            "escape: a new user namespace mapped to root, then the denied host",
            f_drop.get("ESC_USERNS_DENIED"),
            "000 = still contained",
        )
        h.measure(
            "escape: a new user namespace AND its own netns",
            f_drop.get("ESC_OWN_NETNS"),
            "expected to succeed and to be worthless — that netns is not the one the "
            "policy is in, and it has no route out",
        )

        h.expect(
            "a root agent cannot remove its own allowlist once CAP_NET_ADMIN is out of "
            "the bounding set",
            dropped,
            want=False,
        )

        h.not_tried(
            "the shipped path. This installs the allowlist with a hand-written iptables "
            "sequence and drops with capsh; yoloAI's apple path runs entrypoint.py, which "
            "would have to do the drop itself. Whether that is reachable there is a code "
            "question this run does not answer",
            "`container exec` into a container whose entrypoint dropped the capability. "
            "The daemon parents that process, so it cannot be inside a bounding set the "
            "entrypoint narrowed — measured separately in W6b, and it decides whether the "
            "mechanism holds for processes yoloAI starts rather than for the agent",
            "ipset. The real allowlist is iptables+ipset and only iptables is exercised "
            "here; ipset needs the same capability and is assumed to follow, which is an "
            "assumption",
            "podman and containerd, which DF179 names in the same breath as apple",
            "whether the agent can prevent the drop happening — racing the entrypoint, or "
            "arranging to be the process that execs. That is the interesting attack and "
            "it is not run",
            "IPv6. The rules here are iptables, not ip6tables, which is DF179's gap and "
            "not this one's",
            "any host-side check. The whole point is that this needs none, so the run has "
            "nothing to say about pf",
        )
        return 0 if h.report() else 1
    finally:
        cleanup()


def _bit_set(hexmask: str, bit: int) -> bool:
    try:
        return bool(int(hexmask, 16) & (1 << bit))
    except ValueError:
        return False


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)

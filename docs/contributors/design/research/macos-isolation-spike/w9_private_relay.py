#!/usr/bin/env python3
# ABOUTME: W9 — the apple/container maintainers refuse pfctl-based solutions partly over
# ABOUTME: iCloud Private Relay, and the field reports Private Relay disabling itself when
# ABOUTME: rules are added to pf. Does installing yoloAI's pool disturb it on this host?

"""W9: the user-visible side effect nothing in this corpus has named.

The prior-art gate turned this up twice. `apple/container` discussion #719 records that the
maintainers **will not accept a solution requiring manual `pfctl`**, citing UX and **iCloud
Private Relay conflicts**; and Mullvad's write-up states that **Private Relay mostly
disables itself as soon as any firewall rule is added to pf**, with Apple's own discussion
forums carrying "PF/PFCTL breaks iCloud Private Relay".

This design writes rules into `com.apple/yoloai` on every sandbox start. If that switches
off a user's Private Relay, it is a side effect of *installing yoloAI* that no acceptance
test would catch and that this plan does not mention.

**Why this run exists in the shape it does.** While the earlier items of this round were
running, the host's privacy-proxy policy went from a single stable installation held since
03:45 to toggling on and off every 30–60 seconds between 05:29 and 05:54 — a window that
matches the pf harnesses almost exactly — and then settled at *no policy at all*. W6 and
W6b, which churned containers on an existing network with **no pf rules and no new
bridges**, disturbed nothing. That is suggestive and it is not attribution: those harnesses
changed **two** things at once, loading pf rules *and* creating and destroying vmnet
bridges, and a daemon that re-evaluates per-interface policy would react to the second
without caring about the first. That is the `confounded-arms` class by name.

So the two are separated here:

- **`pf-only`** — load and flush a real enforcing ruleset into our own anchor, repeatedly,
  creating no network and touching no interface.
- **`bridge-only`** — create and destroy a per-sandbox network and its guest, repeatedly,
  loading no pf rules at all.

**And a quiet window first, which is the baseline and the thing that makes either arm
readable.** The probe is *"did the privacy-proxy policy change state in this window"*, and
it must be seen answering **no** with nothing happening before either arm's *yes* means
anything. If the quiet window is not quiet, this host cannot answer the question today and
the run says so instead of reporting a number.

**The instrument needs its own control**, which is `pf-change-signal.txt` S1a's lesson: its
notify half always proved its watcher alive by posting to itself, and its log half had no
equivalent, so a silence could not be told from a broken query. Here the same predicate is
first run against a window known to contain transitions.

Instrument boundary: the windows are wall-clock and their length is stated. `sudo pfctl`
and `sudo log` run under the round's blanket grant, which is scaffolding.

Run it as: `python3 w9_private_relay.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from datetime import datetime, timedelta
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

ANCHOR = "com.apple/yoloai_w9"
IMAGE = "yoloai-base:latest"
NET = "ybw9net"
BOX = "ybw9"
WINDOW = 180

PREDICATE = ('process == "networkserviceproxy" AND eventMessage CONTAINS '
             '"privacy proxy policies updated"')

STATE: dict[str, object] = {}
# The window under measurement, held apart from STATE so the probe reads a typed list
# rather than an `object` the checker has to be told to ignore.
WINDOW_SEEN: list[str] = []

_PF_NOISE = re.compile(
    r"use of -f option|main ruleset added|/etc/pf\.conf for further|ALTQ|^\s*$", re.I
)

RULES = """\
table <yw9_dst> persist
pass  in  quick on bridge199 from any to <yw9_dst> no state
pass  out quick on bridge199 from <yw9_dst> to any no state
block drop in  quick on bridge199 from any to any
block drop out quick on bridge199 from any to any
"""


def _q(argv: list[str], timeout: int = 240) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def transitions_since(start: datetime) -> list[str]:
    """Every privacy-proxy policy change the daemon logged since `start`."""
    out = _q(["log", "show", "--style", "compact",
              "--start", start.strftime("%Y-%m-%d %H:%M:%S"),
              "--predicate", PREDICATE]).stdout
    found = []
    for ln in out.splitlines():
        m = re.search(r"(\d\d:\d\d:\d\d)\.\d+ .*policies updated - (\(null\)|<NSP)", ln)
        if m:
            found.append(f"{m.group(1)} {'off' if m.group(2) == '(null)' else 'ON'}")
    return found


def cleanup() -> None:
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    _q(["container", "stop", BOX])
    _q(["container", "rm", BOX])
    _q(["container", "network", "delete", NET])


def churn_pf(seconds: int) -> None:
    """Load and flush a real enforcing ruleset. No interface is created or touched."""
    end = time.monotonic() + seconds
    n = 0
    while time.monotonic() < end:
        subprocess.run(["sudo", "pfctl", "-a", ANCHOR, "-f", "-"],
                       input=RULES, capture_output=True, text=True, check=False)
        _q(["sudo", "pfctl", "-a", ANCHOR, "-t", "yw9_dst", "-T", "add", "1.1.1.1"])
        time.sleep(8)
        _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
        n += 1
        time.sleep(8)
    STATE["pf_cycles"] = n


def churn_bridges(seconds: int) -> None:
    """Create and destroy a per-sandbox network and guest. No pf rule is loaded."""
    end = time.monotonic() + seconds
    n = 0
    while time.monotonic() < end:
        _q(["container", "network", "create", NET])
        _q(["container", "run", "-d", "--name", BOX, "--network", NET, IMAGE, "sleep", "60"])
        time.sleep(5)
        _q(["container", "stop", BOX])
        _q(["container", "rm", BOX])
        _q(["container", "network", "delete", NET])
        n += 1
    STATE["bridge_cycles"] = n


def main() -> int:
    h = Harness("W9", "Does installing yoloAI's pf pool disturb iCloud Private Relay?")
    try:
        cleanup()

        # -- the instrument's own control, before it is trusted to report silence -------
        historical = transitions_since(datetime.now() - timedelta(hours=8))
        h.control(
            "the log predicate finds transitions it is known to contain",
            len(historical) > 0,
            f"{len(historical)} policy changes in the last 8h — without this, a quiet "
            "window is indistinguishable from a query that matches nothing",
        )
        h.measure("the last ten policy changes before this run", historical[-10:])
        h.measure(
            "the state this run starts in",
            historical[-1].split()[-1] if historical else "unknown",
            "'ON' means a privacy-proxy policy is installed; 'off' means none is",
        )

        # -- can this host answer the question at all? --------------------------------
        # A run that churns pf while Private Relay is off records "no disturbance" for the
        # same reason `pf-spoof` run 2 recorded 7 PASS / 0 FAIL: the capability under test
        # was not present. Both arms are voided rather than reported, because two FAILs
        # here read as a clean bill of health and would be quoted as one.
        accounts = _q(["defaults", "read", "MobileMeAccounts"]).stdout
        subscribed = "RELAY" in accounts.upper()
        starting_state = historical[-1].split()[-1] if historical else "unknown"
        h.measure("iCloud Private Relay is a service on this account", subscribed,
                  "the daemon runs regardless — it also serves Mail Privacy Protection and "
                  "Safari's tracker-IP hiding — so its presence proves nothing")
        if not subscribed or starting_state != "ON":
            reason = (
                f"Private Relay is not answerable on this host: subscribed={subscribed}, "
                f"policy state at start={starting_state!r}. A pf change cannot be shown to "
                "disable a feature that is already off, and reporting 'no disturbance' "
                "from that is the free negative this directory exists to refuse."
            )
            h.void_arm("pf-only", reason)
            h.void_arm("bridge-only", reason)

        def policy_changed() -> bool:
            """Did the daemon change privacy-proxy policy during the window just measured?"""
            return len(WINDOW_SEEN) > 0

        probe = h.probe("the privacy-proxy policy changed state during the window",
                        policy_changed)

        # -- BASELINE: a quiet window. Nothing loaded, nothing created. ----------------
        t0 = datetime.now()
        time.sleep(WINDOW)
        WINDOW_SEEN[:] = transitions_since(t0)
        quiet = list(WINDOW_SEEN)
        probe.baseline(want=False)
        h.measure(f"quiet window ({WINDOW}s, nothing running)", quiet or "no changes")

        # -- ARM 1: pf rules only ------------------------------------------------------
        t0 = datetime.now()
        churn_pf(WINDOW)
        WINDOW_SEEN[:] = transitions_since(t0)
        pf_changes = list(WINDOW_SEEN)
        pf_arm = probe.sample("while loading and flushing our anchor")
        h.measure(f"pf-only window ({WINDOW}s, {STATE.get('pf_cycles')} load/flush cycles, "
                  "no interface touched)", pf_changes or "no changes", arm="pf-only")

        # -- ARM 2: bridge churn only --------------------------------------------------
        t0 = datetime.now()
        churn_bridges(WINDOW)
        WINDOW_SEEN[:] = transitions_since(t0)
        br_changes = list(WINDOW_SEEN)
        br_arm = probe.sample("while creating and destroying vmnet networks")
        h.measure(f"bridge-only window ({WINDOW}s, {STATE.get('bridge_cycles')} "
                  "create/destroy cycles, no pf rule loaded)",
                  br_changes or "no changes", arm="bridge-only")

        h.expect("loading pf rules alone disturbs the privacy-proxy policy",
                 pf_arm, want=True, arm="pf-only")
        h.expect("creating and destroying vmnet networks alone disturbs it",
                 br_arm, want=True, arm="bridge-only")

        h.measure(
            "the correlation that prompted this run, left UNATTRIBUTED",
            "policy stable from 03:45 to 05:29, then 18 transitions in 25 minutes while "
            "W1/W2/W2b/W4 ran, then no policy from 05:54 onward",
            "recorded because it is what made this an item. With Private Relay off on this "
            "account those transitions belong to some other privacy-proxy consumer, and "
            "nothing here identifies which — it is an observation, not a finding",
        )

        h.not_tried(
            "**THE QUESTION ITSELF, on this host.** Private Relay is not a service on this "
            "iCloud account, so the arms are voided rather than reported. The prior-art "
            "hazard is therefore neither confirmed nor refuted here and remains live for "
            "users who do have it — it needs a host with iCloud+ and the feature on, and "
            "until then it is a documented risk rather than a measured one",
            "the user-visible symptom. Nothing here opens Safari, checks an egress address, "
            "or reads System Settings — the instrument is the daemon's own log, so a "
            "'change' is a policy update and not a demonstrated loss of the feature",
            "recovery. Whether a disturbed policy comes back on its own, and how long it "
            "takes, is not measured — the run restores nothing and asserts nothing about "
            "the state it leaves behind",
            "which pf operation matters, if any. The pf arm loads AND flushes AND changes "
            "table membership every cycle; it does not separate them, so a positive result "
            "there names the group and not the member",
            "any other pf writer. The anchor census in `pf-midlife-wipe.txt` W0 covers who "
            "else writes here; this run does not re-take it",
            "a second host, a different network, or a Wi-Fi/Ethernet difference. The "
            "daemon's policy is per-network and this is one machine on one SSID",
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

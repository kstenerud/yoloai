#!/usr/bin/env python3
# ABOUTME: W2b — the security boundary has to move twice (a verbose read of our own anchor
# ABOUTME: for W2's detector, and a table range for the inverted pool). Does the widened
# ABOUTME: grant still refuse everything the narrow one refused?

"""W2b: move the boundary once, and measure it once.

Two independent results ask D132's grant to widen, and the plan says to bundle them so the
boundary moves a single time:

- **`pf-grant-matrix.txt` G5** — the pool inversion needs tables named after *bridge
  indices*, so the table regex must cover 100–140 instead of 0–31.
- **W2** — the host-side detector reads `Evaluations`, which needs
  `pfctl -a com.apple/yoloai -vvs rules`. The shipped grant permits `-s rules$`, anchored,
  so the verbose form is refused today. `macos-pf-privileged-path.md` records the
  consequence bluntly: *"macOS went behavioural because it could not ask for a number it
  produces perfectly well."*

**A refusal measured under a blanket grant is not a refusal**, which is why
`pf-liveness-detect.txt` V4 is an `UNKNOWN` in the corpus. The method that works is already
here: this script runs as **root**, so its own authority does not come from sudoers, and it
**deletes the round's blanket grant for the duration** — stripping the unprivileged user's
access while leaving the harness fully privileged to set up and to probe as that user. It
refuses to report anything until `sudo -n /usr/bin/true` is *shown* to fail, and restores
the blanket from a `finally`.

**The blanket is also the baseline, which is the neat part.** Under v2 every expectation
must rest on a probe seen reporting the other answer. For a refusal, the mechanism is the
*restrictive grant* — so the mechanism-absent state is exactly the blanket-grant state, and
each refusal probe is sampled there first and required to come out **permitted**. The thing
that made V4 unanswerable is the thing that makes these answerable.

**The permits are controls, not claims, and that is deliberate.** A command that the blanket
also permits cannot be attributed to D132's grant by this method — the baseline and the
sample agree, so v2 correctly refuses to let a claim rest on it. What the permits establish
is that the widened grant is not simply broken; the security argument rests on the refusals.

**And one content check, because a permit/refuse matrix cannot see disclosure.** Widening to
a verbose read is only safe if the verbose form of *our* anchor does not reveal the host's
filter policy. That is checked by comparing what the grant-holder can now read against the
main ruleset, which the harness reads as root.

Instrument boundary: nothing is timed. The blanket grant is *outside* every measured
region here by construction — its removal is the precondition the run asserts.

Run it as: `sudo python3 w2b_grant_widening.py`
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError, Probe  # noqa: E402

ANCHOR = "com.apple/yoloai"
BLANKET = "/etc/sudoers.d/yoloai-spike-session"
MATRIX = "/etc/sudoers.d/yoloai-w2b-matrix"
# Parked OUTSIDE sudoers.d. Leaving it in the directory relies on sudo ignoring names with
# a dot in them, which is true and is a second thing that has to be true.
PARKED = "/etc/yoloai-spike-session.parked"
PINNED = "/etc/yoloai/pf-pool.conf"
POOL_LO, POOL_HI = 100, 140

USER = os.environ.get("SUDO_USER", "")

# D132's grant with BOTH widenings applied, and nothing else changed. The table range moves
# from 0-31 to the bridge-index range the inverted pool needs, and one line is added for the
# verbose read of our own anchor.
# RAW f-string. A plain one collapses `\\.` to `\.` on the way to the file and `\\\\.` to
# `\\.`, which sudo reads as a literal backslash — run 1 wrote that and every permit was
# refused while every refusal passed, i.e. a matrix that looked like an airtight boundary
# and was a typo.
GRANT = rf"""{USER} ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -t yb_(src|dst)_(1[0-3][0-9]|140) -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*$
{USER} ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -f /etc/yoloai/pf-pool\.conf$
{USER} ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -s rules$
{USER} ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -vvs rules$
{USER} ALL=(root) NOPASSWD: /sbin/pfctl ^-s info$
"""

# Everything that must stay refused. The first nine are `pf-grant-matrix.txt` G3's list,
# carried over verbatim so the widening is measured against the same boundary. The rest are
# new and exist BECAUSE of the widening: each one asks whether `-vvs` or the wider table
# range opened a door beside the one it was meant to open.
REFUSE = [
    ("kill states by host (-k)", ["-k", "192.168.64.2"]),
    ("kill source tracking (-K)", ["-K", "192.168.64.2"]),
    ("flush our own anchor (-F all)", ["-a", ANCHOR, "-F", "all"]),
    ("read the MAIN ruleset", ["-s", "rules"]),
    ("disable pf", ["-d"]),
    ("a different anchor", ["-a", "com.apple/other", "-s", "rules"]),
    ("load a ruleset from our own file", ["-a", ANCHOR, "-f", "/tmp/w2b-mine.conf"]),
    ("load a ruleset from stdin", ["-a", ANCHOR, "-f", "-"]),
    ("a table below the widened range", ["-a", ANCHOR, "-t", "yb_dst_99", "-T", "show"]),
    # -- new, and the point of this run --------------------------------------------
    ("a table above the widened range", ["-a", ANCHOR, "-t", "yb_dst_141", "-T", "show"]),
    ("verbose read of the MAIN ruleset", ["-vvs", "rules"]),
    ("verbose read of the PARENT anchor", ["-a", "com.apple", "-vvs", "rules"]),
    ("verbose read of a SIBLING anchor", ["-a", "com.apple/yoloai_w2", "-vvs", "rules"]),
    ("verbose read of pf STATES", ["-a", ANCHOR, "-vvs", "states"]),
    ("verbose read of TABLES", ["-a", ANCHOR, "-vvs", "Tables"]),
    ("recursive read of our anchor and its children (-A)", ["-a", ANCHOR, "-A", "-s", "rules"]),
]

PERMIT = [
    ("table add in the widened range", ["-a", ANCHOR, "-t", "yb_dst_101", "-T", "add", "1.1.1.1"]),
    ("table show at the top of the range", ["-a", ANCHOR, "-t", "yb_dst_140", "-T", "show"]),
    ("reload the pinned file", ["-a", ANCHOR, "-f", PINNED]),
    ("read our anchor's rules", ["-a", ANCHOR, "-s", "rules"]),
    ("read our anchor's rules VERBOSELY — the new one", ["-a", ANCHOR, "-vvs", "rules"]),
    ("pf info", ["-s", "info"]),
]


def _bound(args: list[str]) -> Callable[[], bool]:
    """One probe callable per command, with its argv captured.

    A `lambda a=args:` default-argument closure does the same thing and mypy cannot infer
    its type, so this says it explicitly rather than silencing the check.
    """
    return lambda: as_user(args)


def _q(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def as_user(args: list[str]) -> bool:
    """True when the unprivileged user is PERMITTED to run this pfctl invocation.

    `-k` is added to defeat any cached timestamp, so a permit is the policy's answer rather
    than a leftover ticket. sudo's own refusal is distinguished from pfctl's — a command
    that sudo allowed and pfctl then rejected is a PERMIT for this purpose, because the
    boundary being measured is the grant's.
    """
    p = _q(["sudo", "-u", USER, "-H", "sudo", "-n", "-k", "/sbin/pfctl", *args])
    blob = (p.stdout + p.stderr).lower()
    return not ("sudo: a password is required" in blob or "not allowed to execute" in blob
                or "may not run" in blob)


def main() -> int:
    h = Harness("W2b", "Does D132's grant still hold once it widens for the inverted pool "
                       "and the host-side detector?")
    had_blanket = os.path.exists(BLANKET)
    try:
        h.require("running as root", os.geteuid() == 0, "re-run with sudo")
        h.require("SUDO_USER is set", bool(USER), "run via sudo, not as a root login")
        h.require("the round's blanket grant is present to baseline against", had_blanket,
                  f"{BLANKET} missing — the mechanism-absent state does not exist")

        os.makedirs("/etc/yoloai", exist_ok=True)
        # A pinned file with REAL rules, loaded before the disclosure check runs. Run 2 used
        # a table declaration and nothing else, so `-vvs rules` returned no rule lines at
        # all and "the verbose read discloses none of the main ruleset" was true because
        # there was nothing to disclose — a free negative on the one check that is about
        # disclosure rather than about permission.
        Path(PINNED).write_text(
            "table <yb_dst_101> persist\n"
            "pass  in  quick on bridge101 from any to <yb_dst_101> no state\n"
            "pass  out quick on bridge101 from <yb_dst_101> to any no state\n"
            "block drop in  quick on bridge101 from any to any\n"
            "block drop out quick on bridge101 from any to any\n"
        )
        os.chmod(PINNED, 0o644)
        _q(["/sbin/pfctl", "-a", ANCHOR, "-f", PINNED])
        Path("/tmp/w2b-mine.conf").write_text("pass in quick on bridge101 all\n")

        # -- baselines, taken while the BLANKET is still installed ---------------------
        probes: dict[str, Probe] = {}
        for label, args in REFUSE:
            probe = h.probe(label, _bound(args))
            probes[label] = probe
            probe.baseline(want=True, detail="permitted under the blanket grant, which is what "
                                         "makes the refusal below attributable")

        # -- swap the blanket for the widened D132 grant ------------------------------
        Path(MATRIX).write_text(GRANT)
        os.chmod(MATRIX, 0o440)
        vis = _q(["visudo", "-c", "-f", MATRIX])
        h.control("the widened grant validates under visudo", vis.returncode == 0,
                  vis.stdout.strip() or vis.stderr.strip())
        os.rename(BLANKET, PARKED)

        # THE GUARD. Everything below is void unless the blanket is really gone.
        h.control(
            "`sudo -n /usr/bin/true` is refused, so a refusal means what it says",
            not as_user_true(),
            "this is the check pf-liveness-detect.txt V4 could not make",
        )
        h.measure("sudoers.d during the matrix", ", ".join(sorted(os.listdir("/etc/sudoers.d"))))

        for label, args in REFUSE:
            m = probes[label].sample(f"under the widened grant: {label}")
            h.expect(f"still refused: {label}", m, want=False)

        for label, args in PERMIT:
            h.control(f"permitted, as it must be: {label}", as_user(args),
                      "a permit the blanket also allows cannot be attributed to D132's "
                      "grant, so this is a control and not a claim")

        # -- disclosure, which a permit/refuse matrix cannot see ----------------------
        ours = _q(["sudo", "-u", USER, "-H", "sudo", "-n", "-k", "/sbin/pfctl",
                   "-a", ANCHOR, "-vvs", "rules"]).stdout
        main_rules = _q(["/sbin/pfctl", "-s", "rules"]).stdout
        main_lines = {ln.strip() for ln in main_rules.splitlines() if ln.strip()}
        # `-vvs rules` prefixes every RULE line with `@N `, and the bracketed lines are its
        # counters. Run 3 filtered out anything starting with `@`, which is precisely the
        # rules — so the disclosure set was empty and the control caught it.
        ours_lines = {
            re.sub(r"^@\d+\s+", "", ln.strip())
            for ln in ours.splitlines()
            if ln.strip().startswith("@")
        }
        leaked = sorted(main_lines & ours_lines)
        h.control(
            "the verbose read actually returned rules, so 'nothing leaked' is not free",
            len(ours_lines) > 0,
            f"{len(ours_lines)} rule lines came back through the grant",
        )
        h.measure("lines the verbose read discloses that are also in the main ruleset",
                  leaked or "none",
                  f"{len(main_lines)} rules in the main ruleset, "
                  f"{len(ours_lines)} rule lines readable through the widened grant")
        h.measure("does the verbose read mention any anchor but ours",
                  sorted({m for m in re.findall(r'anchor "([^"]+)"', ours)
                          if not m.startswith("com.apple/yoloai")}) or "no")
        # What the widening actually hands over, beyond the counter it was asked for. This
        # is the disclosure question a permit/refuse matrix cannot ask, and the answer is
        # small but not nothing.
        extra = sorted({
            m for m in re.findall(r"\[ ([A-Za-z]+)", ours)
        })
        h.measure("fields the verbose form exposes that `-s rules` does not", extra,
                  "`Inserted: uid N pid N` names the process that wrote the rules — "
                  "host-side information the plain form withholds, disclosed to the "
                  "grant holder as a side effect of asking for Evaluations")

        h.not_tried(
            "defeating the regexes. This takes the widened patterns and checks that the "
            "intended commands pass and the obvious ones fail; argument smuggling, "
            "alternate `pfctl` paths, symlinks and `--` handling are a review of the "
            "pattern rather than a run, and `pf-grant-matrix.txt` already says so about "
            "the narrow form",
            "whether a sandbox can land on a bridge index outside 100-140 at all, which is "
            "W4 and is what decides whether this range is the right one",
            "the verbose read's output under a host whose main ruleset is NOT the stock "
            "one. The disclosure check compares against this host's `/etc/pf.conf`; a "
            "machine running a third-party firewall has a different main ruleset and the "
            "comparison would have to be redone there",
            "whether `Evaluations` can be made to lie by a party who can write another "
            "anchor. W2 shows a `quick` match zeroes it, which is the honest direction; "
            "whether anything can INFLATE it to mask an outage is unasked",
            "sudo's own behaviour under a policy sourced from LDAP/AD rather than a file, "
            "which the timing files already name as an unmeasured case",
        )
        return 0 if h.report() else 1
    finally:
        if os.path.exists(PARKED):
            os.rename(PARKED, BLANKET)
        for path in (MATRIX, "/tmp/w2b-mine.conf"):
            if os.path.exists(path):
                os.remove(path)


def as_user_true() -> bool:
    p = _q(["sudo", "-u", USER, "-H", "sudo", "-n", "-k", "/usr/bin/true"])
    return p.returncode == 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)

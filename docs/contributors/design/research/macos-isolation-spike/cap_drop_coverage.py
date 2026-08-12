#!/usr/bin/env python3
# ABOUTME: W6 dropped CAP_NET_ADMIN and showed a root agent could not REMOVE its allowlist.
# ABOUTME: The move that matters is WIDENING one — ipset add — and W6 never tried it, nor
# ABOUTME: the real ipset-backed shape, nor a setuid binary as a way back to the capability.

"""Does the bounding-set drop cover the move an attacker would actually make?

**Post-round, owner-prompted.** Round 2 is closed; this is not one of its items.

`w6-cap-bounding-set.txt` measured a root agent failing to **remove** its allowlist —
`iptables -F OUTPUT` plus `-P OUTPUT ACCEPT`, the maximal teardown. That is the right thing
to measure first and it is not the move an attacker makes. Removing the allowlist takes the
sandbox's network *away*; **widening** it is what reaches a destination policy forbids. The
two need the same capability in principle, which is exactly why the corpus should not assume
it — W6's own bounds say `ipset` "needs the same capability and is assumed to follow, which
is an assumption".

Three things W6 left open, all cheap:

1. **The real shape.** `firewall.py` installs `ipset` (`hash:net`, `allowed-domains`) plus an
   iptables rule matching the set. W6 used a hand-written address-literal rule with no set at
   all, so nothing has been measured against the object the product actually ships.
2. **The widening direction.** `ipset add allowed-domains <denied>` is one command and it is
   the whole attack. So is `iptables -I OUTPUT 1 -j ACCEPT`.
3. **A setuid binary as a way back.** This is the obvious escape from a bounding-set drop and
   the one worth being sure about: on `execve` the kernel computes the new permitted set as
   `fP & bounding`, so a setuid-root binary should gain nothing the bounding set excludes —
   *should*, per the model, which is why it is run rather than reasoned about.

**The probe is behavioural and identical in both arms**: does a destination the allowlist
denies become reachable after the agent's attempt. An `ipset` command's exit status is not the
property.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded.

Run it as: `python3 cap_drop_coverage.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base"
ALLOW = "1.1.1.1"
DENY = "1.0.0.1"

MODE = {"drop": False}
RAW: dict[str, str] = {}


def _q(argv: list[str], timeout: int = 300) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest_script(drop: bool) -> str:
    """Install the ipset-backed shape, then let the 'agent' try to widen it."""
    launch = ("capsh --drop=cap_net_admin -- -c 'sh /tmp/agent.sh'"
              if drop else "sh /tmp/agent.sh")
    return f"""
set -u
probe() {{ curl -s -o /dev/null -m 6 -w '%{{http_code}}' "http://$1/" 2>/dev/null; echo; }}

cat > /tmp/agent.sh <<'AGENT_EOF'
probe() {{ curl -s -o /dev/null -m 6 -w '%{{http_code}}' "http://$1/" 2>/dev/null; echo; }}
printf 'AGENT_UID=%s\\n' "$(id -u)"
printf 'AGENT_CAPBND=%s\\n' "$(grep CapBnd /proc/self/status | tr -s ' \\t' ' ' | cut -d' ' -f2)"

# THE attack: widen the allowlist rather than remove it.
ipset add allowed-domains {DENY} >/dev/null 2>&1
printf 'AFTER_IPSET_ADD=%s\\n' "$(probe {DENY})"

# Second form: insert an accept ahead of everything.
iptables -I OUTPUT 1 -j ACCEPT >/dev/null 2>&1
printf 'AFTER_IPTABLES_INSERT=%s\\n' "$(probe {DENY})"

# Third: destroy the set the rule matches, so the match fails open if the rule is
# written that way round.
ipset flush allowed-domains >/dev/null 2>&1
ipset destroy allowed-domains >/dev/null 2>&1
printf 'AFTER_IPSET_DESTROY=%s\\n' "$(probe {DENY})"

# Fourth: a setuid-root binary, the obvious way back to a dropped capability.
SUID=$(find / -xdev -perm -4000 -type f 2>/dev/null | head -5 | tr '\\n' ' ')
printf 'SUID_BINARIES=%s\\n' "${{SUID:-none}}"
if command -v sudo >/dev/null 2>&1; then
  sudo -n ipset add allowed-domains {DENY} >/dev/null 2>&1
  printf 'AFTER_SUDO_IPSET=%s\\n' "$(probe {DENY})"
fi

printf 'ALLOWED_STILL=%s\\n' "$(probe {ALLOW})"
printf 'AGENT_DONE\\n'
AGENT_EOF

printf 'UID=%s\\n' "$(id -u)"
printf 'CAPBND_START=%s\\n' "$(grep CapBnd /proc/self/status | tr -s ' \\t' ' ' | cut -d' ' -f2)"

# --- the shape firewall.py installs: an ipset the iptables rule matches ------------
ipset create -exist allowed-domains hash:net
ipset flush allowed-domains
ipset add -exist allowed-domains {ALLOW}
iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT
printf 'SET_RULE_RC=%s\\n' "$?"
iptables -P OUTPUT DROP

printf 'ALLOWED_UNDER_POLICY=%s\\n' "$(probe {ALLOW})"
printf 'DENIED_UNDER_POLICY=%s\\n' "$(probe {DENY})"

{launch}
printf 'DONE\\n'
"""


def run_guest() -> dict[str, str]:
    proc = _q([
        "container", "run", "--rm", "--entrypoint", "/bin/sh",
        "--cap-add", "NET_ADMIN", IMAGE, "-c", guest_script(MODE["drop"]),
    ])
    blob = proc.stdout + proc.stderr
    RAW["drop" if MODE["drop"] else "nodrop"] = blob
    return dict(re.findall(r"^([A-Z_]+)=(.*)$", blob, re.M))


def agent_widened_the_allowlist() -> bool:
    """THE probe. Did a denied destination become reachable after the agent's attempts?"""
    f = run_guest()
    if "AGENT_DONE" not in RAW["drop" if MODE["drop"] else "nodrop"]:
        raise HarnessError(
            "the agent arm did not run to completion; nothing below would mean anything.\n"
            + RAW["drop" if MODE["drop"] else "nodrop"][-1500:]
        )
    codes = [f.get("AFTER_IPSET_ADD"), f.get("AFTER_IPTABLES_INSERT"),
             f.get("AFTER_IPSET_DESTROY"), f.get("AFTER_SUDO_IPSET")]
    return any(c not in (None, "", "000") for c in codes)


def main() -> int:
    h = Harness("CAPDROP", "Does the bounding-set drop stop a root agent WIDENING its "
                           "allowlist, against the real ipset-backed shape?")
    try:
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        probe = h.probe("the agent reached a denied destination after trying to widen its "
                        "own allowlist", agent_widened_the_allowlist)

        MODE["drop"] = False
        probe.baseline(want=True, detail="no bounding-set drop — the attack works, which is "
                                         "what makes the refusal below mean something")
        f0 = dict(re.findall(r"^([A-Z_]+)=(.*)$", RAW["nodrop"], re.M))
        h.control("the ipset-backed rule installed and enforced before the agent ran",
                  f0.get("DENIED_UNDER_POLICY") == "000"
                  and f0.get("ALLOWED_UNDER_POLICY") not in (None, "", "000"),
                  f"denied={f0.get('DENIED_UNDER_POLICY')} "
                  f"allowed={f0.get('ALLOWED_UNDER_POLICY')} — this is `--match-set`, not an "
                  "address literal, so it is the object firewall.py actually ships")
        h.measure("undropped arm: after ipset add / iptables insert / ipset destroy / sudo",
                  f"{f0.get('AFTER_IPSET_ADD')} / {f0.get('AFTER_IPTABLES_INSERT')} / "
                  f"{f0.get('AFTER_IPSET_DESTROY')} / {f0.get('AFTER_SUDO_IPSET')}")

        MODE["drop"] = True
        dropped = probe.sample("with CAP_NET_ADMIN dropped from the bounding set")
        f1 = dict(re.findall(r"^([A-Z_]+)=(.*)$", RAW["drop"], re.M))
        h.control("the rule enforced in the dropped arm too, before the agent ran",
                  f1.get("DENIED_UNDER_POLICY") == "000"
                  and f1.get("ALLOWED_UNDER_POLICY") not in (None, "", "000"),
                  f"denied={f1.get('DENIED_UNDER_POLICY')} "
                  f"allowed={f1.get('ALLOWED_UNDER_POLICY')}")
        h.control("the agent was still root", f1.get("AGENT_UID") == "0")
        h.control("the sandbox still had a network when it was refused",
                  f1.get("ALLOWED_STILL") not in (None, "", "000"),
                  f"allowlisted host -> {f1.get('ALLOWED_STILL')} at the end of the arm")

        h.measure("dropped arm: after ipset add / iptables insert / ipset destroy / sudo",
                  f"{f1.get('AFTER_IPSET_ADD')} / {f1.get('AFTER_IPTABLES_INSERT')} / "
                  f"{f1.get('AFTER_IPSET_DESTROY')} / {f1.get('AFTER_SUDO_IPSET')}")
        h.measure("the agent's bounding set", f1.get("AGENT_CAPBND"),
                  f"against {f1.get('CAPBND_START')} at container start")
        h.measure("setuid-root binaries the guest can reach", f1.get("SUID_BINARIES"),
                  "on execve the kernel computes the new permitted set as fP & bounding, so "
                  "a setuid-root binary should gain nothing the bounding set excludes — "
                  "listed so the claim rests on what is actually present")

        h.expect(
            "a root agent cannot WIDEN its own allowlist once CAP_NET_ADMIN is out of the "
            "bounding set, against the ipset-backed shape the product ships",
            dropped, want=False,
        )

        h.not_tried(
            "IPv6. The companion run `ipv6-sidestep.txt` shows the allowlist does not "
            "constrain v6 at all, which is a hole in the RULES that no capability question "
            "touches — an agent needs no privilege to use a family nothing filters",
            "the guest modifying what runs at the NEXT start. Measured separately by hand "
            "and not yet by a harness: `/yoloai/bin/firewall.py` is writable by a root agent "
            "and the edit survives a stop/start, so the drop protects the running sandbox "
            "and not its successor. That is the attack this run does not cover and it is "
            "the one that matters most",
            "racing the entrypoint. The agent here runs strictly after the install and the "
            "drop, which is the intended ordering; nothing tests a guest process that "
            "already exists when setup begins — a restart-policy leftover, or a user "
            "on-start command running before the firewall install",
            "DNS. `firewall.py` also handles resolver rules and this shape does not install "
            "them, so nothing here says whether a dropped agent can redirect resolution",
            "podman and containerd, which DF179 names alongside apple",
            "whether `--match-set` is even available on every backend. `firewall.py` "
            "carries a fallback for hosts whose iptables-nft lacks `xt_set`; this run uses "
            "the set path and does not exercise that fallback",
        )
        return 0 if h.report() else 1
    finally:
        pass


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)

#!/usr/bin/env python3
# ABOUTME: How the product ACTUALLY launches the agent, and what the image's NOPASSWD sudo
# ABOUTME: grant means for every in-guest containment vector. The uid is not the variable —
# ABOUTME: the sudo grant is, and removing it is what changes the answers.

"""What privilege does the agent really have, and what does that decide?

**Post-round, owner-prompted, and it corrects a recommendation.** Round 2 is closed; this is
not one of its items.

Several runs in this directory measured a root agent in a container launched *by the
harness*, with a bare `--entrypoint /bin/sh`. On that basis the agent was described as
running as root, and "run the agent non-root" was recommended to the owner as the change
that closes the `/proc/sys/net` class and the root-owned binaries.

**The product does not launch it that way.** `entrypoint.py:324` execs
`gosu yoloai python3 sandbox-setup.py`, branching on `running_as_root` rather than on
backend — so the agent has been running as **uid 1001 on every backend all along**, and
there is nothing to implement. What was never checked is `Dockerfile:229`:

    yoloai ALL=(ALL) NOPASSWD:ALL

The agent is non-root *and* root on demand, deliberately, because a sandbox where the agent
cannot install a package is not much of a sandbox.

**So the uid is not the variable, and this run treats the sudo grant as the mechanism
instead.** Three vectors are measured with the grant present and absent, from a process
launched exactly the way the product launches it — `capsh --drop=cap_net_admin` around
`gosu yoloai`:

- **A sysctl** (`disable_ipv6`), the class `proc-sys-net-census.txt` found 763 members of.
- **A file** the start path depends on, the class `tamper-persistence.txt` measured.
- **Netlink** (`ipset add`), the class `cap-drop-coverage.txt` measured.

The interesting result is that they do not agree, and the disagreement is the whole finding:
the bounding-set drop is inherited across `sudo`, so netlink stays closed, while the other
two are ordinary root operations that `sudo` simply grants.

**Removing the sudoers file is the mechanism-absent state**, which makes this a real
baseline rather than a comparison of two guesses: with the grant gone the agent is a plain
uid-1001 process and every vector should close.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded. The sudoers file
is removed *inside the guest* by the setup half, before the agent half runs.

Run it as: `python3 agent_privilege_reality.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base"
MODE = {"sudo": True}
RAW: dict[str, str] = {}

AGENT = r"""
echo "AGENT_UID=$(id -u)"
echo "AGENT_CAPBND=$(grep CapBnd /proc/self/status | tr -s ' \t' ' ' | cut -d' ' -f2)"
sudo -n sh -c 'echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6' 2>/dev/null \
  && echo "SYSCTL=ok" || echo "SYSCTL=refused"
echo "DISABLE_IPV6=$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6)"
sudo -n sh -c 'echo "# TAMPER" >> /yoloai/bin/firewall.py' 2>/dev/null \
  && echo "TAMPER=ok" || echo "TAMPER=refused"
sudo -n ipset add allowed-domains 1.0.0.1 2>/dev/null \
  && echo "NETLINK=ok" || echo "NETLINK=refused"
echo "DENIED=$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://1.0.0.1/ 2>/dev/null)"
echo "ALLOWED=$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://1.1.1.1/ 2>/dev/null)"
echo "AGENT_DONE"
"""


def _q(argv: list[str], timeout: int = 300) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def script(with_sudo: bool) -> str:
    strip = "" if with_sudo else "rm -f /etc/sudoers.d/yoloai"
    return f"""
set -u
cat > /tmp/agent.sh <<'AGENT_EOF'
{AGENT}AGENT_EOF
chmod 755 /tmp/agent.sh

ipset create -exist allowed-domains hash:net
ipset flush allowed-domains
ipset add -exist allowed-domains 1.1.1.1
iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT
iptables -P OUTPUT DROP
sysctl -w net.ipv6.conf.all.disable_ipv6=1 >/dev/null 2>&1

echo "SUDOERS_PRESENT=$([ -f /etc/sudoers.d/yoloai ] && echo yes || echo no)"
{strip}
echo "SUDOERS_AFTER_SETUP=$([ -f /etc/sudoers.d/yoloai ] && echo yes || echo no)"

# Exactly how entrypoint.py:324 launches it, with W6's drop applied around it.
capsh --drop=cap_net_admin -- -c 'gosu yoloai sh /tmp/agent.sh'
echo "DONE"
"""


def run() -> dict[str, str]:
    proc = _q(["container", "run", "--rm", "--entrypoint", "/bin/sh",
               "--cap-add", "NET_ADMIN", IMAGE, "-c", script(MODE["sudo"])])
    blob = proc.stdout + proc.stderr
    RAW["sudo" if MODE["sudo"] else "nosudo"] = blob
    if "AGENT_DONE" not in blob:
        raise HarnessError(f"the agent half did not complete:\n{blob[-1500:]}")
    return dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", blob, re.M))


def agent_can_defeat_enforcement() -> bool:
    """THE probe. Can the agent, as the product launches it, reach ANY of the three vectors?"""
    f = run()
    hit = [k for k in ("SYSCTL", "TAMPER", "NETLINK") if f.get(k) == "ok"]
    RAW["hits"] = ", ".join(hit) or "none"
    return len(hit) > 0


def main() -> int:
    h = Harness("PRIV", "What privilege does the agent really have, and what does the "
                        "image's sudo grant decide?")
    try:
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        probe = h.probe("the agent can defeat at least one containment vector",
                        agent_can_defeat_enforcement)

        # -- BASELINE: mechanism absent = the sudoers grant removed --------------------
        MODE["sudo"] = False
        probe.baseline(want=False)
        f_no = dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", RAW["nosudo"], re.M))
        h.control("the grant really was removed before the agent ran",
                  f_no.get("SUDOERS_PRESENT") == "yes"
                  and f_no.get("SUDOERS_AFTER_SETUP") == "no",
                  f"present at start={f_no.get('SUDOERS_PRESENT')}, "
                  f"after strip={f_no.get('SUDOERS_AFTER_SETUP')} — the image ships it, so "
                  "this is a removal and not an absence")
        h.control("the agent ran as the product runs it, not as root",
                  f_no.get("AGENT_UID") == "1001",
                  f"uid={f_no.get('AGENT_UID')} via `gosu yoloai`, which is what "
                  "entrypoint.py:324 execs on every backend")
        h.control("the allowlist was enforcing in the baseline arm",
                  f_no.get("DENIED") == "000"
                  and f_no.get("ALLOWED") not in (None, "", "000"),
                  f"denied={f_no.get('DENIED')} allowed={f_no.get('ALLOWED')}")
        h.measure("without sudo: sysctl / file tamper / netlink",
                  f"{f_no.get('SYSCTL')} / {f_no.get('TAMPER')} / {f_no.get('NETLINK')}")

        # -- SAMPLE: the image as shipped ---------------------------------------------
        MODE["sudo"] = True
        shipped = probe.sample("the image as shipped, with NOPASSWD:ALL")
        f_yes = dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", RAW["sudo"], re.M))
        h.control("the grant was present in this arm",
                  f_yes.get("SUDOERS_AFTER_SETUP") == "yes")
        h.control("same uid, same launch shape — the sudo grant is the ONLY variable",
                  f_yes.get("AGENT_UID") == f_no.get("AGENT_UID") == "1001",
                  "otherwise this compares two different things")
        h.measure("as shipped: sysctl / file tamper / netlink",
                  f"{f_yes.get('SYSCTL')} / {f_yes.get('TAMPER')} / {f_yes.get('NETLINK')}")
        h.measure("which vectors the shipped agent reaches", RAW.get("hits"))
        h.measure("the agent's bounding set", f_yes.get("AGENT_CAPBND"),
                  "CAP_NET_ADMIN is out of it, and the bounding set is inherited across "
                  "`sudo` — which is why netlink alone stays closed")
        h.measure("disable_ipv6 after the shipped agent ran", f_yes.get("DISABLE_IPV6"),
                  "1 = still off, 0 = the agent turned IPv6 back on")

        # Phrased as the finding, not as its absence. The baseline is "no sudo, nothing
        # reachable", so the claim has to want the opposite — and the opposite IS the
        # result: the grant the image ships is what unlocks the vectors, and the uid never
        # was the variable.
        h.expect(
            "the agent as the product launches it CAN defeat containment vectors, and the "
            "NOPASSWD sudo grant is what unlocks them",
            shipped, want=True,
        )

        h.not_tried(
            "**whether removing the sudo grant is acceptable.** The baseline arm shows it "
            "closes every vector, and it is not a proposal: a sandbox whose agent cannot "
            "install a package is a different product. This measures the trade, it does not "
            "make it",
            "the other 762 sysctls. One stands in for the class `proc-sys-net-census.txt` "
            "enumerated; nothing here re-walks it under sudo",
            "whether the agent can regain CAP_NET_ADMIN some other way — a setuid binary "
            "with file capabilities, a kernel bug, or a path that does not go through "
            "`sudo`. `cap-drop-coverage.txt` lists the setuid binaries present and none "
            "carries fcaps",
            "docker, podman and containerd. The launch shape here is shared "
            "(`entrypoint.py` is one file) but only apple was run",
            "what the agent does with any of it. These are capabilities, not exploits; "
            "`ipv6-sidestep.txt` and `tamper-persistence.txt` are where two of them are "
            "carried through to a defeated allowlist",
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

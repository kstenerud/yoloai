#!/usr/bin/env python3
# ABOUTME: The bounding-set drop stops everything that goes through netlink, and nothing that
# ABOUTME: goes through /proc/sys — because those files are root-owned 0644 and the agent is
# ABOUTME: root, so ordinary owner-write applies and no capability is consulted.

"""What the bounding-set drop does not cover, and what does.

**Post-round, owner-prompted.** Round 2 is closed; this is not one of its items.

`w6-cap-bounding-set.txt` and `cap-drop-coverage.txt` measured a root agent with
`CAP_NET_ADMIN` out of its bounding set failing to remove *or* widen its own allowlist —
`iptables`, `ipset`, `ip addr`, `ip route`, all refused. Every one of those goes through
**netlink**, which genuinely checks the capability.

**`/proc/sys/net` does not.** `disable_ipv6` is `-rw-r--r-- root root`. The kernel's
`net_ctl_permissions` widens those bits for a caller holding `CAP_NET_ADMIN` and otherwise
returns the file's own mode — and the file's own mode grants **write to its owner**. The
agent is uid 0. So the write succeeds with no capability consulted at all, and re-enabling
IPv6 is one `echo` away. `ip -6 addr add` and `ip -6 route add` stay refused, which does not
help: with the interface accepting Router Advertisements again, **the kernel reconfigures
the address and the default route by itself**.

That matters because disabling IPv6 in the guest is the cheapest of the candidate remedies
for the hole `ipv6-sidestep.txt` measures, and it is defeatable by the thing it constrains
unless something else changes.

**What closes it is the agent not being root** — which is exactly what the docker path
already does (`security.md`: *"the entrypoint configures rules while running as root, then
drops privileges via `gosu` — the agent never has `CAP_NET_ADMIN`"*), and what the apple
path does not (DF179).

**Two arms, one variable: the agent's uid.** The bounding-set drop is present in both, so
this isolates the uid and not the capability. Dropping `CAP_DAC_OVERRIDE` as well was tried
first and does **not** close it, which is what pointed at ownership rather than at a
capability check — that arm is kept below because a refuted mechanism is worth as much as
the one that survives.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded.

Run it as: `python3 agent_uid_and_sysctls.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base"
GUEST_USER = "yoloai"
MODE = {"launcher": ""}
RAW: dict[str, str] = {}

AGENT = """\
echo "AGENT_UID=$(id -u)"
echo "AGENT_CAPBND=$(grep CapBnd /proc/self/status | tr -s ' \\t' ' ' | cut -d' ' -f2)"
(echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6) 2>/dev/null \\
  && echo "SYSCTL_WRITE=ok" || echo "SYSCTL_WRITE=refused"
echo "DISABLE_IPV6=$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6)"
sleep 2
echo "V6_ADDRS=$(ip -6 addr show eth0 2>/dev/null | grep -c inet6)"
ipset add allowed-domains 1.0.0.1 >/dev/null 2>&1 && echo "IPSET=ok" || echo "IPSET=refused"
echo "V4=$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://1.1.1.1/ 2>/dev/null)"
echo "AGENT_DONE"
"""


def _q(argv: list[str], timeout: int = 300) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def script(launcher: str) -> str:
    return f"""
set -u
cat > /tmp/agent.sh <<'AGENT_EOF'
{AGENT}AGENT_EOF
chmod 755 /tmp/agent.sh

ipset create -exist allowed-domains hash:net
ipset add -exist allowed-domains 1.1.1.1
iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT
iptables -P OUTPUT DROP

# The remedy under test: turn IPv6 off in the guest before the agent runs.
sysctl -w net.ipv6.conf.all.disable_ipv6=1 >/dev/null 2>&1
sysctl -w net.ipv6.conf.default.disable_ipv6=1 >/dev/null 2>&1
echo "PRE_DISABLE=$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6)"
echo "PRE_V6_ADDRS=$(ip -6 addr show eth0 2>/dev/null | grep -c inet6)"
echo "SYSCTL_MODE=$(ls -l /proc/sys/net/ipv6/conf/all/disable_ipv6 | cut -d' ' -f1,3,4)"

{launcher}
echo "DONE"
"""


def run() -> dict[str, str]:
    proc = _q(["container", "run", "--rm", "--entrypoint", "/bin/sh",
               "--cap-add", "NET_ADMIN", IMAGE, "-c", script(MODE["launcher"])])
    blob = proc.stdout + proc.stderr
    RAW[MODE["launcher"]] = blob
    if "AGENT_DONE" not in blob:
        raise HarnessError(f"the agent arm did not complete:\n{blob[-1500:]}")
    return dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", blob, re.M))


def agent_got_ipv6_back() -> bool:
    """THE probe. After the agent's attempt, does the guest hold an IPv6 address again?

    Behavioural rather than an exit status: the sysctl write succeeding is not the property,
    the interface being reconfigured is — and the kernel does that itself once the knob
    flips, with no capability involved.
    """
    f = run()
    RAW["last"] = str(f)
    return int(f.get("V6_ADDRS", "0")) > 0


ROOT = "capsh --drop=cap_net_admin -- -c 'sh /tmp/agent.sh'"
ROOT_NO_DAC = "capsh --drop=cap_net_admin,cap_dac_override -- -c 'sh /tmp/agent.sh'"
NONROOT = f"capsh --drop=cap_net_admin -- -c 'gosu {GUEST_USER} sh /tmp/agent.sh'"


def main() -> int:
    h = Harness("UID", "Does the bounding-set drop protect /proc/sys, and what does?")
    try:
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        probe = h.probe("the agent turned IPv6 back on", agent_got_ipv6_back)

        # -- BASELINE: a ROOT agent, capability dropped. The remedy fails. ------------
        MODE["launcher"] = ROOT
        probe.baseline(want=True)
        f_root = dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", RAW[ROOT], re.M))
        h.control("IPv6 really was off before the agent ran",
                  f_root.get("PRE_DISABLE") == "1" and f_root.get("PRE_V6_ADDRS") == "0",
                  f"disable_ipv6={f_root.get('PRE_DISABLE')} "
                  f"addrs={f_root.get('PRE_V6_ADDRS')} — otherwise the arm measures nothing")
        h.control("CAP_NET_ADMIN really was gone from the root agent",
                  f_root.get("IPSET") == "refused",
                  "ipset goes through netlink, which checks the capability — so a refusal "
                  "here proves the drop took, and makes the sysctl success below a "
                  "statement about /proc/sys rather than about the drop failing")
        h.measure("the sysctl's owner and mode", f_root.get("SYSCTL_MODE"),
                  "root-owned and writable by its owner; the agent is uid 0")
        h.measure("root agent: sysctl write / disable_ipv6 / v6 addrs",
                  f"{f_root.get('SYSCTL_WRITE')} / {f_root.get('DISABLE_IPV6')} / "
                  f"{f_root.get('V6_ADDRS')}",
                  "the address comes back on its own — the kernel reconfigures from the RA "
                  "once the knob flips, needing no capability")

        # -- the mechanism that was tried and refuted --------------------------------
        MODE["launcher"] = ROOT_NO_DAC
        m_dac = probe.sample("root agent, CAP_DAC_OVERRIDE also dropped")
        f_dac = dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", RAW[ROOT_NO_DAC], re.M))
        h.measure("dropping CAP_DAC_OVERRIDE as well",
                  f"bounding {f_dac.get('AGENT_CAPBND')}, write "
                  f"{f_dac.get('SYSCTL_WRITE')}, v6 addrs {f_dac.get('V6_ADDRS')}",
                  "REFUTED as the mechanism. The first guess was that DAC_OVERRIDE bypassed "
                  "a permission check; it does not, because uid 0 is the file's OWNER and "
                  "needs no override to write a mode-644 file it owns")
        h.measure("does dropping DAC_OVERRIDE close it?", m_dac.value is False)

        # -- SAMPLE: the same drop, non-root agent ------------------------------------
        MODE["launcher"] = NONROOT
        m = probe.sample("non-root agent, same bounding-set drop")
        f_nr = dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", RAW[NONROOT], re.M))
        h.control("the non-root arm really did run as a different uid",
                  f_nr.get("AGENT_UID") not in (None, "0"),
                  f"uid={f_nr.get('AGENT_UID')} — the one variable between the arms")
        h.control("IPv6 was off before that arm's agent ran too",
                  f_nr.get("PRE_DISABLE") == "1" and f_nr.get("PRE_V6_ADDRS") == "0")
        h.control("the sandbox still had a network when it was refused",
                  f_nr.get("V4") not in (None, "", "000"),
                  f"allowlisted host -> {f_nr.get('V4')}")
        h.measure("non-root agent: sysctl write / disable_ipv6 / v6 addrs",
                  f"{f_nr.get('SYSCTL_WRITE')} / {f_nr.get('DISABLE_IPV6')} / "
                  f"{f_nr.get('V6_ADDRS')}")

        h.expect(
            "an agent that is not root cannot turn IPv6 back on, so disabling it in the "
            "guest is a remedy that holds",
            m, want=False,
        )

        h.not_tried(
            "the rest of `/proc/sys/net`. This measures ONE knob. Every root-owned 0644 "
            "sysctl under there is reachable by a root agent by the same route, and no "
            "census was taken of which ones matter — `route_localnet`, `accept_local`, "
            "`rp_filter` and the forwarding toggles are the obvious places to look",
            "`/proc/sys` mounted read-only, which would close the class rather than one "
            "knob. It needs CAP_SYS_ADMIN to remount and the container's bounding set does "
            "not contain it (the base set here is docker's default, `a80435fb`), so the "
            "entrypoint cannot do it from inside — it would have to be a mount the host "
            "sets up at create time. Untried",
            "whether the apple path can run the agent non-root at all. `gosu` is present in "
            "the image and the docker path uses it; whether the legacy launch path apple "
            "takes can adopt it is a code question this run does not open",
            "a guest that regains root after the drop — via a setuid binary, or a kernel "
            "bug. The two mechanisms compose deliberately: non-root closes the sysctl path, "
            "and the bounding-set drop still holds if root is somehow recovered",
            "IPv6 disabled by any route other than the sysctl. A v6-less network would be "
            "cleaner and apple's `container network create` does not offer one — a network "
            "created with only `--subnet` still gets a ULA `/64` assigned",
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

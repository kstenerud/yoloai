#!/usr/bin/env python3
# ABOUTME: agent-uid-and-sysctls.txt measured ONE writable knob under /proc/sys/net and said a
# ABOUTME: census was owed. This is the census: every file, both uids, and which of the
# ABOUTME: writable ones a rogue agent could actually use.

"""How wide is the `/proc/sys/net` hole, and does non-root really close the class?

**Post-round, owner-prompted.** Round 2 is closed; this is not one of its items.

[`agent-uid-and-sysctls.txt`](results/agent-uid-and-sysctls.txt) established that the
bounding-set drop covers everything reached through **netlink** and nothing reached through
**`/proc/sys`**, because those files are root-owned mode 0644 and the agent is uid 0 — so
ordinary owner-write applies and no capability is consulted. It measured exactly one knob
(`disable_ipv6`) and its own bounds say a census is owed:

> *This measures ONE knob. Every root-owned 0644 sysctl under there is reachable by a root
> agent by the same route, and no census was taken of which ones matter.*

Two questions, and the second is the one that decides a design:

1. **How many knobs are actually writable**, by a root agent with `CAP_NET_ADMIN` dropped?
2. **Does running the agent non-root close the whole class**, or only the instance already
   measured?

**The write is idempotent by construction**: each file's current value is read and written
back unchanged. Nothing is set to a new value, so the census cannot change the guest's
behaviour while measuring it. Write-only files and anything whose name contains `flush` are
skipped, because those are triggers rather than settings and writing them has an effect
regardless of the value.

**A writable knob is not automatically a hole.** The interesting question is whether any of
them defeats the allowlist, so the run separates *writable* from *containment-relevant* and
names the second list explicitly rather than leaving a count to be read as a threat.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded.

Run it as: `python3 proc_sys_net_census.py`
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

# Knobs a rogue agent could plausibly use, as opposed to merely being able to write. Each is
# named with what it would buy, so the list can be argued with rather than trusted.
INTERESTING = {
    "ipv6/conf/all/disable_ipv6": "turns IPv6 back on; the kernel then reconfigures address "
                                  "and default route from the RA by itself",
    "ipv6/conf/all/accept_ra": "re-accept Router Advertisements, restoring the v6 default route",
    "ipv6/conf/default/disable_ipv6": "same, for interfaces appearing later",
    "ipv4/ip_forward": "make the sandbox a router for anything that can reach it",
    "ipv4/conf/all/rp_filter": "relax reverse-path filtering, which is the check that makes "
                              "source spoofing fail",
    "ipv4/conf/all/accept_local": "accept packets claiming a local source address",
    "ipv4/conf/all/route_localnet": "route 127/8 as if it were global",
    "ipv4/conf/all/forwarding": "per-family forwarding toggle",
    "ipv4/ping_group_range": "open raw-ish ICMP sockets to unprivileged uids",
    "ipv4/conf/all/send_redirects": "emit ICMP redirects at neighbours",
    "ipv4/conf/all/arp_ignore": "change who this host answers ARP for",
    "ipv4/conf/all/arp_announce": "change what source it announces",
}

# The census itself. Idempotent: read the value, write the same value back.
AGENT = r"""
echo "AGENT_UID=$(id -u)"
echo "AGENT_CAPBND=$(grep CapBnd /proc/self/status | tr -s ' \t' ' ' | cut -d' ' -f2)"
total=0; writable=0
: > /tmp/writable.txt
for f in $(find /proc/sys/net -type f 2>/dev/null); do
  case "$f" in *flush*) continue;; esac
  [ -r "$f" ] || continue
  v=$(cat "$f" 2>/dev/null) || continue
  total=$((total+1))
  if (printf '%s\n' "$v" > "$f") 2>/dev/null; then
    writable=$((writable+1))
    printf '%s\n' "${f#/proc/sys/net/}" >> /tmp/writable.txt
  fi
done
echo "ROOT_ONLY=$(find /proc/sys/net -type f -perm -u+r ! -perm -o+r 2>/dev/null | wc -l | tr -d ' ')"
echo "TOTAL=$total"
echo "WRITABLE=$writable"
echo "WRITABLE_LIST_START"
sort /tmp/writable.txt
echo "WRITABLE_LIST_END"
ipset add allowed-domains 1.0.0.1 >/dev/null 2>&1 && echo "IPSET=ok" || echo "IPSET=refused"
echo "V4=$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://1.1.1.1/ 2>/dev/null)"
echo "DENIED=$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://1.0.0.1/ 2>/dev/null)"
echo "AGENT_DONE"
"""


def _q(argv: list[str], timeout: int = 600) -> subprocess.CompletedProcess[str]:
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
{launcher}
echo "DONE"
"""


def run() -> tuple[dict[str, str], list[str]]:
    proc = _q(["container", "run", "--rm", "--entrypoint", "/bin/sh",
               "--cap-add", "NET_ADMIN", IMAGE, "-c", script(MODE["launcher"])])
    blob = proc.stdout + proc.stderr
    RAW[MODE["launcher"]] = blob
    if "AGENT_DONE" not in blob:
        raise HarnessError(f"the census did not complete:\n{blob[-1500:]}")
    fields = dict(re.findall(r"^([A-Z0-9_]+)=(.*)$", blob, re.M))
    m = re.search(r"WRITABLE_LIST_START\n(.*?)WRITABLE_LIST_END", blob, re.S)
    listed = [ln.strip() for ln in (m.group(1).splitlines() if m else []) if ln.strip()]
    return fields, listed


def interesting_writable() -> list[str]:
    _f, listed = run()
    RAW["last_list"] = "\n".join(listed)
    return [k for k in INTERESTING if k in listed]


def agent_can_write_a_useful_knob() -> bool:
    """THE probe. Not 'can it write something' — can it write something it could USE."""
    hits = interesting_writable()
    RAW["hits"] = ", ".join(hits) or "none"
    return len(hits) > 0


ROOT = "capsh --drop=cap_net_admin -- -c 'sh /tmp/agent.sh'"
NONROOT = f"capsh --drop=cap_net_admin -- -c 'gosu {GUEST_USER} sh /tmp/agent.sh'"


def main() -> int:
    h = Harness("SYSCTL", "How much of /proc/sys/net is writable by the agent, and does "
                          "non-root close the class?")
    try:
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        probe = h.probe("the agent can write a containment-relevant sysctl",
                        agent_can_write_a_useful_knob)

        # -- BASELINE: root agent, capability dropped --------------------------------
        MODE["launcher"] = ROOT
        probe.baseline(want=True)
        f_root, list_root = run()
        h.control("CAP_NET_ADMIN really was gone from the root agent",
                  f_root.get("IPSET") == "refused",
                  "ipset goes through netlink and checks the capability, so a refusal proves "
                  "the drop took — every sysctl write below is despite that, not because it "
                  "silently failed")
        h.control("the allowlist was enforcing throughout",
                  f_root.get("DENIED") == "000"
                  and f_root.get("V4") not in (None, "", "000"),
                  f"allowed={f_root.get('V4')} denied={f_root.get('DENIED')}")
        h.measure("root agent: files examined / writable",
                  f"{f_root.get('TOTAL')} / {f_root.get('WRITABLE')}")
        hits_root = [k for k in INTERESTING if k in list_root]
        h.measure("root agent: containment-relevant knobs it can write", hits_root or "none")
        for k in hits_root:
            h.measure(f"  {k}", INTERESTING[k])
        h.measure("root agent: a sample of the rest",
                  [x for x in list_root if x not in hits_root][:12],
                  f"{max(0, len(list_root) - len(hits_root))} others, truncated for reading")

        # -- SAMPLE: same drop, non-root agent ---------------------------------------
        MODE["launcher"] = NONROOT
        m = probe.sample("non-root agent, same bounding-set drop")
        f_nr, list_nr = run()
        h.control("the non-root arm really did run as a different uid",
                  f_nr.get("AGENT_UID") not in (None, "0"),
                  f"uid={f_nr.get('AGENT_UID')} — the one variable between the arms")
        # The populations are NOT identical and should not be forced to be: a handful of
        # files under /proc/sys/net are mode 0600 (`tcp_fastopen_key`, the `stable_secret`
        # set), so root reads them and the non-root arm cannot. Run 1 controlled on exact
        # equality, fired at 780 vs 779, and voided the run -- correctly, because an
        # unexplained population difference is exactly how a smaller writable count gets to
        # be free. The difference is now named and bounded instead of asserted away.
        root_only = int(f_root.get("ROOT_ONLY", "0"))
        h.measure("files under /proc/sys/net readable only by root (mode 0600)", root_only,
                  "tcp_fastopen_key and the stable_secret set — legitimately outside the "
                  "non-root arm's population")
        h.control("the two arms examined effectively the same population",
                  int(f_nr.get("TOTAL", "0")) >= int(f_root.get("TOTAL", "1")) - root_only - 5,
                  f"{f_nr.get('TOTAL')} files against the root arm's {f_root.get('TOTAL')}, "
                  f"with {root_only} root-only by mode and a small per-instance variation in "
                  "the per-interface v6 entries. A population much smaller than that would "
                  "make the writable count below free")
        h.measure("non-root agent: files examined / writable",
                  f"{f_nr.get('TOTAL')} / {f_nr.get('WRITABLE')}")
        h.measure("non-root agent: anything still writable", list_nr or "none")

        h.expect(
            "a non-root agent cannot write any containment-relevant sysctl, so the uid "
            "change closes the class rather than the one knob already measured",
            m, want=False,
        )

        h.not_tried(
            "**whether any writable knob actually defeats the allowlist.** The list is "
            "separated into containment-relevant and not by JUDGEMENT, from what each knob "
            "does — not by trying each one and probing a denied destination. `disable_ipv6` "
            "is the only one measured end to end (`ipv6-sidestep.txt`, "
            "`agent-uid-and-sysctls.txt`); the rest are named as plausible and are not "
            "demonstrated exploits",
            "sysctls outside `/proc/sys/net`. `kernel.*`, `fs.*` and `user.*` are a "
            "different surface with different owners, and a root agent's reach there is "
            "unexamined",
            "non-net paths to the same effect — netlink is covered by the capability, but "
            "`ioctl` on a socket, `/sys/class/net`, and eBPF are not enumerated here",
            "a Fedora or Arch base. Everything here is the Debian-derived blessed image; "
            "sysctl defaults, file modes and the presence of `gosu` are all properties of "
            "that image and do not transfer",
            "whether the apple launch path CAN run the agent non-root. `gosu` is in the "
            "image and the docker path uses it; the legacy path apple takes is a code "
            "question this run does not open",
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

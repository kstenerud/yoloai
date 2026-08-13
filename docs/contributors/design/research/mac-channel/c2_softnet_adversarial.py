#!/usr/bin/env python3
# ABOUTME: C2 of the mac-channel round — is tart's Softnet an out-of-sandbox
# ABOUTME: enforcement point in D137's sense, or only an unprivileged-agent one?
# ABOUTME: Attacks a measured-enforcing default-deny policy from a root guest.

"""C2: can a root guest defeat tart's Softnet policy?

`w8-softnet-enforcement.txt` (harness v2) established that Softnet's default-deny form
enforces — permitted 301, denied 000, with the permitted half checked so it is an
allowlist and not an outage. It did **not** attack it. W8's own *not tried* list names
source pinning as unexercised, and `agent-privilege-reality.txt` is the standing
reminder that "the guest cannot reach it" is exactly the reasoning that collapsed for
the in-guest allowlist.

D137 §1 requires that enforcement sit where **the agent has no privilege over it**. So
the question is not whether Softnet filters, but whether a guest with root can get out
from under it. Softnet is a host process holding the VM's network file descriptor and
enforcing the VM's MAC (`softnet --vm-fd --vm-mac-address`), which predicts containment;
this measures it.

**The trap this run is built around.** Every attack here can break the guest's own
networking. A guest that has DoSed itself reports "denied host unreachable" — the exact
shape of containment, for none of the reason. So each attack is followed by BOTH a
denied-host probe and a permitted-host probe, and an attack that kills the permitted
host is recorded as inconclusive rather than as containment (A22).

The instrument's boundary: the VM is driven by `tart run` directly with boot-time flags,
not through yoloAI — `runtime/tart` passes no Softnet flags today, which is the finding
rather than a gap here. `tart exec` rides the Guest Agent's own channel, not the filtered
path, so a successful block cannot read as a dead VM.
"""

import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "..", "..", "scripts"))
from research_harness_v2 import Harness  # noqa: E402

TART = "/opt/homebrew/bin/tart"
VM = "yoloai-base"
PERMITTED = "1.1.1.1"
DENIED = "208.67.222.222"  # answers HTTP (404) from this host, and a different
#                            operator from PERMITTED, so the arms do not share a route
GUEST_PATH = "export PATH=/sbin:/usr/sbin:/bin:/usr/bin;"
ENV = dict(os.environ, PATH="/opt/homebrew/bin:" + os.environ.get("PATH", ""))


def sh(*args, timeout=180):
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout, env=ENV)


def guest(script, timeout=120):
    return sh(TART, "exec", VM, "sh", "-c", GUEST_PATH + script, timeout=timeout)


def stop_vm():
    sh(TART, "stop", VM, timeout=90)
    time.sleep(3)


def boot(flags):
    stop_vm()
    subprocess.Popen([TART, "run", VM, "--no-graphics"] + flags,
                     stdout=open("/tmp/yc1/tart-c2.log", "w"), stderr=subprocess.STDOUT, env=ENV)
    for _ in range(40):
        time.sleep(5)
        if guest("echo up", timeout=30).stdout.strip().endswith("up"):
            return True
    return False


def http_code(dest):
    out = guest(f'curl -s -m 8 -o /dev/null -w "%{{http_code}}" http://{dest}').stdout.strip()
    return out.splitlines()[-1].strip() if out else ""


def reaches(dest):
    c = http_code(dest)
    return c.isdigit() and c != "000"


DEFAULT_DENY = ["--net-softnet-block=0.0.0.0/0", f"--net-softnet-allow={PERMITTED}/32"]
PERMISSIVE = ["--net-softnet-allow=0.0.0.0/0"]


def main():
    h = Harness("C2", "can a root guest defeat tart's Softnet default-deny policy?")

    h.require("tart is present", os.path.exists(TART))
    h.require("passwordless sudo for softnet is available on the host",
              subprocess.run(["sudo", "-n", "/usr/bin/true"], capture_output=True).returncode == 0,
              detail="softnet refuses to start without root; a temporary sudoers drop-in grants it")

    # ---- baseline arm: policy wide open ------------------------------------
    h.require("the VM boots under a permissive Softnet policy", boot(PERMISSIVE))
    denied = h.probe(f"the guest reaches {DENIED}, which the deny policy will forbid",
                     lambda: reaches(DENIED))
    denied.baseline(want=True,
                    detail="--net-softnet-allow=0.0.0.0/0, which `--help` says disables "
                           "destination restrictions — so the address is reachable when policy allows it")
    h.control("the guest is root-capable", guest("sudo -n id -u").stdout.strip().endswith("0"),
              detail="every attack below runs under sudo; without this the run measures an "
                     "unprivileged user and answers a different question")

    # ---- the mechanism -----------------------------------------------------
    h.require("the VM boots under default-deny", boot(DEFAULT_DENY))
    m_enforced = denied.sample("under --net-softnet-block=0.0.0.0/0 with one /32 allowed")
    h.control("the permitted address still answers under the deny policy", reaches(PERMITTED),
              detail="so the block above is an allowlist and not an outage (A22)")

    iface = guest("ipconfig getifaddr en0; ifconfig en0 | awk '/ether/{print $2}'").stdout.strip().splitlines()
    h.measure("guest en0 before the attacks", " ".join(iface[-2:]) if len(iface) >= 2 else str(iface))

    # ---- attacks, each with its own liveness control ------------------------
    attacks = [
        ("MAC spoof", "sudo -n ifconfig en0 ether 02:11:22:33:44:55"),
        ("static IP reassign", "sudo -n ifconfig en0 inet 192.168.2.99 netmask 255.255.255.0"),
        ("gateway override", "sudo -n route -n add -net 0.0.0.0 192.168.2.1 2>/dev/null; "
                             "sudo -n route -n change default 192.168.2.1"),
    ]
    results = []
    for label, cmd in attacks:
        applied = guest(cmd, timeout=90)
        time.sleep(4)
        still_permitted = reaches(PERMITTED)
        m = denied.sample(f"after: {label}",
                          detail=f"attack exit={applied.returncode}; permitted host still "
                                 f"reachable={still_permitted}")
        if not still_permitted:
            h.measure(f"{label}: INCONCLUSIVE", True,
                      detail="the attack cost the guest its own working destination too, so an "
                             "unreachable denied host here is self-inflicted, not containment")
        else:
            results.append((label, m))
        # put the interface back so the next attack starts from a live network
        guest("sudo -n ipconfig set en0 DHCP", timeout=90)
        time.sleep(6)

    h.expect("Softnet's default-deny actually blocks the denied destination", m_enforced, want=False)
    for label, m in results:
        h.expect(f"a root guest does not get out from under Softnet via {label}", m, want=False)

    h.measure("attacks whose liveness control held, so their result counts", len(results),
              detail=f"of {len(attacks)} attempted")

    stop_vm()

    h.not_tried(
        "**Softnet's dynamic policy channel** — newline-delimited JSON-RPC over a unix socket with "
        "flow-table clearing on change, which is live revocation already built upstream. `tart run` "
        "exposes only boot-time flags and nothing here drives that socket. Whether yoloAI could is "
        "the question that decides if tart gets revocation at all (W8 named this too; still unrun)",
        "attacking the softnet *process* from the guest — it is a host process, and the guest has no "
        "handle on it by construction, so there was nothing to attack. That is the claim, and it is "
        "argued rather than measured",
        "the `@host` alias for gateway traffic, which is W5's credential-injection problem on the "
        "tart side",
        "UDP, ICMP and DNS. Every probe is TCP/80, which is W1's protocol-coverage question unasked "
        "on this backend — and W1 found the pf shape filtered TCP only",
        "IPv6. Softnet's flags are CIDRs and this run never checks whether a v6 destination is "
        "covered at all",
        "whether this composes with yoloAI. `runtime/tart` passes no Softnet flag today",
        "n. One host, one boot per arm, tart 2.32.1 / softnet 0.19.0",
    )

    ok = h.report()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

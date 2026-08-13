#!/usr/bin/env python3
# ABOUTME: C4 of the mac-channel round — names the mechanism that actually makes
# ABOUTME: shape (B) sound on seatbelt, which has no capability bounding set for
# ABOUTME: D139's stated reasoning to rest on.

"""C4: what makes shape (B) sound on seatbelt?

D139 assigns docker, podman and seatbelt shape (B) on the strength of the
`CAP_NET_ADMIN` bounding-set drop — the one vector `agent-privilege-reality.txt`
measured surviving `sudo`. **seatbelt has no bounding set.** It is macOS process
sandboxing, and the agent runs as a host process, so D139's reasoning cannot transfer
even if its conclusion is right. This names the real mechanism and measures it.

Three things have to hold for (B) to mean anything here:

  1. the profile can express "this one socket and nothing else" — otherwise there is
     no (B) to have;
  2. the restriction cannot be relaxed from inside — otherwise the agent lifts it;
  3. it survives `sudo` — because every backend grants the agent root deliberately
     (`Dockerfile:229`), and that is the adversary D137 exists to contain.

The instrument's boundary: this drives `sandbox-exec` directly with hand-written
profiles, not yoloAI's generated one. That is deliberate — the question is what the
*platform* can enforce, which bounds what `runtime/seatbelt/profile.go` could ever
emit. A37 therefore does not apply: no claim here is about the product's behaviour.
`GenerateProfile`'s actual output is read, not run, and cited in the finding.
"""

import os
import subprocess
import sys
import threading
import time
import socket

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "..", "..", "scripts"))
from research_harness_v2 import Harness  # noqa: E402

S = "/tmp/yc1"
SOCK = f"{S}/c4h.sock"
# /tmp is a symlink to /private/tmp, and SBPL matches on the vnode-resolved path.
# An unresolved literal silently matches nothing — the same trap
# backend-idiosyncrasies.md records for subpath rules (DF161/DF162).
SOCK_RESOLVED = f"/private/tmp/yc1/c4h.sock"

BASE_RULES = """(version 1)
(deny default)
(allow process*)
(allow file*)
(allow sysctl-read)
(allow mach*)
(allow signal)
"""

CLIENT = f'''
import socket, sys
try:
    c = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); c.settimeout(5)
    c.connect("{SOCK_RESOLVED}"); c.sendall(b"P"); r = c.recv(64); c.close()
    print("UNIX=" + ("OK" if r == b"PONG" else "ODD"))
except Exception as e:
    print("UNIX=FAIL")
try:
    t = socket.socket(socket.AF_INET, socket.SOCK_STREAM); t.settimeout(5)
    t.connect(("1.1.1.1", 80)); t.close(); print("TCP=OK")
except Exception:
    print("TCP=FAIL")
'''


def serve_forever():
    if os.path.exists(SOCK):
        os.unlink(SOCK)
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.bind(SOCK)
    s.listen(16)
    os.chmod(SOCK, 0o777)
    while True:
        try:
            c, _ = s.accept()
        except OSError:
            return
        try:
            c.recv(64)
            c.sendall(b"PONG")
            c.close()
        except OSError:
            pass


def write_profile(name, body):
    p = f"{S}/{name}.sb"
    with open(p, "w") as f:
        f.write(body)
    return p


def run_client(profile=None, sudo=False):
    with open(f"{S}/c4h_client.py", "w") as f:
        f.write(CLIENT)
    argv = []
    if profile:
        argv += ["sandbox-exec", "-f", profile]
    if sudo:
        argv += ["sudo", "-n"]
    argv += ["/usr/bin/python3", f"{S}/c4h_client.py"]
    p = subprocess.run(argv, capture_output=True, text=True, timeout=90)
    return p.stdout


def tcp_ok(profile=None, sudo=False):
    return "TCP=OK" in run_client(profile, sudo)


def unix_ok(profile=None, sudo=False):
    return "UNIX=OK" in run_client(profile, sudo)


def main():
    h = Harness("C4", "what makes shape (B) sound on seatbelt, which has no capability bounding set?")

    h.require("sandbox-exec is present", subprocess.run(["which", "sandbox-exec"], capture_output=True).returncode == 0)
    os.makedirs(S, exist_ok=True)
    threading.Thread(target=serve_forever, daemon=True).start()
    time.sleep(1)
    h.require("the host-side proxy stand-in is listening", os.path.exists(SOCK), detail=SOCK)

    p_open = write_profile("h_open", BASE_RULES + "(allow network*)\n")
    p_none = write_profile("h_none", BASE_RULES)
    p_b = write_profile("h_b", BASE_RULES + f'(allow network-outbound (literal "{SOCK_RESOLVED}"))\n')
    p_unresolved = write_profile("h_unres", BASE_RULES + f'(allow network-outbound (literal "{SOCK}"))\n')
    write_profile("h_permissive", "(version 1)\n(allow default)\n")

    # ---- 1. can the platform express shape (B) at all? -----------------------
    arm = {"profile": None}
    tcp = h.probe("the sandboxed process reaches the open internet over TCP",
                  lambda: tcp_ok(arm["profile"]))
    unix = h.probe("the sandboxed process reaches the host proxy socket",
                   lambda: unix_ok(arm["profile"]))

    # Each probe is baselined under the arm where ITS OWN mechanism is absent, which
    # is a different arm for each. `tcp` will be claimed to be DENIED, so it baselines
    # where network is fully granted and must read True. `unix` will be claimed to be
    # PERMITTED by the socket literal, so it baselines where that literal is absent
    # and must read False.
    arm["profile"] = p_open
    tcp.baseline(want=True, detail="(allow network*) — the A22 positive control; without it a profile "
                                   "that merely broke python would pass every denial claim below")

    arm["profile"] = p_none
    unix.baseline(want=False, detail="(deny default) with no socket literal — the grant under test "
                                     "is absent, so the proxy socket must be unreachable")
    m_none_tcp = tcp.sample("under (deny default) with no network grant")
    # Recorded, not claimed: v2 forbids an expectation pointing the same way as its
    # own baseline, and rightly — the baseline IS this observation. It matters because
    # it means `--network-none` and shape (B) are different profiles on seatbelt, and
    # it is why C3's seatbelt sandbox could not start.
    m_none_unix = h.measure("unix under (deny default) with no network grant", unix_ok(p_none),
                            detail="so `none` denies the proxy socket too — tmux's own socket is "
                                   "what C3 saw fail")

    arm["profile"] = p_b
    m_b_tcp = tcp.sample("under (deny default) + one socket literal")
    m_b_unix = unix.sample("under (deny default) + one socket literal")

    # The vnode-resolution trap gets its own probe rather than a second sample of
    # `unix`, because its claim points the same way as that probe's baseline. Baselined
    # under the grant written correctly, it is shown answering both ways here.
    arm["profile"] = p_b
    pathgrant = h.probe("the socket literal actually grants access",
                        lambda: unix_ok(arm["profile"]))
    pathgrant.baseline(want=True, detail="the same grant, written vnode-resolved (/private/tmp/...)")
    arm["profile"] = p_unresolved
    m_unres = pathgrant.sample("the same grant, written unresolved (/tmp/...)")

    # ---- 2. can it be relaxed from inside? ----------------------------------
    def nested_reaches(outer):
        """Run a permissive nested sandbox-exec, optionally already inside `outer`."""
        inner = f"sandbox-exec -f {S}/h_permissive.sb /usr/bin/python3 {S}/c4h_client.py"
        argv = (["sandbox-exec", "-f", outer] if outer else []) + ["/bin/sh", "-c", inner]
        return "TCP=OK" in subprocess.run(argv, capture_output=True, text=True, timeout=90).stdout

    outer = {"profile": None}
    nest = h.probe("a nested, fully permissive sandbox-exec reaches the internet",
                   lambda: nested_reaches(outer["profile"]))
    outer["profile"] = None
    nest.baseline(want=True, detail="the identical nested command run from an UNsandboxed parent — so "
                                    "a later refusal is the outer profile holding, not a broken command")
    outer["profile"] = p_b
    m_nest = nest.sample("the same nested command from inside the (B) profile")

    # ---- 3. does it survive sudo? -------------------------------------------
    ticket = subprocess.run(["sudo", "-n", "/usr/bin/true"], capture_output=True).returncode == 0
    h.control("a sudo ticket is active, so the sudo arm measures containment and not authentication",
              ticket,
              detail="without a ticket sudo fails to authenticate and the refusal is free — the "
                     "specimen this round was warned about")
    if ticket:
        elevated = subprocess.run(["sudo", "-n", "/usr/bin/id", "-u"], capture_output=True, text=True).stdout.strip()
        h.control("sudo actually elevated in this arm", elevated == "0", detail=f"uid={elevated}")
        arm["profile"] = p_b
        m_sudo = tcp.sample("under (deny default) + one socket literal, via sudo",
                            detail="still root, still inside the profile")
        h.expect("a root process cannot escape the seatbelt profile", m_sudo, want=False)
    else:
        h.void_arm("sudo", "no sudo ticket: `sudo -n` fails to authenticate, so a refusal here would "
                           "be free and says nothing about containment")

    # ---- claims -------------------------------------------------------------
    h.expect("with no network grant, seatbelt denies TCP", m_none_tcp, want=False)
    h.expect("seatbelt can express shape (B): the proxy socket is reachable", m_b_unix)
    h.expect("...while the open internet is not", m_b_tcp, want=False)
    h.expect("an unresolved /tmp literal grants nothing, silently", m_unres, want=False)
    h.expect("the profile cannot be relaxed from inside by nesting a permissive one",
             m_nest, want=False)

    h.not_tried(
        "yoloAI's generated profile. `GenerateProfile` emits `(allow network*)` or nothing "
        "(`profile.go:369`); it has no shape-(B) form to test, which is the finding rather than a gap "
        "in this run",
        "whether the agent can make the *host-side* proxy do its bidding — this measures reachability, "
        "not what the proxy then permits, which is egress-proxy-build.md's subject",
        "SBPL's `network-bind`, and whether a sandboxed process can listen. Shape (B) as specified only "
        "needs outbound",
        "a profile written by an adversary rather than by us. sandbox-exec reads the profile *before* "
        "dropping in, so a compromised agent cannot supply one — but nothing here tests whether the "
        "agent can influence the file between generation and exec, which is a real TOCTOU question",
        "docker and podman, which D139 assigns (B) in the same breath and which do have a bounding set",
        "n. One host, macOS 26.5.1",
    )

    ok = h.report()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

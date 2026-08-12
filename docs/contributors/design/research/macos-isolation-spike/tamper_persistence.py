#!/usr/bin/env python3
# ABOUTME: Does a tampered guest come back UNENFORCED at the next start — and would simply
# ABOUTME: overwriting our own scripts fix it? Arm A tampers the script we would overwrite;
# ABOUTME: arm B leaves it pristine and tampers the binary it calls.

"""If we blast our scripts over the top on every start, is tampering still a problem?

**Post-round, owner-prompted.** Round 2 is closed; this is not one of its items.

Two things were established by hand earlier in the session and neither is the claim that
matters: `/yoloai/bin/firewall.py` is writable by a root agent, and the edit survives a
stop/start. **Writable and persistent is not the same as unenforced**, and the whole
manifest-and-overwrite discussion rests on the step nobody had taken.

So the run does two things at once, because the second is the owner's actual question:

- **Arm `script`** — the agent neuters the start script itself. This is the case an
  overwrite-on-every-start would fix, and it is measured so that the fix has something to
  be measured against.
- **Arm `toolchain`** — the start script is left **pristine**, and the agent neuters
  `iptables` instead, by shadowing it with a wrapper that exits 0 and does nothing. Nothing
  in an overwrite-our-own-files policy touches this. If the sandbox comes back unenforced
  here, then overwriting is **not sufficient** and the residue is the interpreter and the
  toolchain — which is exactly the regress the owner named.

**The start sequence lives in the rootfs and is invoked by reference**, which is what makes
the tampering meaningful: the container's command is `sh /yoloai/bin/ybstart.sh`, so the
*invocation* is host-controlled and the *content* is in the guest's writable layer — the same
shape as the image's real `ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]`.

**Tampering is done through `container exec`**, which holds full capabilities (W6b). That is
faithful rather than a shortcut: writing a file needs **no capability at all**, so a dropped
in-guest agent can do exactly this. The capability question and the tampering question are
independent, which is the point.

**The baseline is the untampered restart.** A sandbox that comes back unenforced for some
unrelated reason would make every arm free, so the same probe must be seen reporting
*enforced* first, on the same container, across the same stop/start cycle.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded.

Run it as: `python3 tamper_persistence.py`
"""

from __future__ import annotations

import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base:latest"
BOX = "ybtamper"
ALLOW = "1.1.1.1"
DENY = "1.0.0.1"
STATE: dict[str, object] = {}
MODE = {"as": "root"}
GUEST_USER = "yoloai"

# What the product's start path does, reduced to its enforcing core. Lives in the rootfs so
# the guest can edit it, exactly like firewall.py does.
START = f"""#!/bin/sh
ipset create -exist allowed-domains hash:net
ipset flush allowed-domains
ipset add -exist allowed-domains {ALLOW}
iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT
iptables -P OUTPUT DROP
"""


def _q(argv: list[str], timeout: int = 240) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(script: str) -> str:
    return _q(["container", "exec", BOX, "sh", "-c", script]).stdout.strip()


def cleanup() -> None:
    _q(["container", "stop", BOX])
    _q(["container", "rm", BOX])


def wait_up(limit: int = 60) -> bool:
    for _ in range(limit):
        if guest("echo ok") == "ok":
            return True
        time.sleep(1)
    return False


def restart() -> bool:
    _q(["container", "stop", BOX])
    time.sleep(2)
    _q(["container", "start", BOX])
    return wait_up()


def can_replace_start_script() -> bool:
    """Can the current principal end up with a start script of its choosing?

    Deliberately not "can it write the file": the file may be root-owned and unwritable
    while the DIRECTORY is agent-owned, and unlink+create reaches the same place. Reporting
    the append refusal alone would have said the non-root agent was contained.
    """
    who = MODE["as"]
    pre = "" if who == "root" else f"gosu {who} "
    appended = guest(f"{pre}sh -c '(echo x >> /yoloai/bin/ybstart.sh) 2>/dev/null "
                     "&& echo yes || echo no'")
    replaced = guest(f"{pre}sh -c '(rm -f /yoloai/bin/ybstart.sh && "
                     "touch /yoloai/bin/ybstart.sh) 2>/dev/null && echo yes || echo no'")
    STATE["how"] = f"append={appended}, unlink+create={replaced}"
    return appended == "yes" or replaced == "yes"


def comes_back_unenforced() -> bool:
    """THE probe, phrased as the NEGATIVE so its baseline can answer the other way.

    The findings here are failures — a tampered guest returning without a policy — so that
    is what has to be claimed, and a claim needs a baseline that came out opposite. The
    untampered restart supplies it. Written the natural way round the run refused to render,
    correctly, because baseline and claim both wanted "enforced".

    Behavioural: whether the rules 'look' installed is not the property, and a sandbox whose
    start script silently did nothing looks identical to one that never had a policy.
    """
    denied = guest(f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' http://{DENY}/ "
                   "2>/dev/null; echo")
    allowed = guest(f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' http://{ALLOW}/ "
                    "2>/dev/null; echo")
    STATE["denied"], STATE["allowed"] = denied, allowed
    return not (denied in ("", "000") and allowed not in ("", "000"))


def main() -> int:
    h = Harness("TAMPER", "Does a tampered guest come back unenforced, and would overwriting "
                          "our own scripts fix it?")
    try:
        cleanup()
        # The command is a REFERENCE to a rootfs path, so its content is guest-writable while
        # the invocation is not — the same shape as the image's real ENTRYPOINT.
        _q(["container", "create", "--name", BOX, "--cap-add", "NET_ADMIN",
            "--entrypoint", "/bin/sh", IMAGE, "-c",
            "sh /yoloai/bin/ybstart.sh 2>/dev/null; sleep 900"])
        _q(["container", "start", BOX])
        h.require("the sandbox came up", wait_up())

        guest(f"cat > /yoloai/bin/ybstart.sh <<'EOF'\n{START}EOF\nchmod +x /yoloai/bin/ybstart.sh")
        h.require("the start script is in place",
                  guest("test -x /yoloai/bin/ybstart.sh && echo yes") == "yes")
        h.measure("is the toolchain writable by the guest at all?",
                  guest("test -w $(command -v iptables) && echo yes || echo no"),
                  "the premise of arm B — if this were 'no', that arm could not exist")

        # ---- Ownership: does running the agent NON-ROOT protect our own files? ------
        # The obvious reading of "run the agent as yoloai" is that root-owned files become
        # untouchable. That is true of the FILES and false of the DIRECTORY: Dockerfile:276
        # does `chown -R yoloai:yoloai /yoloai`, so /yoloai/bin belongs to the agent's own
        # user, and write permission on a directory permits unlink+create whatever the
        # file's own mode says. entrypoint.py:117 chowns the files themselves at runtime,
        # which removes even that fig leaf.
        h.measure("ownership of what the start path depends on",
                  guest("ls -ld /yoloai/bin | awk '{print $1, $3\":\"$4}'; "
                        "ls -l $(command -v iptables) | awk '{print $1, $3\":\"$4}'"))

        replace_probe = h.probe("the agent can replace the start script",
                                can_replace_start_script)
        MODE["as"] = "root"
        replace_probe.baseline(want=True, detail="a root agent obviously can — this is what "
                                                 "makes the non-root answer meaningful")
        MODE["as"] = GUEST_USER
        nonroot = replace_probe.sample("as the non-root agent user")
        h.measure("how the non-root user managed it", STATE.get("how"),
                  "append vs unlink+create — the second is the one the directory permits")
        h.expect(
            "running the agent as a non-root user protects our own start path from being "
            "replaced",
            nonroot, want=False,
        )
        guest("cat > /yoloai/bin/ybstart.sh <<'RESTORE_EOF'\n" + START + "RESTORE_EOF\n"
              "chmod +x /yoloai/bin/ybstart.sh")
        h.control("the start script was restored before the restart arms",
                  guest("grep -c 'match-set allowed-domains' /yoloai/bin/ybstart.sh") == "1",
                  "so those arms measure tampering and not this one's leftovers")

        probe = h.probe("the sandbox comes back UNENFORCED after a restart",
                        comes_back_unenforced)

        # -- BASELINE: untampered restart. The rules must actually take. ---------------
        h.require("the untampered restart succeeded", restart())
        probe.baseline(want=False)
        h.measure("baseline: denied / allowed", f"{STATE['denied']} / {STATE['allowed']}",
                  "the start script runs from the rootfs and enforces, so a later failure "
                  "is the tampering and not the rig")

        # -- ARM A: tamper the script we would overwrite --------------------------------
        guest("printf '#!/bin/sh\\nexit 0\\n' > /yoloai/bin/ybstart.sh")
        h.require("arm A restart succeeded", restart())
        a = probe.sample("start script neutered")
        h.measure("[script] denied / allowed", f"{STATE['denied']} / {STATE['allowed']}",
                  arm="script")
        h.expect("a tampered start script leaves the sandbox UNENFORCED at the next "
                 "start — the hazard an overwrite-on-every-start policy exists to fix",
                 a, want=True, arm="script")

        # -- ARM B: restore the script; tamper the binary it CALLS ----------------------
        # This is the overwrite-on-every-start policy, simulated exactly: our file is
        # pristine, and nothing in that policy touches what it invokes.
        guest(f"cat > /yoloai/bin/ybstart.sh <<'EOF'\n{START}EOF\nchmod +x /yoloai/bin/ybstart.sh")
        real = guest("command -v iptables")
        guest(f"cp {real} /tmp/iptables.real 2>/dev/null; "
              f"printf '#!/bin/sh\\nexit 0\\n' > {real}; chmod +x {real}")
        h.control("the start script is byte-identical to the clean one for arm B",
                  guest("grep -c 'match-set allowed-domains' /yoloai/bin/ybstart.sh") == "1",
                  "so arm B measures the toolchain and not a leftover from arm A — this is "
                  "the overwrite policy, applied")
        h.require("arm B restart succeeded", restart())
        b = probe.sample("start script pristine, iptables shadowed")
        h.measure("[toolchain] denied / allowed", f"{STATE['denied']} / {STATE['allowed']}",
                  arm="toolchain")
        h.expect("with our script pristine, a tampered TOOLCHAIN still leaves it "
                 "UNENFORCED — i.e. overwriting our own files is NOT sufficient",
                 b, want=True, arm="toolchain")

        h.not_tried(
            "the product's real start path. This is a reduced start script standing in for "
            "`entrypoint.py` plus `install-firewall.py`; whether those fail loudly on a "
            "broken `firewall.py` — which would make arm A less severe than it looks — is "
            "not measured here and should be, because it changes how urgent the overwrite is",
            "the interpreter. Arm B shadows `iptables`; `python3`, `ipset`, libc and the "
            "dynamic linker are the same class and are not exercised. The arm is an "
            "existence proof for the class, not a survey of it",
            "whether the OVERWRITE itself can be trusted to run. It has to happen before "
            "anything guest-authored executes, and on apple `container cp` requires the "
            "container to be RUNNING — which means something already started. Whether that "
            "something can be a host-chosen binary rather than a guest-writable one is the "
            "next question and this run does not open it",
            "detection. Nothing here computes or compares a hash; the manifest proposal is "
            "what this run supplies evidence FOR, not something it tests",
            "a read-only mount of /yoloai/bin, which would remove arm A entirely without "
            "any overwrite or hash. `m.ReadOnly` exists in the apple runtime and nothing "
            "here tries it",
            "whether making /yoloai/bin root-owned would hold. The ownership arm shows the "
            "agent-owned DIRECTORY is what defeats non-root; it does not verify that a "
            "root-owned directory with a root-owned parent actually closes it, and that is "
            "the fix that ought to be tested before it is adopted",
            "podman and containerd, and any non-Debian base",
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

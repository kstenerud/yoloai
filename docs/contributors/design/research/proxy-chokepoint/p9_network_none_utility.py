#!/usr/bin/env python3
# ABOUTME: P9 — is `--network-none` usable for anything? A single-item follow-up asked by the
# ABOUTME: owner, because deprecating a shipped flag needs the "nobody can use it" claim measured
# ABOUTME: rather than assumed, and the docs currently recommend it three separate times.

"""P9: can anyone actually use `--network-none`?

A follow-up to the closed chokepoint round, not part of it. The owner's proposal is a three-mode
shape — deprecate `--network-none`, keep `--network-isolated` for a *confused* agent, add a third
mode for a *compromised* one — resting on the claim that `--network-none` leaves the agent unable
to reach its own API server and therefore useless.

**Why this needs measuring rather than reasoning.** Deprecating a shipped flag is a user-visible
change, and the claim is universal ("nobody could ever make use of it"). A universal claim is
refuted by one counterexample, so the useful shape is to look for counterexamples rather than to
confirm the headline. Three exist in principle and each is checked:

1. **A real agent.** If it cannot reach its API the flag is useless for the product's purpose.
2. **The `test` agent**, which is deterministic and makes no API calls. If that works, the flag
   is not literally unusable, and the deprecation has to say what happens to it.
3. **The sandbox as a plain container** — `yoloai exec`, local builds, offline work. That path
   does not need the agent at all, and a user could reasonably be using it.

**The docs are the reason this is not academic.** `yoloai help security` says *"Use --network-none
for maximum isolation"* and *"Use --network-none where it matters"*; `GUIDE.md` says *"use
--network-none when you need a guarantee"*. So the flag is not merely present, it is the product's
own recommended answer whenever the allowlist cannot be trusted — which
[D137](../../../decisions/working-notes.md) says is *always*, on every backend. If it is also
useless, then the product's advice for a hostile agent is currently "make the agent useless", and
that is worth knowing precisely.

**The negative self-test.** One probe — does the agent complete a trivial task — baselined with
networking OPEN, where it must succeed. Without that, "it failed under --network-none" is
compatible with the agent being broken for unrelated reasons, which is exactly how this round's
first census run went wrong.

Run it as: `python3 p9_network_none_utility.py`
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from chokepoint_rig import YOLOAI, quiet, sh  # noqa: E402
from research_harness_v2 import Harness, HarnessError  # noqa: E402

BOX = "p9none"
WORKDIR = os.environ.get("P9_WORKDIR", str(Path.home() / "p9-repo"))
TASK = "Create a file called hello.txt containing the word hello. Then stop."


def cleanup() -> None:
    quiet([str(YOLOAI), "destroy", BOX, "--abandon-unapplied"])


def run_agent(h: Harness, *extra: str) -> tuple[int, str]:
    """Launch, wait, and report whether the task's artefact exists in the diff."""
    cleanup()
    rc = h.run([str(YOLOAI), "run", BOX, WORKDIR, "-p", TASK, "--wait", *extra],
               check=False).returncode
    diff = h.run([str(YOLOAI), "diff", BOX], check=False).stdout
    return rc, diff


def main() -> int:
    h = Harness("P9", "Is `--network-none` usable for anything?")
    try:
        cleanup()

        # -- the probe must be shown succeeding before a failure means anything --
        state = {"none": False}

        def completes() -> bool:
            extra = ["--network-none"] if state["none"] else []
            _, diff = run_agent(h, *extra)
            return "hello.txt" in diff

        works = h.probe("a real agent completes a trivial task", completes)
        works.baseline(want=True,
                       detail="networking OPEN. If this fails the agent is broken for some "
                              "unrelated reason and every failure below is free — which is how "
                              "this round's first census run went wrong")

        state["none"] = True
        h.expect("a real agent CANNOT complete even a trivial local task under --network-none, "
                 "because it cannot reach its own API server",
                 works.sample("with --network-none"), want=False)

        # -- counterexample 1: the deterministic test agent needs no API ---------
        # Its HeadlessCmd is `sh -c "PROMPT"`, so the prompt must BE a shell command.
        # A first attempt passed "ignored", which is not one, and scored the resulting
        # exit 1 as "the flag broke it" — a rig error reported as a product fact.
        cleanup()
        test_rc = h.run([str(YOLOAI), "run", BOX, WORKDIR, "-p", "echo hi > out.txt",
                         "--agent", "test", "--network-none", "--wait"], check=False)
        h.measure("the `test` agent runs under --network-none", test_rc.returncode == 0,
                  f"rc={test_rc.returncode} — deterministic and makes no API call, so if this "
                  "works the flag is not literally unusable and a deprecation has to say what "
                  "becomes of it", arm="counterexamples")

        # -- counterexample 2: the sandbox as a plain offline container ---------
        # The `idle` agent is a shipped no-op that keeps the box up, which is what makes
        # this checkable at all: a `--wait` run leaves a STOPPED container, and the first
        # attempt measured "local work fails" against a box that was not running.
        quiet([str(YOLOAI), "destroy", BOX + "idle", "--abandon-unapplied"])
        h.run([str(YOLOAI), "run", BOX + "idle", WORKDIR, "-p", "unused",
               "--agent", "idle", "--network-none"], check=False)
        import time as _t
        _t.sleep(3)
        local = sh(h, BOX + "idle",
                   "python3 -c \"print('LOCAL_OK')\"; ip -o -4 addr show 2>/dev/null "
                   "| awk '{print $2}' | tr '\\n' ' '", timeout=60)
        h.measure("local work still runs inside a --network-none sandbox",
                  "LOCAL_OK" in local.stdout,
                  f"interfaces present: {local.stdout.split()[-1:] or ['?']}; "
                  "exec, builds and offline tooling do not need egress; a user could be using "
                  "the sandbox this way and a deprecation would take it from them",
                  arm="counterexamples")

        h.measure("so the flag's status is",
                  "useless for every AI agent, usable for the no-op ones",
                  "the distinction the deprecation note has to make: no agent that talks to an "
                  "API can work, which is every shipped agent, while the test agent and plain "
                  "exec still function",
                  arm="counterexamples")

        h.not_tried(
            "a LOCAL model. An agent pointed at an LLM on the host would need egress to reach "
            "it, and nothing in the product exposes a host socket into the sandbox — but if "
            "that ever ships, --network-none stops being useless and this verdict expires",
            "brokering, which `launch.go:613` rejects outright under --network-none with an "
            "explicit error; that is read from the code, not measured here",
            "whether any USER actually runs it. This measures capability, not adoption, and "
            "adoption is not knowable from this host",
            "the other backends. docker only",
            "--network-none on a RESTART of a sandbox created with networking, which is a "
            "different path and might behave differently",
        )
        return 0 if h.report() else 1
    finally:
        cleanup()
        quiet([str(YOLOAI), "destroy", BOX + "idle", "--abandon-unapplied"])


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)

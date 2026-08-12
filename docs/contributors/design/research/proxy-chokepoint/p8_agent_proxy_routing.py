#!/usr/bin/env python3
# ABOUTME: P8 — when the proxy is the only path out, do the OTHER shipped agents take it? Prior
# ABOUTME: art says Codex disables proxy env inside its own sandbox and Aider ignores it without
# ABOUTME: a flag; under a chokepoint that is not a quirk, it is an agent that cannot work.

"""P8: is a chokepoint agent-agnostic, or is it silently Claude-only?

P2 showed a Claude session completing its work with one destination. Claude honours proxy
environment variables by documentation, so that result may say more about Claude than about the
design. [`agent-proxy-support.md`](../agent-proxy-support.md) surveyed the other four and found
two hazards that matter far more under a chokepoint than they did when it was written:

* **Codex** preserves reqwest's default env-proxy behaviour but **`.no_proxy()`s inside its own
  sandbox**, and coverage is inconsistent across call sites.
* **Aider**'s default `aiohttp` transport ignores env proxies entirely without
  `DISABLE_AIOHTTP_TRANSPORT=True`.

When egress is merely *filtered*, ignoring the proxy means the traffic goes direct and works.
When the proxy is the only route, ignoring it means the agent does not work at all. So the same
fact changes from a footnote to a gate, and that is what this measures.

**The discriminator, and why it needs no credentials.** The question is not whether an agent can
authenticate — it is **which path its request takes**. A request that reaches the proxy appears
in the proxy log by name, in the clear, before any TLS. A request that does not appears as
packets on the chokepoint's deny counter. Both are observable with a dummy key, and an
authentication failure at the far end is irrelevant to the question. So this costs no
credentials and no LLM spend for any of the four.

**Attribution is the hard part, and run 2 was voided for getting it wrong.** The sandbox has a
live Claude agent in it, requests are asynchronous, and a previous agent's connections trail into
the next agent's window. Run 2 scored gemini as *"took the proxy"* on targets
(`api.openai.com`, `chatgpt.com`) that were plainly codex's, while gemini's own output showed it
had refused to run at all. **4 of 4, and wrong** — the shape D136 §3 names, where the bad run
agrees with you. So a verdict now requires a new target matching **that agent's own provider**,
and hosts the resident Claude agent contacts are excluded by name.

**One thing the deny counter cannot do**, and it is why the proxy log is the authority: a
non-proxy-aware tool tries **DNS first**, and DNS is refused by the chokepoint, so the counter
moves for any tool that merely resolves. Counter movement therefore does not demonstrate a
bypass attempt. Absence from the proxy log does.

**Instrument boundary.** Inside the region: the proxy log and the deny counter, sampled per
agent. Scaffolding: dummy credentials, and a per-agent invocation chosen to provoke exactly one
network call. Not measured: whether any agent would *complete* a task — only which route it
takes.

Run it as: `python3 p8_agent_proxy_routing.py`
"""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from chokepoint_rig import (  # noqa: E402
    PROXY_ENV, PROXY_HOST, PROXY_PORT, YOLOAI, assert_choked, counter, host_veth,
    install_chokepoint, proxy_lines, quiet, sh_proxied, start_proxy, targets_seen,
    teardown,
)
from research_harness_v2 import Harness, HarnessError  # noqa: E402

BOX = "p8agents"
TABLE = "yb_p8"
WORKDIR = os.environ.get("P8_WORKDIR", str(Path.home() / "p8-repo"))

# Each provokes exactly one outbound attempt with a dummy credential. The point is the
# route, so a 401 at the far end is a success for this measurement.
# Hosts the sandbox's OWN resident Claude agent contacts. Anything here is not evidence
# about the agent under test, whichever window it lands in.
RESIDENT = ("anthropic.com", "claude.ai", "datadoghq.com")

AGENTS = {
    "codex": ("OPENAI_API_KEY=sk-dummy codex exec 'say hi' 2>&1 | tail -2",
              ("openai.com", "chatgpt.com")),
    # --yolo: without it gemini refuses an untrusted folder headlessly and never
    # reaches the network, which would score as a proxy result and is not one.
    "gemini": ("GEMINI_API_KEY=dummy gemini --yolo -p 'say hi' 2>&1 | tail -2",
               ("google", "googleapis.com", "gstatic.com")),
    # the image seeds an EMPTY ~/.aider.conf.yml, which aider rejects before doing
    # anything; removing it is rig hygiene, not a thumb on the scale.
    "aider": ("rm -f ~/.aider.conf.yml; OPENAI_API_KEY=sk-dummy aider --no-git --yes "
              "--message hi --model gpt-4o-mini 2>&1 | tail -2",
              ("openai.com", "pypi.org", "litellm")),
    "opencode": ("OPENAI_API_KEY=sk-dummy opencode run 'say hi' 2>&1 | tail -2",
                 ("openai.com", "opencode.ai")),
}


def main() -> int:
    h = Harness("P8", "With the proxy as the only path out, do the other agents take it?")
    proxy = None
    try:
        teardown(BOX, TABLE)
        proxy = start_proxy()
        h.control("the proxy stand-in is listening", proxy.poll() is None,
                  f"{PROXY_HOST}:{PROXY_PORT}")

        started = h.run([str(YOLOAI), "run", BOX, WORKDIR,
                         "-p", "Do nothing at all. Exit immediately without using any tools.",
                         *sum((["--env", e] for e in PROXY_ENV), []), "--tty"], check=False)
        h.control("the sandbox launched via the product's own launch path",
                  started.returncode == 0, f"rc={started.returncode}")

        dev = host_veth(h, BOX)
        h.control("the sandbox's host-side veth was found", bool(dev), f"dev={dev!r}")
        h.control("the chokepoint rule loaded", install_chokepoint(h, dev, TABLE),
                  "one destination permitted, DNS deliberately not")

        choked = h.probe("the chokepoint refuses an ordinary HTTPS destination",
                         lambda: assert_choked(h, BOX))
        choked.baseline(want=True,
                        detail="proxy unset. If False, there is no chokepoint and every "
                               "'took the proxy' below is free")

        # Claude is the positive control: P2 already showed it working, so if it does not
        # appear here the rig is wrong rather than the agents being interesting.
        before = proxy_lines()
        sh_proxied(h, BOX, "curl -s -o /dev/null -m 20 https://api.anthropic.com/ || true",
                   timeout=60)
        h.control("a known-cooperative client reaches the proxy in this rig",
                  proxy_lines() > before,
                  "curl is the stand-in for the agent P2 already proved; without this a row "
                  "of NOs below would be the rig's fault and would read as a finding")

        verdicts: dict[str, str] = {}
        for name, (script, providers) in AGENTS.items():
            time.sleep(8)  # let the previous agent's async connections finish landing
            seen_before = targets_seen()
            deny_before = counter(h, TABLE, "chokepoint")
            out = sh_proxied(h, BOX, script, timeout=180)
            time.sleep(5)
            raw_new = targets_seen() - seen_before
            deny_moved = counter(h, TABLE, "chokepoint") - deny_before
            # attributable = new, not the resident agent's, and this agent's own provider
            mine = {t for t in raw_new
                    if not any(r in t for r in RESIDENT)
                    and any(pv in t for pv in providers)}

            if mine:
                verdict = "took the proxy"
            elif deny_moved > 0:
                verdict = "tried to leave without it"
            else:
                verdict = "never got as far as the network"
            verdicts[name] = verdict
            h.measure(f"{name}", verdict,
                      f"attributable: {sorted(mine) or 'none'}; other new traffic in the "
                      f"window: {sorted(raw_new - mine) or 'none'}; deny +{deny_moved}; "
                      f"tail={out.stdout.strip()[-140:]!r}", arm="agents")

        took = [k for k, v in verdicts.items() if v == "took the proxy"]
        bypassed = [k for k, v in verdicts.items() if v == "tried to leave without it"]
        never = [k for k, v in verdicts.items() if v == "never got as far as the network"]
        h.measure("agents that routed through the proxy", f"{len(took)} of {len(AGENTS)}",
                  f"{sorted(took) or 'none'}")
        h.measure("agents that made no network attempt at all", str(sorted(never)) or "none",
                  "these say NOTHING about proxying. Each failed on configuration before "
                  "reaching a request, and counting them as evidence either way would be the "
                  "inference-overreach class — an unrun tool is not a compliant one")
        h.measure(
            "so, on the agents that actually reached the network, a chokepoint is",
            "agent-agnostic" if not bypassed
            else f"NOT agent-agnostic — {sorted(bypassed)} tried to leave without it",
            "prior art predicted Codex and Aider would be the problems; this records what they "
            "did rather than what the survey said they would, and refuses to score the two "
            "that never got that far",
            arm="agents")

        h.not_tried(
            "whether any of them would COMPLETE a task. Dummy credentials, so every request "
            "fails at the far end; this measures the ROUTE and nothing else",
            "the per-agent remedies prior art names — DISABLE_AIOHTTP_TRANSPORT for Aider, the "
            "CA variables for Codex. If an agent failed here, whether its documented knob fixes "
            "it under a chokepoint is a separate question",
            "Codex's OWN sandbox, which is where prior art says it disables proxy env. Whether "
            "this invocation entered that path is not established",
            "base_url brokering, which is how the product actually points an agent at an "
            "endpoint and which bypasses the proxy-env question entirely for the LLM call — "
            "the agents here were run bare, not as yoloAI launches them",
            "version pinning. Every agent is whatever the image ships today, and prior art "
            "warns this is a fast-moving snapshot",
            "repeat runs. n=1 per agent",
        )
        return 0 if h.report() else 1
    finally:
        if proxy is not None:
            proxy.terminate()
        teardown(BOX, TABLE)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)

> **ABOUTME:** Session handoff for the macOS host-`pf` workstream, written immediately before a
> deliberate reboot that ends the authoring session's context. Read this first if you are picking
> the work up cold; it records the state, the decisions and — most importantly — what NOT to redo.

# Handoff: macOS host-`pf` enforcement, mid-reboot-test

**Written 2026-08-04, immediately before a reboot.** The reboot is itself the experiment. The agent
that wrote this will not remember any of it.

## Do this first

```
sudo bash docs/contributors/design/research/macos-isolation-spike/reboot_post.sh
```

`reboot_pre.sh` ran before the restart: it installed a narrow `NOPASSWD` grant, a root-owned
`/etc/yoloai/pf-pool.conf`, and a live pf anchor with working enforcement, then snapshotted
everything to `results/reboot-snapshot.txt`. The post half compares live state against that
snapshot and **removes all three**. Until it runs, the machine carries:

```
/etc/sudoers.d/yoloai-reboot-probe
/etc/yoloai/pf-pool.conf
pf anchor com.apple/yoloai_rb
```

If the post half is never going to run, remove them by hand:

```
sudo rm -f /etc/sudoers.d/yoloai-reboot-probe && sudo rm -rf /etc/yoloai \
  && sudo pfctl -a com.apple/yoloai_rb -F all
```

## What the workstream is

`--network-isolated` on macOS is weak (apple grants the guest `NET_ADMIN`, so the agent can flush
its own rules — DF179) or absent (tart, seatbelt refuse it). The fix is to enforce from **host `pf`**,
which needs root, which yoloAI has no path to. The plan is
[`design/plans/macos-pf-privileged-path.md`](../../plans/macos-pf-privileged-path.md). Status
PLANNED, nothing built.

## The decision, and why — this is the expensive part to re-derive

**Opt-in = a generated `NOPASSWD` sudoers grant.** Not a flag, not an isolation mode, not a runtime
prompt. Teardown has no tty (stop, signal handlers, MCP calls), so the privileged call cannot prompt,
so `NOPASSWD` is forced, so **the authorized command set is the security boundary**.

**Mechanism = a fixed slot pool: static rules, dynamic pf table membership.** The alternative —
per-sandbox sub-anchors loaded from stdin — was recommended for two days and then rejected on
measurement:

- A grant that can load a ruleset **can void all filtering**: `pass in quick all` took another
  sandbox from blocked to reachable (`pf-shapea2.txt` C2). A table-membership grant cannot express
  that; nine bypasses were tried and all refused (`pf-shapeb.txt` B3).
- Sub-anchor reaping must name orphans `yoloai-<principal>-<name>`, the ambiguous form
  `runtime/orphan.go` forbids (DF19/DF115/DF125). Table membership is identified by **address**.
- **The sub-anchor design's supposed advantage was not real.** The original argument was
  "per-sandbox allowlists need per-sandbox rulesets, therefore dynamic loading". Two slots give two
  sandboxes fully independent allowlists (`pf-shapeb.txt` B2). The cap is the only real difference.

**Do not re-open this on the "no cap" argument alone.** That was the argument, it was tested, and it
lost.

## Things that are settled — do not re-measure

- `block drop in quick` **enforces**; `block drop out` loads cleanly and **filters nothing** (pf NATs
  outbound). Both candidates in one run: `pf-enforce.txt` E1.
- 32 slots load; a high slot index enforces; **table contents survive a ruleset reload**;
  an empty `dst` fails **closed** — `pf-assumptions.txt` D1/D2/D4.
- **A stale entry does not merely block — it grants the stale slot's allowlist.** Not a slot-pool
  cost: an orphaned sub-anchor does the same (`pf-assumptions.txt` D3, `pf-stale-a.txt` SA2). This is
  why reaping is a security requirement.
- **Membership without rules fails open silently** (`pf-assumptions.txt` D6). Hence mandatory VERIFY.
- The four-line grant validated as a unit, with a detector that separates `refuse-by-policy` from
  `ran-but-failed` (`pf-assumptions.txt` D5/D7).
- **`sandbox-exec` denies exec of setuid binaries** regardless of `(allow process-exec)`, so a
  seatbelt-contained agent cannot reach `sudo` or the grant.
- `load anchor` is **inert under `-a`** — `pfctl` never opens the file.
- sudoers: globs unsafe (args match as one concatenated string), regex safe, **no escaping**, and
  **`visudo -c` cannot tell safe from unsafe**.

## What the reboot settles

P1/P2 do anchor rules and table contents survive? (the plan's largest asserted-not-measured premise)
· P3 is pf still enabled — it is reference-counted and not auto-enabled, so if nothing re-enables it
every rule is inert · P4 main ruleset identical? · P5 do the files survive? · P6 do vmnet bridges
return on the same subnets? · P7/P8 do sandboxes and their **addresses** survive? · P9 unattended
recovery after a *real* reboot.

**If P1 says the rules survived**, the plan is wrong in an interesting direction: reboot recovery,
the pinned-file grant row and part of VERIFY exist for a problem that does not occur. Update the
plan rather than defending it.

## Still open — decisions, not research

Pool size and exhaustion behaviour · the slot-allocation lock (cross-sandbox; per-sandbox `flock`s
already exist via `store.AcquireLock`) · host/guest DNS resolution parity · whether yoloAI holds its
own `pfctl -E` reference (**deliberately untested** — a wrong `-X` breaks vmnet NAT for every VM).

Known gaps: the slot pool has been run on **apple only**; renewal *after* a subnet re-pick is
untested; seatbelt is parked ([`seatbelt-host-pf-enforcement.md`](../../plans/seatbelt-host-pf-enforcement.md)).

**Blocking dependency:** rules must precede the agent's first packet, but apple and tart have no
host-side step between guest boot and agent exec — `entrypoint.py` execs the agent itself, and only
docker sets `AgentFreeLaunch`. `host-controlled-agent-launch.md` is a declared dependency and is
`PLANNED` with no code.

## How this work went wrong, repeatedly

Recorded properly as **A28** and **A29** in [`agent-failures.md`](../../../agent-failures.md). The
short version, because it will happen again:

- **Ten instrumentation bugs** produced verdicts that could not have come out otherwise — a shell
  idiom for "default on failure" colliding with a command that already emits a value on failure.
  Before trusting a verdict, **construct the input that should produce the opposite one**.
- **Five flat claims about this codebase that were false or unsourced**, two of which were used to
  reject a design alternative. **A claim about what the code does gets a `file:line`, or it gets
  hedged.**

Three independent audit agents caught what self-review did not, and the discriminating behaviour was
that they *reproduced* idioms and *opened* files rather than reading and reasoning. If you are about
to write "this is safe because X", check whether you have run X.

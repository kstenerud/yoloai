> **ABOUTME:** Session handoff for the macOS host-`pf` workstream, written across a deliberate reboot
> that ended the authoring session's context. Read this first if you are picking the work up cold; it
> records the state, the decisions and — most importantly — what NOT to redo.

# Handoff: macOS host-`pf` enforcement, reboot test

**Round 1 ran 2026-08-04 and is complete** — `results/reboot-post.txt`, pass=8 fail=0 unknown=3, and
the machine was cleaned up. It answered the plan's largest premise and left three questions open, all
for the same reason: after a reboot nothing is running, and the harness measured an idle host. **Round
2 is a rerun of the same two scripts, with the guests restarted before the live half.** It is set up
and not yet run; nothing is currently installed on the machine.

## Whether to run round 2 at all

It settles four things, and all four are downstream of a *running* guest — so the only way to get
them is another restart:

- Can an apple sandbox that came back **`removed`** be restarted? Round 1 found both of them in that
  state, which is stronger than "its address went stale": recovery may have no sandbox to re-add an
  address for. The plan does not currently account for it.
- Do the vmnet bridges return on the same subnets and indices (DF172)? Round 1's P6 verdict was an
  artifact and is withdrawn.
- Does a guest come back on the same address? Round 1's tart PASS is withdrawn — it read a surviving
  `dhcpd_leases` record for a stopped VM.
- Does enforcement filter again **end to end** after a real reboot, in two slots with different
  allowlists? Round 1 restored the pool and stopped there.

If none of that is worth a restart, the plan already has what it most needed; say so and leave it.

## Running it

```
mkdir -p ~/yoloai-reboot-test/repo && git init ~/yoloai-reboot-test/repo
yoloai new --backend apple rb-a ~/yoloai-reboot-test/repo
yoloai new --backend apple rb-b ~/yoloai-reboot-test/repo
yoloai new --backend tart  rb-t ~/yoloai-reboot-test/repo   # optional 3rd, covers tart too
sudo bash docs/contributors/design/research/macos-isolation-spike/reboot_pre.sh rb-a rb-b rb-t
sudo reboot
sudo bash docs/contributors/design/research/macos-isolation-spike/reboot_post.sh
```

The pre half needs **two running `apple` sandboxes**, and optionally a **tart** one as a third
argument. It aborts unless every guest has an address *and* enforcement is demonstrably live before
going down. The helpers are backend-aware (apple answers through `container`, tart through `tart`)
and derive the backend from `yoloai ls`, so a mis-ordered argument fails loudly rather than measuring
one guest twice. Seatbelt has no guest network of its own and is not applicable. The workdir must be
somewhere durable, **not** under `/tmp`: a workdir that vanishes across the restart adds a second
variable to an experiment that already has nine.

The post half needs no manual step — it reads the kernel and file state first (those questions are
unaffected by what is running), then restarts the guests itself, then measures the live half. Cleanup
afterwards: `yoloai destroy rb-a rb-b rb-t --abandon-unapplied && rm -rf ~/yoloai-reboot-test`.

Between the two halves the machine carries a narrow `NOPASSWD` grant, a root-owned pinned ruleset and
a live pf anchor. The post half removes all three. If it is never going to run:

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
- **The pool is backend-agnostic.** It enforces on tart as well as apple, and an apple guest and a
  tart guest hold different allowlists in different slots simultaneously, on separate bridges, with
  teardown by table delete restoring either (`pf-tart-pool.txt` T1/T2/T3).
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
- **Settled by round 1 of the reboot test** (`reboot-post.txt`): nothing in the anchor survives a
  reboot — rules 16 → 0, every table emptied, so boot restore is mandatory · pf comes back **enabled
  on its own** with the same reference count, so the `-E` question is about surviving someone else's
  `-X`, not about starting from a disabled host · the main ruleset returns byte-identical · the
  pinned file and the grant survive · and the pool **restores unattended** from the pinned file with
  no tty after a real restart.

**Do not re-read `reboot-post.txt` as if every line in it holds.** Three of its verdicts had a
control that never loaded and one PASS was read off a stale DHCP lease; `results/README.md` says
exactly which and why, and both scripts were fixed. That is A30.

## Still open — decisions, not research

Pool size and exhaustion behaviour · the slot-allocation lock (cross-sandbox; per-sandbox `flock`s
already exist via `store.AcquireLock`) · host/guest DNS resolution parity · whether yoloAI holds its
own `pfctl -E` reference (**deliberately untested** — a wrong `-X` breaks vmnet NAT for every VM).

Known gaps: renewal *after* a subnet re-pick is untested; seatbelt is parked ([`seatbelt-host-pf-enforcement.md`](../../plans/seatbelt-host-pf-enforcement.md)).

**Blocking dependency:** rules must precede the agent's first packet, but apple and tart have no
host-side step between guest boot and agent exec — `entrypoint.py` execs the agent itself, and only
docker sets `AgentFreeLaunch`. `host-controlled-agent-launch.md` is a declared dependency and is
`PLANNED` with no code.

## How this work went wrong, repeatedly

Recorded properly as **A28**, **A29** and **A30** in
[`agent-failures.md`](../../../agent-failures.md). The short version, because it will happen again:

- **Ten instrumentation bugs** produced verdicts that could not have come out otherwise — a shell
  idiom for "default on failure" colliding with a command that already emits a value on failure.
  Before trusting a verdict, **construct the input that should produce the opposite one**.
- **Five flat claims about this codebase that were false or unsourced**, two of which were used to
  reject a design alternative. **A claim about what the code does gets a `file:line`, or it gets
  hedged.**

- **A30 is the same shape aimed at the harness itself**: the control the whole design rested on was
  the one input never tested, and three verdicts were rendered against a snapshot that had not
  loaded. **The control is an input too — corrupt it deliberately and confirm the harness refuses
  rather than reports.**

Three independent audit agents caught what self-review did not, and the discriminating behaviour was
that they *reproduced* idioms and *opened* files rather than reading and reasoning. If you are about
to write "this is safe because X", check whether you have run X.

> **ABOUTME:** Session handoff for the macOS host-`pf` workstream, written across a deliberate
> reboot that ended the authoring session's context. Read this first if you are picking the work
> up cold; it records the state, the decisions and — most importantly — what NOT to redo.

# Handoff: macOS host-`pf` enforcement, reboot test

**All three rounds ran 2026-08-04 and are complete. Nothing is installed on the machine and no
further round is needed.** Round 1: pass=8 fail=0 unknown=3, in git at `7c913d32`. Round 2:
pass=12 fail=0 unknown=1 — **its output no longer exists**, having never been committed before
round 3 overwrote it in place. Round 3: **pass=15 fail=1 unknown=0**, taken 11:30, and it is the
`results/reboot-post.txt` on disk. The machine was cleaned up after each.

Round 3 closed the last open question — *does a guest come back on the same address?* — with a
straight no on both backends, and turned up one FAIL that is a genuine property of the host rather
than a harness fault (tart's lease pool is exhausted and a reboot does not clear it). Every verdict
that matters to the plan is now measured. **The one thing to carry forward is a discipline, not an
experiment: commit a round's three files before starting the next one.** The harness rewrites them
in place, and that destroyed data twice in one morning — round 2's entire output, and round 3's
pre-half log.

## What round 2 got wrong, because it was the whole reason for round 3

Round 2 reported both apple guests returning on the addresses they went down on and recorded **"PASS
addresses preserved across reboot"**. A no-reboot control run afterwards (`results/restart-control.txt`)
moved *every* guest on *both* backends with a plain stop/start — no reboot, no sleep — each with a
freshly generated MAC. So nothing preserves an address, and what round 2 saw was an allocator counting
from the same place while the harness happened to restart the guests in creation order.

**The two explanations are not equally bad.** If allocation follows start order, then after a reboot a
saved slot→address mapping does not go stale, it names the **wrong sandbox** — restoring it hands one
guest the allowlist authorized for another, which is fail-open. So round 3 starts **B first** (P10).
That single reversal was the experiment; everything else in the run was re-measurement.

**The answer: neither.** Both addresses moved and B did not land on A's old one (A `.5`→`.4`, B
`.6`→`.3`, tart `65.4`→`65.2`, all with regenerated MACs). Allocation is not pinned to identity and
not a clean function of start order — the pool just advances from wherever it stood. A stored
mapping is therefore stale in general, and *wrong* specifically when the pool has wrapped, which on
tart it permanently has. The plan already assumed this worse case; it now has the measurement.

Two mechanisms found alongside it, both now in `backend-idiosyncrasies.md`: `tart run` regenerates the
VM's MAC on every start (yoloAI passes no MAC flag), so leases are burned per start and
`/var/db/dhcpd_leases` — already **exhausted** on this host, 253/253 — now recycles addresses that
previous VMs held; and apple holds **zero** records in that file, running a separate allocator, so
neither backend's address behaviour generalises to the other. P11 re-measured both pools across the
reboot and confirmed the exhaustion is permanent: 253 before, 253 after. Nothing prunes that file.

## Whether to run another round

**No.** Every question this test was built to answer is measured, and the remaining weakness is
replication rather than coverage — everything here is n=1 on one host, and a fourth round on the
*same* host would not fix that. Bridge-index stability is the one result actively contradicted
elsewhere in this directory, so treat it as an observation and never as a property. If a round 4 ever
does happen, it should be on **different hardware**, and the pre half should be run against a fresh
`git status` so the previous round's files are safely committed first.

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

The pre half snapshots each guest's **MAC**, the **start order**, a census of both DHCP pools, and
the pre-reboot enforcement digits — the values P10 and P11 compare against.

**The pre half now refuses to run when you have pasted the wrong filename**, which is the mistake
that cost round 3 its log: an unconsumed snapshot written before the machine's current boot means
the reboot already happened and the half you want is `reboot_post.sh`. `reboot_post.sh` stamps
`CONSUMED=` into the snapshot when it finishes, so a new round is never blocked by a spent one. If
you genuinely want to restart a round from scratch, delete the snapshot.

That check runs **before** the root check, deliberately. It needs no privilege, so the wrong-half
mistake is caught before the password prompt — and, more usefully, its refusal can be exercised
without sudo. The first attempt to verify it could not: reaching the guard required a real
privileged run, so "confirm it refuses" and "run the thing it must refuse" were the same command,
and the confirmation attempt overwrote the log a second time. **A guard you can only test by
triggering the damage is not tested.**

The post half needs no manual step — it reads the kernel and file state first (those questions are
unaffected by what is running), starts the apple `container` service, then restarts the guests
itself **in reversed order (B before A, deliberately — P10)**, then measures the live half. Cleanup
afterwards: `yoloai destroy rb-a rb-b rb-t --abandon-unapplied && rm -rf ~/yoloai-reboot-test`.

`restart_control.sh` is not part of the reboot pair and needs no restart: it stops and starts each
guest in place to establish what an ordinary restart does, so the reboot halves cannot attribute to
the reboot what happens anyway. Run it before the pre half if you want a fresh control.

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

- **Settled by rounds 2 and 3, and reproduced by round 3** (which is the readable one): the apple
  sandboxes **do** survive and restart rc=0 once `container system start` has run — round 1's
  `removed` reading was DF180, not a destroyed sandbox; they come back `stopped`, all of them · the
  vmnet bridges came back on the **same subnets and the same indices** (n=1 each time;
  `vmnet-switch.txt` and `pf-main-run.txt` disagree 26 minutes apart on this host, so do not promote
  it to a property) · **enforcement filters again end to end after a real reboot, in two slots with
  different allowlists** (`allow=301 deny=000` each), which is the half round 1 could not reach.

- **Settled by round 3** (`reboot-post.txt` on disk): **addresses move on both backends across a
  reboot**, with regenerated MACs, and reversing the start order neither preserves nor swaps them —
  membership must be rebuilt from live state, never restored from a mapping (P8/P10) · **tart's
  lease pool is exhausted and stays exhausted across a reboot**, 253/253 both sides, so every new VM
  recycles an address a previous VM held — the one FAIL in the run, and a real property of the host
  (P11) · apple holds **zero** `dhcpd_leases` records, so the two backends share neither pool nor
  exhaustion.

**Do not re-read `reboot-post.txt` as if every line in it holds** — and note it is rewritten in place
by each round, so check which one you have. Round 1: three verdicts had a control that never loaded
and one PASS was read off a stale DHCP lease. Round 2: the controls loaded, but its address PASS was
withdrawn (above) and its tart UNKNOWN was a harness artifact — the address lookup gated on `yoloai
ls` saying `active|running`, and yoloAI calls a running tart VM whose agent is not attached `idle`,
so the check excluded the guest it was measuring on both sides of its own comparison. It now asks the
backend. Round 3: the verdicts hold, but its **pre-half log was destroyed** by a re-run of the wrong
half and the file on disk is not round 3's at all — it is a partial, aborted round 4 (349 bytes,
twelve lines), because verifying the guard meant running the pre half again. What makes round 3
sound anyway is that the
snapshot is written *after* a gate that aborts unless every sandbox is enforcing, so the snapshot's
existence is the evidence the log would have carried. `results/README.md` indexes every round's
caveats. That is A30, three times.

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

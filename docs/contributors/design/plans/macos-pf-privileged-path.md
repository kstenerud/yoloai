> **ABOUTME:** Build plan for enforcing `--network-isolated` from host `pf` on macOS, where the
> allowlist today is either weak (apple grants the guest NET_ADMIN) or absent (tart, seatbelt).
> Covers how yoloAI acquires the privilege `pf` needs without installing a root daemon.

# macOS: enforce the network allowlist from host `pf`

- **Status:** PLANNED — mechanism, authorization and enforcement all measured on hardware
  2026-08-02/03. Nothing built.
- **Depends on:** tamper-resistant-network-isolation.md
- **Rides:** **any** — the user-visible surface only gains capability. `--network-isolated` becomes
  *accepted* on tart and seatbelt where it is refused today (newly-accepted input is not a break),
  and on apple it becomes stronger without changing its spelling. The `BackendCaps` change in
  Phase 2 is internal. If Phase 2 instead ends up *withdrawing* isolation anywhere, that half is
  **breaking** and needs a `docs/BREAKING-CHANGES.md` entry.

## Why this exists

On macOS, `--network-isolated` is either weak or absent. `apple` installs the allowlist inside the
sandbox and grants it `NET_ADMIN`, so the agent can flush its own rules (DF179, measured). `tart`
and `seatbelt` refuse `--network-isolated` outright, so they have none at all.

The research settled that this is **not** a platform limitation. Host `pf` enforces per-sandbox on
macOS, keyed on something the guest cannot change, and it holds against an agent that actively
attacks it. Measurements and their controls are in
[macos-isolation-spike](../research/macos-isolation-spike/README.md); the surrounding design is in
[tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) § The macOS half.
What blocks it is one thing: **`pf` needs root, and yoloAI on macOS has no privileged path.**

## The decision

**A generated `NOPASSWD` sudoers line.** Installing that line is the opt-in — no flag, no isolation
mode, no password prompt at runtime.

**Why not a runtime `sudo` prompt.** Teardown has no tty: `yoloai stop`, a signal handler, an MCP
call, a crash sweep. A teardown that cannot run unattended strands rules. So `NOPASSWD` is forced,
which makes the authorized command set the security boundary. Confirmed: `sudo -n` succeeds with no
controlling tty, including with a **ruleset on stdin**.

**Why not `sudo yoloai …` with a privilege drop.** Mechanically available: `syscall.Setuid` returns
EPERM not ENOTSUP on darwin, `fileutil.HostUID`/`ChownIfSudo` already exist, and `HOME` survives
sudo on macOS (measured), so the layout root resolves correctly. Two reasons, in order of weight:

1. **It cannot run unattended, which is the same wall the prompt hits.** Under this model *every*
   lifecycle command that touches pf must be invoked as `sudo yoloai …`, including `stop`. Teardown
   runs from scripts, signal handlers, MCP calls and crash-recovery sweeps, none of which have a tty
   or a warm credential cache. The sudoers-line model needs privilege for one `pfctl` call and
   leaves yoloAI itself unprivileged.
2. **The blast radius is the whole process tree.** The agent-facing process would run as root, so a
   sandbox escape escapes to root rather than to the user. Elevating one `pfctl` invocation does not.

> **Correction.** An earlier version of this plan rejected the wrapper on credentials — sudo strips
> `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` (measured), and `fileutil.SudoParentEnv`
> recovers them by reading `/proc/<ppid>/environ`, which does not exist on macOS. That fact is
> right; the conclusion was not. `sudo -E`, or a `Defaults env_keep +=` line, surmounts it — and
> **`sudo -E yoloai` is already an established invocation in this project** (`podman.go:340`, and
> throughout the archived VM-isolation work). Citing it as fatal was wrong. It is a papercut on a
> model rejected for the two reasons above.

None of this bears on the chosen design, where yoloAI never runs under sudo at all — only `pfctl`
is elevated, so the environment its own process sees is untouched.

**Why not authorize a yoloAI subcommand.** `/opt/homebrew/bin` is user-writable — Homebrew's Apple
Silicon default — so a `NOPASSWD` grant on the yoloAI binary is passwordless root for anything
running as the user. `/sbin/pfctl` is SIP-`restricted` and unwritable even by root.

**Why not load rules into a top-level anchor.** sudoers cannot constrain stdin, and a `rdr` rule
loaded into `com.apple/yoloai_*` **is evaluated** — measured, redirecting a sandbox's traffic to a
dead port. That grant is a traffic-interception primitive.

## The mechanism

**Per-sandbox sub-anchors under a filter-only parent.**

Setup, once, privileged, interactive — the parent anchor's entire contents:

```
anchor "*"
```

No `nat-anchor`, no `rdr-anchor`. That single line is the whole security boundary: translation is
evaluated only where a translation anchor is declared **at every level**, so rules inside a
sub-anchor can filter but cannot translate.

Per sandbox at start: `pfctl -a com.apple/yoloai/<instance> -f -`, rules on stdin, translation
before filtering (pf enforces the order options → normalization → queueing → translation →
filtering; a `rdr` after a filter rule is a syntax error). At stop: `-F rules` on that sub-anchor.

```
pass  in quick from <sandbox-ip> to <allowed-ip>
block drop in quick from <sandbox-ip> to any
```

`in` on the bridge, evaluated **before** NAT so the packet still carries the guest's address, and
`quick` so the `pass` wins by appearing first. Both halves are load-bearing: a `block drop out`
form sees the host's post-NAT source, matches nothing, and leaves the sandbox wholly unfiltered
while loading cleanly. That form was proposed in an earlier draft of this plan and is why the
acceptance test asserts a positive control rather than only a denial.

Measured together, one parent, parent read back as filter-only: filter rules **enforce** (so the
sub-anchor is demonstrably on the packet path), a `rdr` in the same sub-anchor is **inert**, a
second sandbox on the same bridge is **unaffected**, and `load anchor ".."` does **not** write the
parent. `set skip` is **not honored**, tested against a block rule proven working in the same run.

There is **no cap on concurrent isolated sandboxes**, which is the main reason this shape was
chosen over the fixed slot pool recorded at the end of this document.

### The grant

Three lines. Writes are sub-path-only; reads may name the parent, because `-s` cannot modify
anything and reaping must enumerate orphans.

```
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai/[A-Za-z0-9][A-Za-z0-9._-]* -f -$
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai/[A-Za-z0-9][A-Za-z0-9._-]* -(F rules|s rules|s nat)$
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -s (Anchors|rules)$
```

Validated 24/24 against a live policy
([pf-shapea.txt](../research/macos-isolation-spike/results/pf-shapea.txt)) — the tested anchor root
was `com.apple/yoloai_s`, otherwise identical. Permitted: install, teardown, self-verify, and
parent enumeration. **Refused:** writing the parent, flushing the parent, `..` as a leaf,
`../evil`, a leading-dot leaf, rules from a file, another anchor, main-ruleset load, `pfctl -d`,
`-F all` (globally and within a sub-anchor), and table ops. Cold-cache control passed.

Four things that must not be "tidied" later:

1. **The leaf must start with an alphanumeric.** That is what makes `..` unmatchable. A charset of
   `[A-Za-z0-9._-]+` alone would permit `-a com.apple/yoloai/.. -f -`, which writes the parent and
   destroys the containment.
2. **Globs are unusable.** sudoers matches arguments as one concatenated string, so
   `-a com.apple/yoloai*` would also permit `-a com.apple/yoloai -f /etc/pf.conf`. Regex only.
3. **Do not add escapes.** `man sudoers`: *"There is no need to escape sudoers special characters in
   a regular expression other than the pound sign."* The unescaped form is correct and tested.
4. **`visudo -c` cannot validate any of this.** It accepts the unsafe glob and the safe regex
   identically. The policy is **generated by yoloAI and verified by a permit/refuse matrix**, never
   documented for a user to hand-copy.

The leaf is the **principal-scoped instance name** (`store.InstanceName`), not the bare sandbox
name: sandbox names are unique per principal, anchors are not, so two principals with the same
sandbox name would otherwise collide on one anchor. The charset follows the real grammar in
`internal/config/names.go` (`^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$`) — an earlier `[a-z0-9-]+` would
have rejected `my_box`, `Feature2` and `a.b`, surfacing as a permission error rather than a name
error.

### What enforcement keys on, per backend

| Backend | Key | Note |
| --- | --- | --- |
| apple | source address on the vmnet bridge | a second VM on the same bridge was unaffected — the discriminator that makes it per-sandbox |
| tart | source address on the vmnet bridge | same shape; enforcement itself measured on apple only |
| seatbelt | **gid owning the socket** | `pf`'s `user`/`group` selectors match **TCP and UDP only**, so non-TCP/UDP egress is unfiltered by construction — a materially weaker guarantee that must be stated in user-facing text |

seatbelt also needs a second privileged step: `setegid()` to a supplementary group returns `EPERM`
**even for a member**, so the gid must be set by a privileged launcher. Its argument surface (a gid
and a program path) is much harder to constrain than an anchor name. **Phase 3, and possibly not
worth doing.**

## Hazards

Each produced a false result during the research before it was understood.

1. **Never touch the main ruleset.** `pfctl -f` *replaces* it, and that is where vmnet's NAT lives.
   Reloading `/etc/pf.conf` does not restore it; only restarting the vmnet service does.
2. **Rule order inside a ruleset is enforced**: options → normalization → queueing → translation →
   filtering. A `rdr` after a filter rule is a syntax error, and the failed load looks exactly like
   a rule that loaded and did nothing.
3. **`quick` follows the direction keyword.** `block drop quick in on …` does not parse;
   `block drop in quick on …` does.
4. **Key on the address, never the interface name.** vmnet re-picks subnets and bridge indices move
   between backends (DF172).
5. **Sub-anchors evaluate in alphabetical order of anchor name**, and a `quick` match *"aborts the
   evaluation of the rules in other anchors and the main ruleset"* (`man pf.conf`). For yoloAI's own
   rules this is harmless — every rule is keyed on its own sandbox's source address, so one
   sandbox's `pass` cannot match another's packets. It matters for the security ceiling below.

## Ordering, and the enforcement window

The sandbox address does not exist at launch. Measured: apple's appears **2s** after `yoloai new`
begins against **3s** for the command to return; tart's at **8s** against **39s**. So

```
start guest → wait for address → load sub-anchor → VERIFY → launch agent
```

is achievable synchronously in-process. Only the last arrow is a security boundary; boot traffic
before the rules exist is unfiltered, acceptable only because no agent code has run yet. **If agent
launch is ever reordered ahead of the rule load, the guarantee is void and nothing about the sandbox
looks different** — see the acceptance test, which must assert this directly.

## VERIFY is mandatory, and must check the parent

**A missing parent fails open silently** (measured): `pfctl` auto-creates an anchor path, so if
`com.apple/yoloai` has no `anchor "*"`, the per-sandbox load **succeeds**, the rules never evaluate,
and nothing distinguishes it from working enforcement.

This is not an edge case. **pf anchor contents are in-kernel state and do not survive a reboot.**
After every reboot the parent is empty until setup is re-run, and re-running it needs `pfctl -f`,
which the grant refuses by design. So the start path must verify:

1. the parent contains `anchor "*"` — `pfctl -a com.apple/yoloai -s rules` (granted, read-only), and
2. this sandbox's own rules are present — `-a com.apple/yoloai/<instance> -s rules` (granted).

Failing either is an error, not a warning. What to do about reboot itself is **undecided**: either
document "re-run setup after reboot" and fail closed until then, or install a `/etc/pf.anchors`
fragment at setup — which is persistent but edits the main ruleset and collides with hazard 1. A
LaunchDaemon contradicts this plan's premise. **This needs a decision before Phase 1.**

## Reaping and concurrency

Sub-anchors are reconciled against live sandboxes: enumerate with
`pfctl -a com.apple/yoloai -s Anchors` (granted, read-only), flush any whose sandbox is gone.
Teardown itself is `-F rules` on one sub-anchor.

Reaping is **correctness, not housekeeping**, because addresses are recycled: a sandbox's address
changes on every stop/start (apple `.22`→`.23`, tart `.2`→`.4`, and tart's MAC changes too, so the
pool is consumed per *start*), and a stale `block` rule naming a recycled address **silently denies
its next occupant** — measured directly.

**Concurrency is unsolved and must not be hand-waved.** `concurrency-guard-sandbox-operations.md`
records that no concurrency controls exist anywhere in the project. Reconciliation is therefore also
a de-synchroniser: process A starts sandbox X and loads its sub-anchor; process B runs `ls` and
reconciles before X is visible in on-disk state, and flushes it. X is then running, healthy, and
unfiltered — exactly the silent fail-open this design exists to prevent. Phase 2 must either take a
lock or make reconciliation delete-only past a grace window, with the reasoning recorded.

## The security ceiling

The grant's holder is the host user, who already has unrestricted network access — so for the
**single-user Mac this project targets**, the grant is not an escalation. Two qualifications the
earlier drafts of this plan got wrong:

- **This shape's ceiling is "load arbitrary filter rules into a sub-anchor", not "add one address to
  a table".** pf filter rules are host-global, and per hazard 5 a `pass … quick` in an
  alphabetically-early sub-anchor aborts evaluation of every other anchor *and the main ruleset*. On
  a **multi-user** Mac that is a host-wide filter-bypass or denial primitive an unprivileged user
  does not otherwise have. Document it; do not claim it away.
- **On seatbelt the agent runs as the same uid on the host.** The argument that the contained agent
  cannot reach the grant rests entirely on the seatbelt profile being `(deny default)`. If that
  profile ever permits exec of `sudo`, the contained agent *is* the grant holder. This is a further
  reason Phase 3 is separate.

## Shape of the work

**Phase 1 — capability, setup, verification.** Probe with `sudo -n /sbin/pfctl -a com.apple/yoloai
-s Anchors`; report through `yoloai doctor` like any other host prerequisite, with a command that
generates and installs the policy and loads the parent. `pfctl -n -f -` validates a generated
ruleset unprivileged (exit 0 valid / 1 invalid), so verify before spending a privileged call — note
it always prints the "Use of -f option" warning to stderr, which must be filtered rather than read
as failure. Decide the reboot story here.

**Phase 2 — apple and tart.** This is more than removing a line:

- `runtime.BackendCaps.NetworkIsolation` is documented as *"supports `--network=isolated` (iptables
  domain filtering)"*. Flipping it to `true` on tart/seatbelt routes them into the ip-filter path,
  which neither can execute. The capability must be able to express "isolated, enforced by host pf"
  — a new field or a mechanism enum.
- `internal/netpolicy/strategy.go` has a `Strategy` seam (`StrategyEgressProxy` is reserved), but no
  strategy-*selection* function exists; the sole call site hardcodes `StrategyIPFilter`. A
  `StrategyHostPF` needs one, plus a defined behaviour when the grant is absent.
- The `NET_ADMIN` grant at `launch.go:1040` is `NetworkMode == "isolated" && caps.NetworkIsolation
  && !sidecarFirewall`, and `sidecarFirewall` requires both `NetnsSidecarRunner` and
  `AgentFreeLaunch` — docker-only. A third condition is needed.
- **Dropping `NET_ADMIN` alone breaks boot.** `entrypoint.py` still runs `isolate_network` and
  raises `NetworkIsolationError` if a rule fails to install, so the sandbox would not start. The
  seam already exists — `YOLOAI_FIREWALL_EXTERNAL=1` (`entrypoint.py:187`, set today at
  `launch.go:1016`) — and must be set on this path.

With the capability absent, tart keeps today's refusal and apple keeps today's weak path plus
DF179's disclosure, so nothing that works today stops working.

**Phase 3 — seatbelt.** The gid launcher, or a decision not to. See the backend table.

## Acceptance test

Every assertion pairs a **block** with a **positive control in the same run**. A sandbox stranded by
the vmnet subnet re-pick refuses every destination for free, which silently invalidated the first pf
harness (DF172). A case asserting only the denial certifies nothing.

Per backend, in one run: an allowlisted destination **succeeds**, a non-allowlisted destination
**fails**, a second sandbox on the same bridge is **unaffected**, and after stop the sub-anchor is
**empty**. Plus two the earlier draft of this test omitted:

- **The ordering assertion.** The agent's first egress attempt must be observably after the rules
  are installed. Nothing else in the list goes red if launch is reordered ahead of the load.
- **The parent-missing case.** With an empty parent, starting an isolated sandbox must **fail**, not
  succeed unfiltered.

Per AGENTS.md rule 10, name the capability intersection before writing these: with
`NetworkIsolation: false` on tart and seatbelt today, a `runtime/runtimetest` case gated on that cap
has an **empty** backend set and would certify nothing. The tamper arm — the sandbox tries to remove
the rule and cannot — belongs in `runtime/runtimetest`, not a spike script.

## IPv6 — owned elsewhere

`--network-isolated` is IPv4-only on every backend (**DF104**), because `firewall.resolve_domains`
requests `AF_INET` only. Two plans already own this: `ipv6-network-isolation.md` (filter what is
present) and `guest-network-families.md` (decide what guests get, **Rides: breaking**). **This plan
does not close DF104** and must not grow a second mechanism for it.

What this plan contributes is that the mechanism is ready when those land: pf accepts `inet6` rules,
and one table or ruleset can carry both families — confirmed at runtime. Any v6 rule must use the
same `in quick` form; a `block drop out` variant fails open exactly as the v4 one does.

Measured, and the limit of it: an apple guest holds three `inet6` addresses including a **ULA**
(`fd96::/8`) and has no IPv6 egress; tart guests have none. This host has no IPv6 upstream, so
whether guests would egress over v6 on a host that does is **unmeasured** — a ULA cannot source
upstream traffic, so it would require an RA carrying a global prefix, which was not observed.

## Unmeasured, and known limits

- **Enforcement was measured on apple only.** tart shares the addressing shape but its pf
  enforcement has not been run.
- **Fail-open on address change while running.** Renewal alone does not move tart's address —
  five renewals counted across a 28-minute watch, address unchanged. But **renewal following a
  vmnet subnet re-pick is untested**, and that is the sharpest remaining risk. apple's long-run
  behaviour is also unmeasured: it has no `/var/db/dhcpd_leases` record, so it does not renew
  through bootpd at all.
- **`SandboxNetHealth` is not a periodic probe.** `probeNetHealth` runs only from the list and
  inspect read paths (`status.go:344`, `:486`) — when a user types `ls`, never on a schedule. Any
  proposal to re-verify "on the probe tick" must first create the tick.
- **Host/guest resolution parity.** The `pass` rules hold resolved addresses, and resolution moves
  from the guest to the host. Where the two resolve a domain differently, the guest connects to an
  address the host never allowed and is blocked while allowlisted. Applies to apple only — tart and
  seatbelt never had in-guest resolution. `resolve_domains` is also one-shot, so CDN rotation
  already breaks long-lived sandboxes today; that is inherited, not caused.
- **Reboot behaviour is undecided** (see VERIFY).

## Rejected alternative: a fixed slot pool

Static rules loaded once, referencing pf tables, with per-sandbox work reduced to
`pfctl -T add|delete <address>` — a much narrower grant (one IP per call). Measured working: 32
rules load, order preserved, membership add/delete works, teardown by delete alone restores egress.

Rejected because it caps concurrent isolated sandboxes at the pool size, and because its grant is
*too* narrow to run the design: `-T show` is absent from the validated table regex, so the capability
probe and reconciliation are both refused by it. Recorded here so the tradeoff is not rediscovered.

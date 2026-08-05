> **ABOUTME:** Retained harness for the macOS isolation research — host `pf` enforcement per
> sandbox, and host→guest signalling coherence on tart and apple. Every figure quoted in the
> two macOS plans is re-derivable by running these scripts.

# macOS isolation spike — `pf` enforcement and guest coherence

## Why this exists

The first pass at this research produced a set of numbers and a "no cannot on macOS" conclusion
with **no harness retained** — prose over evidence that no longer existed. Two of its claims were
later falsified on re-examination, and a third (the apple timings) turned out to measure the
measuring loop rather than the filesystem. This directory is the fix: the experiments are the
artifact, the prose is downstream of them.

It is a spike, not a regression guard. The guard these plans actually need is a
`runtimetest` conformance case, which does not exist yet.

## How to run

```sh
# 1. no root, no sandboxes, ~30s — SBPL semantics + the privilege audit
bash unprivileged.sh

# 2. no root, needs one sandbox per backend — host->guest signalling coherence
yoloai new --backend apple mac-coh-apple <some-repo>
yoloai new --backend tart  mac-coh-tart  <some-repo>
python3 coherence_host.py --backend apple --sandbox mac-coh-apple --reps 5 --poll 0.001
python3 coherence_host.py --backend tart  --sandbox mac-coh-tart  --reps 5 --poll 0.001

# 3. no root — is the vmnet alternation real and symmetric? (DF172)
bash vmnet_switch.sh mac-coh-apple mac-coh-tart

# 4. no root — does DF173's Docker Desktop staleness still reproduce?
bash docker_desktop_stale.sh

# 5. no root, creates its own tart sandbox — which host write pattern, if any,
#    reaches a guest at a path it has already read? (DF175)
bash df175_writepath.sh [sandbox-name]

# 5b. the guest-side repair the fix is built on, runnable by hand against any
#     tart guest: prints REFRESHED / ALREADY-FRESH / MISMATCH per file
tart exec yoloai-cli-<box> /usr/bin/python3 msync_refresh.py <sha256> <guest-path>

# 5. ROOT, once — pf enforcement, the dedicated-gid design, the ICMP hole, reaping
#    apple-B is the per-VM SCOPING discriminator; without it the run shows only that a rule
#    blocks a VM, not that it is scoped to one. isolated-apple must be created with
#    --network-isolated so its guest actually holds NET_ADMIN — a guest that cannot even run
#    iptables has not attempted tampering, and its failure proves nothing.
yoloai new --backend apple mac-coh-apple-b <repo>
yoloai new --backend apple --network-isolated mac-iso-apple <repo>
sudo bash privileged.sh mac-coh-apple mac-coh-tart mac-coh-apple-b mac-iso-apple
```

`privileged.sh` **destroys the first sandbox** (`mac-coh-apple`) partway through, deliberately, to
show that an address-keyed rule outlives the sandbox it was written for. Run it last.

`privileged.sh` writes pf rules **only** into the nested anchor `com.apple/yoloai_spike` and
restores everything on exit. It never runs `pfctl -f`, which replaces the main ruleset — that is
where vmnet's NAT lives for every VM on the host, `/etc/pf.conf` warns about it in its own header,
and reloading `/etc/pf.conf` does **not** put the NAT back. Only restarting the vmnet service does.
An earlier harness generation did exactly this and silently destroyed every VM's egress, then
measured "blocked".

## Method rules these scripts enforce

Three defects produced false results before the harness was built this way, so each is now
mechanical rather than remembered:

1. **Every block assertion carries a positive control.** A permitted destination that must still
   succeed, in the same run. Without it, a sandbox with no network at all passes "egress is
   refused" on every destination ([DF172](../../findings-unresolved.md), and [A22](../../../agent-failures.md)).
2. **Rules are verified present in the anchor before anything is measured.** `load()` counts them
   and prints `RULE LOAD FAILED … MEANINGLESS` otherwise. A rule that never parsed reads exactly
   like traffic that cannot be filtered — which is how `block drop quick in on …` (wrong keyword
   order; `quick` must follow the direction) was once written up as "pf cannot match this".
3. **Every shape records exactly `--reps` rounds.** `coherence_host.py` asserts
   `len(rows) == args.reps`. An earlier version asserted that three exhaustive buckets summed to
   `len(rows)` — true for every possible input including a run that silently dropped rounds, i.e.
   unfalsifiable. It was written to catch a dropped-round bug that it could not, by construction,
   have caught; the ACK-atomicity fix is what actually caught that.
4. **The predicate is an axis.** Every applicable predicate is polled for every action, because
   one-predicate-per-action measures `action × predicate` and reports it as a property of the
   action. That mistake produced three false "NEVER" rows and two wrong headline findings.

## Results

Measured 2026-08-02 on macOS 26.5.1 (25F80) arm64, tart 2.32.1, `container` 1.0.0.

**Raw output for every number below is in [`results/`](results/README.md)**, including the runs that
produced nothing usable. That directory's README also lists what these runs do *not* support —
read it before quoting a figure. In short: every pf result is **one run on one host**; the apple
coherence matrix is the only thing replicated (n=2, different sandboxes).

### Host→guest signalling coherence (`coherence_host.py`, n=3, 1 ms poll, 200 ms settle)

**Predicate is an axis, not a detail.** Every applicable predicate is polled for every host
action, because binding one predicate per action — and reporting the result as a fact about the
action — is exactly what made an earlier version of this table wrong. Times are **guest-side**,
from the start of the round, so they include the 200 ms settle; subtract it for
time-after-the-host-acted.

| host action | apple `read`/`stat`/`readdir` | tart `read`/`stat`/`readdir` |
| --- | --- | --- |
| create a new file | – / 208 / **209** | – / 993 / **214** |
| mkdir | – / 204 / 206 | – / 994 / **212** |
| symlink | – / 204 / 206 | – / 995 / **207** |
| overwrite in place | 206 / 205 / – | **NEVER** / 989 / – |
| overwrite via temp+rename | 205 / 1004 / – | **NEVER** / **NEVER** / – |
| delete | – / 1204 / **205** | – / **NEVER** / **212** |
| append | 1004 / 1004 / – | 992 / 992 / – |

**1. Poll with `readdir`; signal by creating a new name.** ~10–15 ms after the host acts, on both
backends. The identical event costs ~800 ms observed by `stat` on tart. Free, and worth ~70×.

**2. "NEVER" is about the predicate, not the filesystem.** tart's overwrite and delete both reach
the guest in about a second — via `st_size` and `readdir` respectively. What never converges is
`read()` on a rewritten file and `stat()` on a deleted one. An earlier version of this table
reported three rows as "the change does not propagate", which was false.

**2b. The `append` row reads as healthy and is not.** It converged because this harness prepares
an *empty* file, so the append only added bytes past the cached extent. The mechanism, settled
later (DF175, [`results/df175-write-patterns.txt`](results/df175-write-patterns.txt)): the guest
caches file **pages** and never invalidates them on a host write, refreshing only attributes. A file
that grows therefore looks correct while a file that shrinks — or is rewritten inside a cached
page — serves stale bytes clamped to the new size. Appending to a file that already had content
returns stale head plus fresh tail. Read this row as "the harness picked the one append that
works", not as "append propagates".

**3. The "~800 ms" figures were an artifact of this harness — swept, not argued.** `--settle` is now
a flag; sweeping it on tart's `create` (raw: [`results/settle-sweep-tart.txt`](results/settle-sweep-tart.txt)):

| `--settle` | `stat_ms` | `readdir_ms` | readdir − settle |
| --- | --- | --- | --- |
| 0.05 s | **995** | 64 | 14 ms |
| 0.20 s | **998** | 215 | 15 ms |
| 0.50 s | **997** | 510 | 10 ms |
| 0.90 s | 1517 | 912 | 12 ms |

`stat` is **pinned at ~997 ms regardless of when the host acts** across the first three rows — a
latency cannot do that. It is a revalidation tick anchored to the guest's *first lookup of the
path*, so the old host-side number was `tick − settle`, and picking a 0.9 s settle would have let
the draft report "the cache is ~100 ms" with equal confidence. `readdir`, by contrast, tracks settle
exactly: subtract it and you get a flat 10–15 ms, which is a real latency.

Honest anomaly: the 0.9 s row lands at ~1517 rather than ~1000, so the model is not simply "one
tick at 1 s" — the host's action at 900 ms appears to miss the first revalidation and catch a later
one. The load-bearing conclusion (pinned vs. tracking) does not depend on resolving that, and it is
not resolved.

**4. The serious result is not a latency.** On tart, `read()` after a host overwrite returns the
**old bytes at the new length** — correct `st_size`, successful read, content that never existed.
Filed as [DF175](../../findings-unresolved.md); catalogued in
[backend-idiosyncrasies.md](../../../backend-idiosyncrasies.md).

**5. apple has two clocks and both are honest.** Data in ~5 ms, metadata (size after rename,
dentry removal after delete, size growth after append) on the same ~1 s tick — every round, not
occasionally. No `NEVER` anywhere.

### SBPL semantics (`unprivileged.sh`, 16/16 passing)

- **No destination allowlist is expressible.** `(remote ip "1.1.1.1:443")` →
  `sandbox-exec: host must be * or localhost in network address`; CIDR the same. Port scoping does
  work: under `(remote ip "*:443")`, port 443 reached (301) and port 80 refused (000).
- **Sandbox confinement is no-way, not one-way.** A confined process cannot *loosen* its sandbox —
  and cannot *tighten* it either: a permissive outer profile with a tightening inner profile is
  refused with the identical `sandbox_apply: Operation not permitted`. The two nesting cases that
  succeed are both semantic no-ops. Already documented as
  [macOS `sandbox-exec` doesn't nest](../../../backend-idiosyncrasies.md); the spike's contribution
  is the discriminator. **Consequence:** yoloAI cannot re-tighten a live seatbelt sandbox, so live
  `allow`/`deny` has no SBPL route on that backend.

### The privilege audit — "needs root" is true, and now evidenced

Both plans assert that a `pf` design needs privilege yoloAI does not have on macOS. Checked:

| Claim | Evidence |
| --- | --- |
| `pf` needs root | `/dev/pf` is `crw------- root:wheel`; non-root `pfctl -a <anchor> -f` → rc=1, `/dev/pf: Permission denied` |
| …even for a nested anchor | same; only parse-only `pfctl -n -f` (rc=0) works unprivileged, because it never opens the device |
| a dedicated gid needs privilege | `setegid(80)`/`setgid(80)` → `EPERM` **even though the process is a member of gid 80** |
| yoloAI has no privileged path on macOS today | every `sudo` reference in the tree is a Linux install hint (`runtime/*/caps.go`, `containerd.go`) |

So the seatbelt gid mechanism needs the *same* privileged launcher as the `pf` rules — it is not
merely "new machinery", and the two costs do not add up independently.

### Does DF173's Docker Desktop staleness still reproduce? (no)

`docker_desktop_stale.sh` models the documented sequence (a container reads the mounted file, the
host patches it, a **new** container reads it at start). On Docker Desktop as installed here, both
rewrite shapes read **FRESH**:

| rewrite | guest reads at start |
| --- | --- |
| control: no patch at all | STALE *(**this control is vacuous** — with no rewrite the file still says BEFORE, so the probe reports STALE against any filesystem, working or not. It demonstrates nothing. A real control would need a known-stale mount.)* |
| in place (`os.WriteFile`, same inode) | FRESH |
| temp + rename (new inode) | FRESH |

This does not vindicate or refute the original diagnosis — the symptom was real and reproduced when
filed — but the failure is not reproducible here, so the inode explanation in
[DF173](../../findings-unresolved.md) cannot be confirmed by re-running it. Treat the entry's stated
*mechanism* as unverified rather than as a fact to build on. **Two caveats on this run itself:** the
Docker Desktop version is not recorded, and each probe creates its container *after* the rewrite, so
no mount spans the patch — which is the condition the original report describes. This probe may
simply not exercise the path.

### vmnet: the two VM backends coexist — an earlier finding retracted

`vmnet_switch.sh` drove the switch deliberately and refuted the claim it was written to confirm.
At **all four** checkpoints, including immediately after a `container system` restart with tart
running and after a tart VM restart with apple running:

| | bridges | apple egress | tart egress |
| --- | --- | --- | --- |
| T0 baseline | 100=192.168.139.3, 101=192.168.64.1, 102=192.168.65.1 | 301 | 301 |
| T1 apple network restarted | same three | 301 | 301 |
| T2 tart VM restarted | same three | 301 | 301 |
| T3 apple restarted again | same three | 301 | 301 |

Three bridges, one per vmnet consumer (Docker Desktop, apple, tart), indices stable **across this
run**, both backends working throughout. Two honest limits: the log grep is truncated by `tail -40`
and covered only ~3 s of a ~4-minute run, so it supports no claim about detach events either way;
and a later run the same evening (`priv2.txt`) shows `bridge101` carrying tart's `192.168.65.x`
subnet, so index stability holds for this run and **not** as a general property.

So [DF172](../../findings-unresolved.md)'s "the epoch change crosses backends / most-recent-start
wins" is **retracted**. Those observations came from a host whose container-framework vmnet was
already wedged — only two bridges existed and index 101 was being recycled with a new subnet each
time, which strands whatever is attached to the old epoch. That is the *already documented*
cross-epoch flapping, not cross-backend exclusivity. What survives: bridge **re-creation** can
change the subnet and silently strand attached VMs, and only tart has a detector for the result.

The general lesson is the one this directory keeps re-teaching: a repeated symptom on a
misconfigured host is not a mechanism, and n=4 with a causal verb attached is a hypothesis.

### Host `pf` enforcement (`privileged.sh`, run 2026-08-02)

`000` = no connection, `301` = reached. Every block reading below has a positive control beside it
and was taken only after the rules were verified present in the anchor.

| Case | Result |
| --- | --- |
| nested anchor is evaluated | host blocked `000`, other destination `301`, restored `301` |
| **apple: per-VM scoping** | VM A blocked `000`; **VM B on the same bridge `301`**; host `301` |
| apple: allowlist shape | allowed `301`, everything else `000` |
| tart: allowlist + tamper | allowed `301`, denied `000`; guest ran `pfctl -d` and stayed blocked, allowed destination still `301`. **Weaker than it looks** — the harness truncates `pfctl -d` output with `head -1`, and nothing established that the guest's pf was enabled or had a rule to defeat, so this shows the host rule held, not that a tamper was defeated. The apple P4 below is the one with an efficacy control. |
| **apple: privileged tamper** | see P4 below — guest with `NET_ADMIN` destroyed its own firewall and still could not pass the host rule |
| **rule reaping** | rule for a destroyed sandbox's IP **still sitting in the anchor**; a later sandbox issued that address inherits it |

**The dedicated-gid design works, and is not just the borrowed-groups result.** A throwaway group
at gid 700, which the user is deliberately *not* a member of:

| | result |
| --- | --- |
| baseline, no rules | gid 700 → `301`, gid staff → `301` |
| allowlist for gid 700 | allowed `301`, denied `000`, **gid staff unaffected `301`** |
| can the gid be shed? | `setgid(20)`, `setegid(20)`, `setgid(0)`, `setegid(0)` → all **EPERM** |

**The ICMP hole is real, and the mechanism is correctly scoped** — settled 2026-08-03 with the
protocol-agnostic rule (`block drop quick inet from any to any group <gid>`, no `proto`) plus an
over-block control:

| probe | result |
| --- | --- |
| tcp → denied dest, as the sandbox gid | `000` (block bites TCP) |
| tcp → allowed dest, as the sandbox gid | `301` (pass still works) |
| **tcp → denied dest, as an unrelated gid** | **`301` — correctly scoped, no over-block** |
| ICMP → denied dest, as the sandbox gid | **REACHED** — `group` never matches ICMP |

The over-block row is the one that mattered: had it read `000`, `group` would be ignored rather
than unmatched, the rule would hit every process on the machine, and the mechanism would be
unusable. Two earlier attempts at this were circular — their block rule carried `proto tcp`, so
"ICMP escapes" restated the qualifier — and one of those shipped a `PROTOCOL-AGNOSTIC` label over
the unchanged `proto tcp` rule, which was worse than the original error.

### P4 — a privileged guest tears down its own firewall and still cannot get past host `pf`

This is the load-bearing tamper result, and it took three attempts to make it mean anything.
Against an apple sandbox created with `--network-isolated`, whose guest genuinely holds
`NET_ADMIN` (verified: `sudo iptables -L OUTPUT` succeeds):

| step | reading |
| --- | --- |
| baseline: target the guest's own allowlist permits (`160.79.104.10`) | `403` — reachable |
| baseline: address blocked by the guest's *own* firewall (`1.0.0.1`) | `000` |
| host `pf` rule blocks `160.79.104.10` for this VM's address | `000` |
| guest runs `iptables -F` + `-P OUTPUT ACCEPT` + `-P INPUT ACCEPT` | — |
| **positive control:** `1.0.0.1` after the flush | **`301`** — the guest really did destroy its own firewall |
| **result:** `160.79.104.10` after the same flush | **`000`** — the host rule held |

The control is what makes this evidence rather than a coincidence: the guest is *shown* to have
defeated the policy it controls, in the same breath as failing to defeat the one it does not.

**Why it took three runs, since the failure mode is the point.** Attempt 1 used a guest without
`NET_ADMIN` — an unprivileged `iptables` failure is not a tamper attempt. Attempt 2 used a
non-allowlisted destination as the target, so the baseline was already `000` from the guest's own
firewall and "still blocked" measured nothing. Attempt 3 hit a stranded sandbox (`via en0` rather
than a bridge, after tart's restart took apple's). Each was caught by a precondition gate rather
than reported as a pass; the gates are the reason the fourth result can be trusted.

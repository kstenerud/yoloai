> **ABOUTME:** Raw, unedited output from the macOS isolation spike runs, kept so every figure
> quoted in the plans traces to a line someone can read rather than to prose about a run.

# Raw run output

The parent README's numbers come from these files. They are verbatim stdout, including the runs
that produced nothing usable — those are the more instructive half, and deleting them would leave
the impression that the harness worked first time.

| File | What it is |
| --- | --- |
| `coherence-apple.txt` | host→guest shape matrix, apple, n=5, 1 ms poll |
| `coherence-tart.txt` | same, tart |
| `pf-main-run.txt` | the pf run everything is cited from: per-VM scoping, allowlist, reaping, gid, ICMP, tart tamper |
| `pf-p4-tamper.txt` | the privileged in-guest tamper on apple, the one with an efficacy control |
| `vmnet-switch.txt` | the controlled tart/apple switch that **refuted** the cross-backend exclusivity claim |
| `pf-run1-invalidated.txt` | first pf run — the gid section returned empty strings because sudo refused the runas group; kept because the blanks are what a broken probe looks like |
| `pf-p4-skipped.txt` | P4 correctly refusing to run against a stranded sandbox |
| `df175-write-patterns.txt` | every host write pattern tried against a path the guest had read, plus the `msync` repair verified here and the prior art that reports the same defect |
| `reboot-snapshot.txt` | the pre-reboot control the post half compares against — written by `reboot_pre.sh`, machine-read, not prose |
| `reboot-pre.txt` | the pre half: enforcement demonstrated live for two apple guests and one tart guest immediately before the restart, so the after-comparison means something |
| `reboot-post.txt` | the post half, after a **real** restart: anchor survival, pf's own state, and unattended recovery from the pinned file |

## What these files do NOT support

Read this before quoting anything from them.

- **Every pf result is n=1 run on one host.** The apple coherence matrix is n=2 (two sandboxes);
  nothing else is replicated.
- **`vmnet-switch.txt`'s log section proves nothing about detach events.** It is piped through
  `tail -40` and covers ~3 seconds of a ~4-minute run. The script asked "who tears the bridge
  down" and did not get an answer.
- **Bridge indices are not stable in general.** `vmnet-switch.txt` shows `bridge101`=apple and
  `bridge102`=tart; `pf-main-run.txt`, 26 minutes later, shows `bridge101` carrying tart's
  `192.168.65.x`. Both are true; the second is why rules must not key on an interface name.
- **The tart tamper line in `pf-main-run.txt` is weaker than the apple one.** `pfctl -d` output is
  truncated by `head -1`, and nothing established the guest's pf was enabled or had a rule to
  defeat. `pf-p4-tamper.txt` is the run that carries an efficacy control.
- **The ICMP "sharper" test in `pf-main-run.txt` is circular** — its block rule was scoped
  `proto tcp`, so ICMP passing follows from the qualifier. The harness was fixed afterwards to load
  the protocol-agnostic form; that has not been re-run, and it is the case an implementer needs.
- **Three of `reboot-post.txt`'s comparisons had no working control.** `reboot_pre.sh` wrote the
  snapshot's values unquoted, so the four carrying spaces — `PRE_DATE`, `PF_STATUS`, `BRIDGES`,
  `SANDBOXES` — did not survive being sourced. Three `command not found` lines at the top of the file
  are the evidence, and `BRIDGES` truncated silently to its first token rather than erroring. So:
  **P6's UNKNOWN is an artifact**, it compared a corrupted before-value against an after-state that
  had no vmnet bridges at all because nothing was running; and P3's and P7's `before:` lines print
  empty. P3's verdict does not depend on its before-value (it reads pf's live status and reference
  count), P4's and P1/P2's controls are single-token and loaded correctly. Fixed in the scripts;
  the file is kept as run.
- **`reboot-post.txt`'s "tart address preserved across reboot" is not reproducible and should be
  read as unmeasured.** rb-t was `stopped`, `bridge102` did not exist, and four minutes later the
  exact command the harness used — `tart ip yoloai-cli-rb-t` — returned *"no IP address found, is
  your VM running?"* with rc=1. A `/var/db/dhcpd_leases` record for `192.168.65.2` did survive the
  reboot, which is the likeliest thing that PASS read. Nothing here establishes that a tart guest
  **holds** its address across a restart.
- **P8 and P9's end-to-end half were not exercised at all**, and the reason is worth keeping: the
  apple sandboxes came back `removed` rather than `stopped`, so no guest had an address to re-add to
  a slot. The pool-restore half of P9 is measured; "enforcement works again afterwards" is not.

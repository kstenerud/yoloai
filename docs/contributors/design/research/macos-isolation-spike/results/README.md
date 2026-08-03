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

> **ABOUTME:** Raw runs from the Linux half of the enforcement verification queue. One file per
> item, named for the item. Invalidated runs are kept and labelled, because what a bad control
> looked like is worth as much as the result that replaced it.

# Linux enforcement verification — raw results

Counterpart to `../../macos-isolation-spike/results/`. Item definitions and what each result
decides live in `design/plans/enforcement-verification-queue.md`; this directory holds only the
runs. Harness scripts are one level up.

**Host under test.** Ubuntu, kernel 6.8.0-136-generic, nftables v1.0.9, docker 29.6.1, podman
4.9.3, nerdctl 2.2.1, unified cgroup v2 (`cgroup2fs`), systemd-resolved in stub mode with the
LAN resolver at `192.168.111.1`. `ufw` is installed and its unit is active, but `ufw status`
reports `inactive` — the unit being up is not the same as the firewall enforcing, which matters
for L3. firewalld is not installed.

**Method rules applied, both from the queue header.** Every negative carries a positive control
in the same run, and every negative result names what was tried. Where a control was itself weak,
the file says so rather than quietly resting on it — see the prerouting control in `l1b`.

| Item | File | Outcome |
| --- | --- | --- |
| L1 | `l1-cgroup-key.txt` | Negative. Cgroup keying is unavailable for container egress. |
| L1b | `l1b-cgroup-prerouting.txt` | The one hook that accepts the rule never fires it. |
| L2 | `l2-split-horizon-dns.txt` | Reproduced, plus a demonstrated widening. |
| L3 | `l3-firewall-manager-triggers.txt` | ufw and a docker restart do not touch our table. |
| L3b | `l3b-firewalld-mechanism.txt` | Nor does firewalld on its nftables backend. |
| L3c | `l3c-shared-vs-own-table.txt` | Own table survives reload, complete-reload, restart. |
| L3d | `l3d-shared-table-iptables-backend.txt` | Sharing the manager's table *is* destroyed — the CVE shape. |
| L4 | `l4-address-recycling.txt` | docker recycles at once; containerd allocates forward. |
| L4b | `l4b-rootless-podman-path.txt` | Rootless podman egresses as the *host*, in the output hook. |
| L4c | `l4c-rootless-podman-shared-egress.txt` | One shared egress process for any number of sandboxes. |
| L5/6/7 | `l5-l6-l7-kernel-assumptions.txt` | `nft -f` atomic; priority irrelevant for drops. |
| L5b | `l5b-ipv6-hole.txt` | Invalid — docker isolates separate bridges; see `l5c`. |
| L5c | `l5c-lateral-and-family.txt` | Same-bridge sandbox↔sandbox traffic is unfilterable. |
| L6b | `l6b-set-replacement-atomicity.txt` | Set replacement is atomic and fails closed. |

## Runs that were discarded, and why

- **L1, first two attempts.** `nft -f` rejected a chain named `fwd` (reserved word), then failed
  on a cgroup path that did not yet exist because paths resolve at rule-load time, not match time.
  Neither run produced a usable counter. The second failure is informative on its own: because
  `nft -f` is one transaction, a single bad rule made *every* rule in the file report
  "Operation not supported", including a plain `ip saddr` that was fine. Read carelessly, that
  looks like the whole match family being unsupported. It is instead L6's answer arriving early —
  `nft -f` really is all-or-nothing.
- **L3b's first run, and its control caught it.** firewalld never started — the Fedora image has no
  `dbus-daemon` until you install it, and `docker exec` without `-i` swallowed the heredoc feeding
  `nft -f`, so the foreign table was never loaded either. The run duly reported the foreign table as
  "DESTROYED by a firewalld reload", which is the answer the prior art predicted and would have been
  entirely believable. The positive control — firewalld's own table must exist after a reload it
  supposedly performed — is the only reason that did not become the recorded result.
- **L3c Part 2 was inconclusive and was redone as L3d.** Setting `FirewallBackend=iptables` and
  restarting was not enough to make firewalld manage the shared table in a way the run could see:
  `iptables` in that image is `iptables-legacy`, so nothing appeared under `nft list tables` and the
  reload had nothing to flush. The foreign chain "survived", which proves nothing. L3d re-ran it
  after confirming firewalld had actually populated the shared table (30 of its own chains present),
  and that is the run the conclusion rests on.
- **L4's podman rows are empty and should be read as invalid, not as zero.** The shared
  `probe_backend` helper used docker's inspect format, which returns nothing for rootless podman —
  whose containers have no `NetworkSettings.Networks` entry at all. The row duly printed
  "address RECYCLED immediately", comparing one empty string to another. Podman was re-measured
  properly in `l4b`/`l4c`; the original row is left in place because a format string that silently
  yields "" and then compares equal is the kind of harness bug worth being able to recognise again.
- **L4c's first count was self-matching.** `pgrep -af slirp4netns | grep -c rootless-netns` counts
  its own pipeline, so it reported 5 egress processes at every sandbox count. The number was noise;
  that it did not *change* was the only real signal. Recounted by walking `/proc/*/comm` and cgroup,
  which gives 0, 1, 1, 1 for 0, 1, 2, 3 sandboxes.
- **L5 and L5b both had failed controls, in opposite ways, and L5c is the valid run.** L5 put both
  containers on one bridge, where the traffic never reaches the forward hook — so the v4 control
  read "REACHABLE" when the policy said drop, and the counter sat at 0. L5b moved them to two
  bridges to force routing, and hit docker's inter-network isolation, which blocked *both* families
  before any rule of ours ran — a control that reads "blocked" for the wrong reason, which is the
  more dangerous of the two failures because blocked is the answer you are hoping for. Neither run
  supports a conclusion. L5c asks each question where it can actually be answered: lateral traffic
  against a live internet control, and family matching on the host's own loopback.
- **L1b's probe control returned 0.** The within-run comparison still holds (see the file), but
  the separate cgroup control for the prerouting hook did not fire either, so it proves nothing.
  The conclusion rests on the L1 output-hook control and on the address counter in the same chain,
  not on this one.

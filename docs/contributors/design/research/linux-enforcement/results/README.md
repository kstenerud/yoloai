> **ABOUTME:** Raw runs from the Linux half of the enforcement verification queue. One file per
> item, named for the item. Invalidated runs are kept and labelled, because what a bad control
> looked like is worth as much as the result that replaced it.

# Linux enforcement verification — raw results

Counterpart to `../../macos-isolation-spike/results/`. This directory holds only the runs; harness
scripts are one level up.

**Where the conclusions live.** Synthesis applied these results on 2026-08-09 — cite those documents,
not this index: [enforcement-state-reaping.md](../../../plans/enforcement-state-reaping.md) (rules 0b and
5, the key question, the verification answer), [macos-pf-privileged-path.md](../../../plans/macos-pf-privileged-path.md),
D132 and D133 in `decisions/working-notes.md`, and DF104/DF188. The item definitions that generated
these runs are archaeology now, in
[archive/plans/enforcement-verification-queue.md](../../../../archive/plans/enforcement-verification-queue.md).

**Host under test.** Ubuntu, kernel 6.8.0-136-generic, nftables v1.0.9, docker 29.6.1, podman
4.9.3, nerdctl 2.2.1, unified cgroup v2 (`cgroup2fs`), systemd-resolved in stub mode with the
LAN resolver at `192.168.111.1`. `ufw` is installed and its unit is active, but `ufw status`
reports `inactive` — the unit being up is not the same as the firewall enforcing, which matters
for L3. firewalld is not installed.

**Method rules applied.** Every negative carries a positive control in the same run, and every
negative result names what was tried. Where a control was itself weak, the file says so rather than
quietly resting on it — see the prerouting control in `l1b`. The discarded runs below are why those
rules now live in [testing-principles.md §11](../../../../principles/testing-principles.md): nearly
all of them failed the same way, with a control that something *other* than the code under test
satisfied for free.

| Item | File | Outcome |
| --- | --- | --- |
| L1 | `l1-cgroup-key.txt` | Negative. Cgroup keying is unavailable for container egress. |
| L1b | `l1b-cgroup-prerouting.txt` | The one hook that accepts the rule never fires it. |
| L2 | `l2-split-horizon-dns.txt` | Split-horizon reproduced. **Its second conclusion is retracted — see `r6`.** |
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
| L5d | `l5d-layers-compared.txt` | The in-guest layer covers what the host layer cannot see. |
| L8 | `l8-br-netfilter.txt` | **Corrects L5c/L5d** — that gap is a default, not a limit. |
| L9 | `l9-sidecar-resolver-context.txt` | Sidecar shares resolv.conf *and* hosts; no divergence. |
| L9b | `l9b-allowlist-set-stability.txt` | Shipped allowlist domains are single-address and stable. |
| L9c | `l9c-zero-address-allowlist.txt` | A `0.0.0.0` answer is inert, not a widening. |
| L10 | `l10-conntrack-recycling.txt` | TCP residue does not carry a new sandbox through. |
| L10b | `l10b-established-residue.txt` | Invalid — see below. Superseded by `l10c`. |
| L10c | `l10c-udp-residue.txt` | **UDP residue does** — a new sandbox inherits the accept. |
| L11 | `l11-l12-cni.txt` | CNI host-local wraps and reuses freed addresses. |
| L12 | `l11-l12-cni.txt`, `l12b-stale-cni-entry.txt` | DF9 absent here; live stale accept found. |
| X1 | `x1-source-address-spoofing.txt` | The address key is defeasible; one bridge rule closes it. |
| X2 | `x2-revocation-vs-live-flow.txt` | Revocation does not stop an already-open connection. |
| R1 | `r1-rootless-netns-enforcement.txt` | Enforcement works inside podman's rootless netns. |
| R2 | `r2-rootless-reach-and-lifecycle.txt` | Partial — agent holds NET_ADMIN but not CAP_SYS_ADMIN. |
| R3 | `r3-rootless-spoof-cause-and-lifecycle.txt` | The rootless netns dies with the last container. |
| R4 | `r4-rootless-spoof-tcp.txt` | Spoofing works there too; rule 0b closes it. |
| R5 | `r5-rootless-iifname-diagnostic.txt` | Diagnostic: which interface forwarded traffic arrives on. |
| R6 | `r6-host-destined-traffic.txt` | **Forward-hook policy does not cover sandbox→host traffic at all.** |

**X1 and X2 are the macOS pass's extras asked on Linux**, after that pass landed. Both reproduce, and
X1's fix — a bridge-scoped default-deny naming no address — works here too.

One cosmetic failure inside an otherwise valid X1 run: `ip -4 -br addr show` is not supported by
busybox and printed a usage message. The `ip addr add` it was reporting on had already succeeded,
which the bypass itself demonstrates, so the result stands and only the display of it failed.

**R1–R5 answer the rootless-podman question** L4b/L4c left open: host-netns enforcement cannot reach
it, but podman's *rootless network namespace* can, and the whole address-keyed design transfers there
unchanged. Four of those five runs were invalid before R4 got it right — see below, because the way
they failed is the most instructive part.

**L2's second conclusion is retracted (2026-08-09).** L2 reported that host-side resolution wrote the
host's own LAN address into the guest's allowed set and that the guest then reached it *through* that
entry — described at the time as "a widening that happened, not one inferred". R6 re-ran it with no
allowlist and no accept rule at all, and the guest reaches the host's LAN address anyway. Traffic to
any address the host holds is delivered locally, so it goes prerouting → **input** and the forward
chain never sees it: in R6 the forward counter took 3 packets (the external control) while the input
counter took 8 (the two host-destined connections). The allowlist entry was inert and the attribution
was wrong. L2's *first* conclusion — the guest cannot reach the name it was allowlisted for — is
unaffected, and so is D133. What replaces the retracted half is a larger and unrelated finding: a
forward-only design cannot express any policy about sandbox-to-host traffic.

## Runs that were discarded, and why

- **R1, R2 and R3's spoofing results were all free, and it took three runs to see it.** Every one
  probed the spoofed source with `ping` while establishing its controls with `nc`. **ICMP does not
  traverse `slirp4netns` on this host at all**, so "blocked" was guaranteed regardless of policy — the
  R3 baseline finally showed it, with the container's *own* address unable to ping out before any rule
  existed. Two runs had already recorded the reassuring answer. The rule this yields is sharper than
  "use a control": **the control and the test must use the same protocol and the same path**, because
  a control that travels differently from the test is not a control at all.
- **R2 also hung and had to be killed.** It ran `apk add nftables` inside a container whose egress it
  had just denied — the third instance of that exact mistake in this directory (see L5d, L10c). Its
  earlier sections are valid and are kept: the agent holds `CAP_NET_ADMIN` but **not**
  `CAP_SYS_ADMIN` (`CapEff 800415fb`), and `/run/user/<uid>/netns` is not visible inside the
  container, which together are why it cannot reach the namespace that binds it.
- **R4's first run tested rule 0b against the wrong interface.** It hardcoded `iifname "podman1"`
  while that network sat on `podman2`, so the rule named an interface no packet ever arrived on and
  its counter stayed at 0 — reading exactly like "the fix does not work". R5 is the diagnostic that
  found it: outbound traffic matched neither the hardcoded bridge nor `veth*`. The design consequence
  is real and not just a harness bug — **rule 0b's interface must be resolved per network at install
  time**, never hardcoded, since podman and docker both name bridges per-network.
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
- **L5d's first run had a control that could not fail, and a verdict written in advance.** The script
  installed the in-guest layer *after* the host layer was already denying egress, so `apk add
  iptables` could not reach its repositories and the in-guest rules were never installed at all. Both
  of that section's controls — allowlisted still reachable, denied still blocked — were satisfied by
  the host layer that was still loaded, so they held while the layer under test did not exist. The
  file's closing verdict, written before the run, asserted the in-guest layer blocked sibling traffic;
  the data on the line above it said `REACHABLE`. Nothing in the run's own controls would have caught
  that. The redone version installs the tool first, prints `iptables -S OUTPUT` as an install check,
  and measures each layer with the other removed.
- **L10's first run: `nft -f` merges, it does not replace.** Loading B's policy while A's table
  still existed left A's rules *and* A's allowlisted peer in the set, so the negative control found
  the peer reachable and printed "REACHABLE (unexpected)". Both results were void. The control is
  the only reason that was visible; the test's own answer looked like a finding. Fixed by deleting
  the table before installing the replacement.
- **L10b is invalid on two counts and is superseded by `l10c`.** It tried to hold a conntrack entry
  in `ESTABLISHED` past the sandbox's death by dropping the flow's FIN/RST in a filter chain. That
  cannot work: conntrack hooks at priority −200, well below the filter chain, so it records the state
  transition before our rule ever drops the packet — the entry went to `FIN_WAIT` regardless. The
  address also was not recycled that run (B got `.2`, A had `.3`), so the tuple could not have
  matched anyway. L10 had a guard for exactly that and L10b had lost it; `l10c` restores it as a
  hard abort.
- **L10c's first run repeated L5d's trap.** A's policy binds B too, since they share an address, and
  it was still loaded while B ran `apk add bind-tools` — so `dig` was never installed and both the
  control and the test reported "blocked" for free. Every counter reading zero is what gave it away.
  The fix drops the table before B fetches tooling, and asserts `dig` exists before testing.
- **L9's first run produced a verdict from empty strings.** `docker0` had gone missing, so every
  container command failed, and the resolv.conf comparison duly concluded "IS shared" by finding two
  empty values equal. It now aborts when a value it is about to compare comes back empty.
- **L1b's probe control returned 0.** The within-run comparison still holds (see the file), but
  the separate cgroup control for the prerouting hook did not fire either, so it proves nothing.
  The conclusion rests on the L1 output-hook control and on the address counter in the same chain,
  not on this one.

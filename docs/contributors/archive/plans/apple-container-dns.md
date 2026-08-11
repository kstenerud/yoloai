> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since; the code it describes has moved. It is
> **not a specification** — do not build from it or cite it as the current answer. Good for
> archaeology only: see [`../README.md`](../README.md).

> **ABOUTME:** Specifies opt-in custom DNS resolvers for Apple Container sandboxes,
> including configuration precedence, lifecycle persistence, compatibility, and verification.

# Configurable DNS for Apple Container sandboxes

- **Status:** IMPLEMENTED
- **Depends on:** —
- **Rides:** a migration — persisted DNS requires the v6→v7 library migration; Apple Container
  no-network rejection also requires a breaking release

## Problem

Apple Container sandboxes can have working IP networking while the vmnet resolver fails. yoloAI
currently delegates resolver selection to Apple, leaving users without a durable workaround.

## Scope

Add an opt-in ordered list of custom IPv4 resolvers for Apple Container sandbox creation. No public
resolver becomes a default. Absent DNS inherits configuration; `system` and an explicit empty YAML
list select the vendor resolver; custom DNS is persisted and restored on recreation.

Only Apple Container supports custom DNS. Docker, Podman, containerd/Kata, Tart, and Seatbelt reject it.
Apple Container `--network-none` is rejected before setup for system and custom DNS until a separate
design provides an enforcing adapter and live egress-denial proof.

## User contract

- `yoloai new` and `yoloai run` accept repeatable `--dns <IPv4>` in order.
- A sole `--dns system` clears inherited DNS. It cannot be mixed with addresses.
- `network.dns` is a YAML sequence. An absent key inherits; `[]` and `[system]` select system DNS.
- Hostnames, IPv6, blanks, malformed YAML shapes, and mixed `system`/address inputs are rejected.
- An explicit create value replaces defaults or profile DNS; it never appends.
- Custom DNS works with `--network-isolated`; guest firewall rules permit UDP/TCP 53 only to the
  configured resolvers before broad allowlist accepts. System DNS keeps its existing behavior.
- Existing sandboxes retain their metadata snapshot after defaults change, reset, clone, or native
  recreation. `yoloai info` and the public environment model show active custom DNS.

## Design

`DNSResolvers` is the public tri-state creation type: `nil` inherits, non-nil empty selects system,
and a non-empty ordered slice is custom DNS. CLI and YAML adapters preserve this intent. The create
policy resolves precedence, validates IPv4 values, checks backend capabilities and network mode,
then invokes runtime setup. Invalid or unsupported intent therefore has no setup, image build,
replacement, teardown, or filesystem effect.

The resolved custom list is the only DNS value downstream receives. It is written to environment
metadata, state, runtime-config, and `runtime.InstanceConfig`. Apple translates it to repeated
`container create --dns <address>` arguments before the image; system DNS emits no DNS flag.

Environment metadata advances v3→v4 and realm schema advances v6→v7. The explicit framework
migration preflights every tiered sandbox record before mutation, accepts exactly v3/v4 metadata,
upgrades v3 to v4/system DNS durably, and advances the realm stamp last. It refuses malformed,
unreadable, unsupported, missing, or flat-layout records rather than stamping over them. Earlier
framework rungs preserve v3; ordinary readers require v4.

## Verification

- Unit and boundary tests cover public options, CLI/config/profile parsing and replacement, every
  backend capability, create-policy preflight, launch mapping, Apple argv, public observation,
  clone/reset/recreation, and metadata-authoritative runtime-config repair.
- Migration tests cover older schema sequencing, v3/v4 mixed records, malformed/unreadable/flat
  records, reruns, too-new realms, and stamp-last failure behavior.
- Python firewall tests cover custom UDP/TCP accept/reject ordering, ipset and per-IP fallback, and
  unchanged system-DNS behavior.
- The supported-host Apple integration test proves hostname resolution, explicit UDP/TCP DNS,
  canonical firewall ordering via `sudo -n iptables`, denial of a host-proven unselected resolver
  and unrelated TCP endpoint, and recreation after defaults change.

## Out of scope

- Changing Apple’s default resolver or introducing a default public resolver.
- IPv6, hostnames, search domains, DNS options, `--no-dns`, dynamic health checks, or DNS mutation.
- Custom DNS for non-Apple-Container backends or Apple Container image builds/persistent builders.
- Implementing Apple Container no-network before a real adapter and live enforcement proof exist.

## Release records

This is migration-bearing breaking release work on `release-v0.12.0`. The v3→v4/v6→v7 migration
and Apple Container no-network rejection are recorded under `docs/BREAKING-CHANGES.md#unreleased`;
the deprecation register retains the migration-only readers. The implementation and reachable
verification are complete. The live Apple Container, Python, and container-tool tiers remain
release-host evidence because their required infrastructure is unavailable in this workspace.

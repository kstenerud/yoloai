> **ABOUTME:** Let profile config declare named package-manager-cache volumes that persist
> across sandboxes, so new sandboxes on the same profile skip cold-cache re-downloads.

# Shared cache volumes

- **Status:** PLANNED — designed 2026-07-18; config syntax and the cross-backend mapping have
  recommendations below, with the trust-boundary and concurrency questions marked open.
- **Depends on:** —
- **Rides:** **any** release — purely additive and opt-in (a new profile config key + a new
  reclaim flag); no existing behavior changes.

Allow profile config to declare persistent, profile-scoped cache locations for package-manager
caches (npm, pip, cargo, …) that outlive a single sandbox, so a new sandbox on the same profile
reuses the warm cache instead of re-downloading every dependency. Inspired by
[amazing-sandbox](https://github.com/ashishb/amazing-sandbox) (~15 named cache volumes).

## Why it isn't a one-line addition

The mount pipeline is **bind-only** today. `runtime.MountSpec` (`runtime/runtime.go`) carries only
`HostPath` / `ContainerPath` / `ReadOnly`, `mounts.Build` (`internal/orchestrator/mounts/mounts.go`)
assembles host binds, and `ConvertMounts` (`runtime/docker/docker.go`) hardcodes
`mount.TypeBind` — there is no named-volume concept anywhere, and yoloai creates **no** volumes at
all right now. A per-sandbox `cache/` host dir exists (`Sandbox.CacheDir()`), but it is per-sandbox
and host-bound, i.e. exactly what this feature is *not*: it dies with the sandbox and isn't shared.

## The design

### 1. The abstraction: a profile-scoped shared cache location

Not "a Docker volume" — that's backend-specific. The unit is **a persistent, profile-scoped cache
mounted at a container path**, implemented per backend as whatever gives shared persistence:

| Backend | Implementation |
| --- | --- |
| docker / podman | a real named volume (`mount.TypeVolume`), created with the managed label |
| containerd | containerd has no native volume; back it with a managed **host directory** bind under the yoloai data dir (`caches/<profile>/<name>`) |
| tart | a host directory shared into the VM (virtiofs), same managed-dir shape |
| seatbelt | agents run on the host — a managed host directory bind |

So the plumbing change is: extend `MountSpec` with a `VolumeName` (or a `Kind: bind|volume` + a
`Source`), thread it through `state.State` → `mounts.Build`, and give `ConvertMounts` a
`TypeVolume` branch for docker/podman while the other backends resolve the name to a managed host
dir. The naming is `yoloai-<profile>-<name>` (per-profile to avoid cross-profile collision), and
every created volume/dir is stamped `com.yoloai.managed` — **required**, because DF137 just made
`VolumesPrune` reclaim only managed-labelled volumes, and these would be the first managed volumes
yoloai ever creates.

### 2. Config surface (recommended)

A profile config map of container-path → cache name:

```yaml
cache_volumes:
  npm: /root/.npm
  pip: /root/.cache/pip
  cargo: /usr/local/cargo/registry
```

The key is the cache name (→ `yoloai-<profile>-npm`), the value the in-container mount point. Merge
additively across a profile `extends:` chain, like `Mounts` already does. Open: map vs list form,
and whether a bare name implies a conventional path.

### 3. Reclamation

Cache volumes are **kept** by a normal `yoloai system prune` (they're the point). Add an explicit
`yoloai system prune --caches` to remove them. Because they carry `com.yoloai.managed`, the existing
DF137-scoped `VolumesPrune` already reclaims managed volumes on a plain prune today — so the design
must ensure cache volumes are **excluded** from the plain sweep (e.g. a second, cache-specific label
`com.yoloai.cache=true` that plain prune skips and `--caches` targets), or the warm cache is wiped by
routine cleanup. This is a concrete interaction to get right, not an afterthought.

## Trust boundary (the important open question)

A shared cache is a **cross-sandbox data-flow channel**. A compromised or adversarial agent can
**poison** a cache (a malicious package tarball, a tampered index) that a *later* sandbox — possibly
on a different task — then reads and executes. That directly undercuts the "disposable, isolated
sandbox" model and the egress-broker/isolation threat model built elsewhere. So:

- Shared caches should almost certainly be **off by default for `--network-isolated` / hostile-grade
  sandboxes**, or at minimum gated behind an explicit opt-in that documents the channel.
- Even for ordinary sandboxes, a poisoned cache is a real supply-chain vector between a user's own
  tasks. Read-only mounts don't help (the point is to *populate* the cache).

This is the decision that most shapes the feature and is the owner's to make.

## Open decisions

1. **Trust boundary:** allow shared caches under isolation/hostile mode at all? Default on or off?
2. **Concurrency:** several sandboxes on one profile share one RW cache concurrently. npm/pip/cargo
   caches are largely concurrency-tolerant (content-addressed), but not universally — confirm per
   manager, or serialize/limit.
3. **Config form:** the `cache_volumes` map above vs a list; conventional default paths.
4. **Base-profile defaults:** ship sensible npm/pip/cargo caches in the base profile, or opt-in
   per profile only (YAGNI leans opt-in first).
5. **Prune interaction:** the cache-vs-plain-prune label scheme (see Reclamation) so routine prune
   doesn't wipe the warm cache.

## Pointers

`runtime/runtime.go` (`MountSpec`); `internal/orchestrator/mounts/mounts.go` (`Build`,
`ParseConfigMount`); `runtime/docker/docker.go` (`ConvertMounts`, hardcoded `TypeBind`);
`internal/config/profile.go` (`Mounts`, chain merge); `runtime/docker/prune.go` (`managedLabel`,
DF137 volume scoping); `Sandbox.CacheDir()` (the per-sandbox dir this deliberately is not).

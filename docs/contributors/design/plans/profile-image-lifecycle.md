> **ABOUTME:** Plan for the profile-image lifecycle: one `ProfileImageBuilder` interface change
> serving DF152 and DF154's remaining half, plus containerd's missing implementation (DF153).
> Sequenced so the correctness fix lands before the optimisations that depend on its shape.

# Profile image lifecycle — staleness that reflects the store

- **Status:** IN-PROGRESS
- **Depends on:** —

Three open findings are one problem seen from three angles: **yoloAI decides whether to build a
profile image by reading a file, and that file describes a build that happened, not an image that
exists.**

| Finding | The angle |
| --- | --- |
| [DF152](../findings-unresolved.md) | The marker is keyed by backend *name*, and `docker` may address OrbStack, Docker Desktop or Colima — three stores, one key. |
| [DF153](../findings-unresolved.md) | containerd never implements the interface at all, so profile Dockerfiles are silently ignored there. |
| [DF154](../findings-resolved.md) | Nothing verified the image exists. **Done 2026-07-29** — the failure explains itself and recovery is automatic. |

[DF150](../findings-resolved.md) was the fourth and is fixed — it is why the marker is keyed by
backend at all.

## The ordering constraint

**DF154's recovery half must land before DF152**, and that is the one non-obvious thing here.

DF152's remedy is to stop relying on a host-side marker for docker and instead stamp the checksum
onto the image, the way `baseChecksumLabel` already does for the base image — staleness then
travels with the image, into whatever store holds it, and the multi-daemon problem disappears
because the question is asked of the store rather than of a filename. But reading a label means
asking the backend about an image, and *that* is the same check-then-act shape DF154 warns about:
the answer is true when given and the image is used later. It is a better *proxy* than a filename,
not a *guarantee*.

So the guarantee has to exist first. Once a missing image at launch is recoverable rather than
fatal, every staleness mechanism upstream is free to be an optimisation that is allowed to be
wrong — which is the only footing on which a label check (or any check) is safe to add.

Build in this order:

1. ~~**Recovery at the use site** (DF154).~~ **Done** — `createWithImageRecovery`, `internal/orchestrator/launch/launch.go`.
2. **containerd's implementation** (DF153) — independent of the interface change; can be done in
   parallel by someone else.
3. **Label-based staleness for docker/podman** (DF152) — the interface change.

## 1. Recovery at the use site — DONE (2026-07-29)

`createWithImageRecovery` wraps both `rt.Create` sites: on a missing-image failure it rebuilds the
profile chain and retries once. The obstacle this plan predicted — that launch lacks the build
inputs — did not materialise. `state.State` already carries `Layout`, `Profile` and `Output`, and
`profiles.AutoBuildSecrets` is a pure function of `Layout.HomeDir`, so neither of the two costed
options was needed and nothing was threaded through. Recorded because the prediction was wrong in
the cheap direction, and the next estimate should be correspondingly less confident.

The rebuild goes through a `var rebuildProfileImage = profiles.EnsureProfileImage` seam so the
retry policy is testable without a real backend, and the concurrency question that was in "Out of
scope" is now [DF155](../findings-unresolved.md) rather than a line in a plan that will one day be
archived.

**This is what unblocks step 3.** A missing image at launch is now recoverable rather than fatal,
so a staleness check upstream is free to be wrong.

## 2. containerd's implementation (DF153)

**containerd has no build of its own.** Its base image comes from shelling out to `docker build`
and linking the result into the containerd namespace — `runtime/containerd/image.go`,
`buildDockerImage` + `tryLink`, with a namespace-share/descriptor-walk fast path when Docker runs
in containerd-snapshotter mode. A profile build takes the same route.

Consequences worth stating before someone re-derives them:

- The recorded `backendKey` is `"containerd"` (the store the sandbox runs from) while the builder
  is docker. The base path already does exactly this — `RecordBuildChecksum(layout, "containerd")`
  after a `docker build`.
- containerd inherits docker's build environment and `--secret` support for free, unlike apple.
- The `ProfileImageBuilder` docstring's `buildEnv` contract still applies: the build subprocess
  draws from `buildEnv`, not from an env captured at construction.

**Prerequisite to check first:** whether the link step generalises to an arbitrary tag or is
special-cased to `yoloai-base`. If it is special-cased, that is the actual work.

## 3. Label-based staleness (DF152)

Mirror the base image: stamp the profile's Dockerfile checksum onto the image as a label at build
time, read it back to decide staleness. `checksumLabelStale` already exists and generalises with a
parameterised label name.

This is the interface change, and it is small:

```
ProfileImageNeedsBuild(ctx context.Context, profileDir, parentDir, tag string) bool
```

`ctx` and `tag` are what a label read needs and what the current signature lacks. Every
implementation and both call sites move together; there is no compatibility window worth keeping,
since the interface is internal to this repo's backends.

Then split by what a backend name means:

- **docker / podman** — one name, several possible daemons: read the label.
- **apple / containerd** — one name, one store: the host-side keyed marker is exact, keep it.

Keep both paths behind the one interface. Do **not** route the label read through a fresh
`ImageInspect`: `runtime/docker/docker.go`'s `imageExists` documents that inspect transiently
reports a present image as NotFound on the Docker Desktop containerd store, and that believing it
rebuilt `yoloai-base` from scratch on every smoke run until it was cross-checked against
`ImageList` with backoff. Reuse the hardened probe.

## Out of scope

The concurrent-build race recorded on DF154 — two `yoloai new --profile` runs both reading the
marker stale, both building, both writing it unlocked. Probably benign, unverified, and it wants
its own investigation rather than a lock added on suspicion.

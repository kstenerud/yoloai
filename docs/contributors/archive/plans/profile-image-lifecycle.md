> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since; the code it describes has moved. It is
> **not a specification** — do not build from it or cite it as the current answer. Good for
> archaeology only: see [`../README.md`](../README.md).

> **ABOUTME:** Plan for the profile-image lifecycle: one `ProfileImageBuilder` interface change
> serving DF152 and DF154's remaining half, plus containerd's missing implementation (DF153).
> Sequenced so the correctness fix lands before the optimisations that depend on its shape.

# Profile image lifecycle — staleness that reflects the store

- **Status:** IMPLEMENTED — all three steps done 2026-07-29; DF152/DF153/DF154/DF150/DF155 resolved, DF156 filed
- **Depends on:** —

Three open findings are one problem seen from three angles: **yoloAI decides whether to build a
profile image by reading a file, and that file describes a build that happened, not an image that
exists.**

| Finding | The angle |
| --- | --- |
| [DF152](../findings-unresolved.md) | The marker is keyed by backend *name*, and `docker` may address OrbStack, Docker Desktop or Colima — three stores, one key. |
| [DF153](../findings-resolved.md) | containerd never implemented the interface, so profile Dockerfiles were silently ignored there. **Done 2026-07-29**, verified on a real daemon. |
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
2. ~~**containerd's implementation** (DF153).~~ **Done** — `runtime/containerd/profile_image.go`.
3. **Label-based staleness for docker/podman** (DF152) — the interface change. **Paused**, not blocked: step 1 removed most of its value. See below.

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

## 2. containerd's implementation (DF153) — DONE (2026-07-29)

Built and verified against containerd v2.2.5 + Docker 29.6.1. The prerequisite was the work, as
predicted: the pipeline is now parameterised by tag, `Setup` still passes the `yoloai-base` const,
and `dockerRefFor(tag)` replaced the second hardcoded const. No parallel pipeline.

Three things learned here that step 3 should carry:

- **`requireAvailable` was too strong for image tests.** Its CAP_SYS_ADMIN stage exists for CNI, and
  image tests create no containers. `requireDaemon` is the split. Any DF152 test wants the same.
- **`tryLink` used to discard why it failed**, so a 50-80s fallback looked like the normal path. It
  now says. Expect more of this class in the import machinery.
- **The first import on a host is slow by construction.** BuildKit leaves fresh layers in the
  snapshotter; the compressed blobs only materialise when something exports them — which the slow
  fallback itself does. So a cold host takes the import path and every later link succeeds. Do not
  read a slow first build as a defect, and do not attribute a later fast one to whatever changed in
  between: that mistake was made here, with a plausible-looking 76s → 0.84s measurement that a
  control run refuted.

## 3. Label-based staleness (DF152) — REOPENED, and the shape changed

**The pause was wrong, and two checks show why** — both recorded on [DF152](../findings-unresolved.md). The provider-switch case is reachable through two first-class backends (`orbstack` and `docker-desktop` both resolve to the docker runtime with key `"docker"` and separate stores), and recovery does not cover `yoloai system build <profile>`, which consults the marker, skips, and prints "Profile image built successfully" without building anything.

But the fix is no longer necessarily the label change. Two cheaper, independent moves close both observed failures — key the marker by daemon endpoint (the Runtime already knows it, so no `ctx` needed), and make an explicit build verify rather than trust. Do those first; the label design below is the principled end state and may never be needed.

### The design, if it is built

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

The concurrent-build race, [DF155](../findings-resolved.md) — **checked 2026-07-29 and closed as
benign** on both image-based backends: identical digests, intact artifacts, converging marker
writes. The only cost is a duplicated build and import. A lock is now an efficiency option
(reusing `AcquireBaseLock`), not a correctness need.

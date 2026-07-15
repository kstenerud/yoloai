> **ABOUTME:** Plan and policy converting "skip a test when its backend infra is absent" into a
> hard failure on any machine that can host it, with an explicit carve-out only for environments
> we don't control, like CI.

# Plan: mandatory-infrastructure test policy (expunge skip-when-possible-but-absent)

- **Status:** IN-PROGRESS — IMPLEMENTED (A–F) + LINUX-VERIFIED on branch
  `mandatory-infra-test-policy`; macOS-side verification of the `GOOS` guard still pending.
  Landed as D112 (A gates + testutil helper, B/C/F harness+Makefile+tooling, D CI carve-out, E docs,
  unit tests). **Verified on a fully-provisioned Linux host (docker/podman/gVisor/containerd-Kata,
  /dev/kvm):**
- **Depends on:** —
- `make check` green (incl. `vet-tagged`); carve-out decision logic unit-tested on both layers.
- `make integration` (non-root, containerd carved) — docker/orchestrator/cli **run + pass, zero
  skips**; apple/seatbelt/tart structural exit-0 via the `GOOS` guard.
- **Deliberate-absence FAIL proven on the real containerd gate**: non-root + not-carved →
  `t.Fatalf` with the D112 message (exit 1); carved → clean skip; root/provisioned → runs + passes.
  (A live docker-absence test is defeated by `dialFirstAlive` self-heal, `runtime/docker/docker.go:189`
  — containerd has no self-heal, so it's the honest fail-path proof.)
- `make smoketest-quick` — real agent, docker core path, `--quick` narrowing correct.
- `make smoketest` (full matrix, real agents) — **22 passed, 0 failed, 0 skipped** across
  docker/docker-priv/gVisor/podman/podman-priv/containerd-vm/containerd-vmenhanced.

**Still pending (needs a macOS host):** the macOS side of the `GOOS` guard — apple/seatbelt/tart
*failing on absence* where the platform can host them (only their Linux exit-0 path was exercised here).

## The policy (from the maintainer)

> If it's technically possible to run the infrastructure on this machine, running it is
> **mandatory**. The only carve-out is systems we don't control (e.g. GitHub CI) — we can't
> control that environment, so we don't hard-fail there.

Corollaries:
- **Absence/misconfiguration of a platform-possible backend must FAIL, loudly — never skip.** A
  stopped docker daemon, an absent containerd socket, or a sandbox built without nesting is a
  misconfiguration to *fix*, and a silent skip hides it.
- **This sandbox (no nesting) should rightfully fail** integration/smoke after this change —
  that failure is the desired signal to rebuild it with nesting, not something to paper over.
- Ties to DEV §10 ("never … skip a test … to land code") and TEST §6. This plan makes the
  test infra *enforce* that principle instead of quietly undercutting it.

## What is the mistake (to expunge)

Two layers currently skip when platform-possible infra is merely absent:

1. **Smoke harness tiering** — documented verbatim at `scripts/smoke_test.py:253-260`:
   > the smoke *tier* does NOT change this set; it only changes how unavailable infrastructure
   > is treated: `make smoketest` **SKIPS** a backend whose prereqs are missing … while
   > `make smoketest-full` **FAILS loudly** for any missing infra.

   The base-tier skip is the mistake. Both tiers must fail on missing infra; the tier should
   select *breadth* (which backends/tests are in scope), never *skip-vs-fail*.

2. **Integration gates** — every backend's `TestMain` returns 0 ("… unavailable, skipping
   integration tests") when its daemon/host is absent, and containerd's per-test
   `skipIfNotAvailable` `t.Skipf`s. The `make integration` target *also* swallows docker
   absence (`echo "Docker unavailable — skipping…"`).

## What is NOT the mistake (preserve exactly)

Do not touch these — they are correct and must survive:

- **Structural / capability impossibility** — (test × backend) pairings no host change can make
  runnable (tart/seatbelt on Linux; gVisor-`-enhanced` honoring iptables isolation). The smoke
  runner **excludes these from scheduling** (`dind_applies`, `isolation_check_applies`,
  `uncovered_backends`, `smoke_test.py:410+`) rather than emitting a SKIP. Keep exclusion-from-
  scheduling; it is *not* a skip.
- **Platform build-tag gating** — `//go:build integration && linux` (containerd), darwin-only
  files (apple/tart/seatbelt), and the non-Linux stub `TestMain`s that exit 0. A macOS backend
  on Linux is "not technically possible," so it falls outside the policy (the rule only bites
  when the infra *is* possible on this platform). Keep.

The line between them: **structurally-impossible → exclude/compile-out (not possible here);
present-but-not-running / installable-but-absent → FAIL (possible here, so mandatory).**

## The carve-out mechanism (CI / uncontrolled envs) — DECIDED

**A list-valued, default-empty env var** (`YOLOAI_TEST_UNCONTROLLED_BACKENDS`), applied
per-step in the CI workflow we control:

- Value is a CSV of backend names the *current runner genuinely cannot provision*, e.g.
  `YOLOAI_TEST_UNCONTROLLED_BACKENDS=containerd,apple`. A scheduled backend whose name is in
  the list downgrades from FAIL to skip/return-0 when absent; **any backend NOT in the list
  still fails on absence, even in CI.** So if CI unexpectedly loses Docker, it fails loudly.
- Empty/unset (every dev machine, this sandbox) ⇒ **every** platform-possible backend is
  mandatory.
- **Name signals intent, not permission:** "uncontrolled backends," not "allow-absent" — it
  should feel wrong to set on a machine you control. Do **not** auto-detect `CI`/`GITHUB_ACTIONS`
  (a dev box exporting `CI=1` must not silently start skipping).
- Applied **selectively, per-step** in `.github/workflows/ci.yml` — Docker/podman are present on
  the Linux runner and stay mandatory; only steps the runner truly can't provision (audit
  needed: nested-virt for Kata? etc.) name their backend in the list.
- **Scope: backends only.** Dev tooling (rsync/python3/uv) is installable everywhere including
  CI, so it has no legitimate carve-out — it is unconditionally required (see §F).

## Change inventory

### A. Go integration gates → fail-unless-carve-out

Route all of these through the shared `internal/testutil` helpers from Resolved-decision §4
(`BackendAbsent` for `TestMain`, `RequireBackend` for per-test gates), which read
`YOLOAI_TEST_UNCONTROLLED_BACKENDS` once:

- `runtime/docker/integration_main_test.go:28` — `dockerErr != nil` → fail (was `return 0`).
- `runtime/podman/integration_main_test.go:23`
- `runtime/apple/integration_main_test.go:31`
- `runtime/seatbelt/integration_main_test.go:27`
- `runtime/tart/integration_main_test.go:31`
- `runtime/containerd/integration_main_test.go:21` — this is the **non-Linux stub** (platform-
  impossible) → keep exit 0 (build-tag/platform, not the mistake).
- `runtime/containerd/integration_test.go:33` `skipIfNotAvailable` → `requireAvailable`:
  `t.Fatalf` on socket-absent / not-connectable / netns-uncreatable (was `t.Skipf`), unless the
  carve-out env is set.
- `internal/orchestrator/integration_main_test.go` (the shared "sandbox" suite) — same as docker.

Keep the existing opt-in *scope* gates as-is (they select breadth, not infra-presence):
`YOLOAI_TEST_TART_VM`, `YOLOAI_TEST_APPLE_BASE` (slow builds), `YOLOAI_TEST_BACKEND=podman`.
These say "run this heavy thing," not "skip because absent" — different axis. (Re-confirm each
during implementation; e.g. `integration_tart_test.go:48`'s "set YOLOAI_TEST_TART=1 to enable"
is a scope gate, but verify it isn't masking an availability skip.)

### B. Smoke harness → always fail on missing scheduled infra

The lenient base tier is **removed entirely**; the two tiers become breadth-only and both are
strict (see §C for the target names — `smoketest` = everything, `smoketest-quick` = primary
machinery):

- `scripts/smoke_test.py` — **default = the full OS matrix, fail-on-missing.** Delete the
  skip-vs-fail tier semantics: a scheduled backend whose prereqs are missing is a `failure`
  (unless its name is in `YOLOAI_TEST_UNCONTROLLED_BACKENDS`). The old `--full` /
  `--all-docker-providers` breadth becomes the default; there is no lenient mode to fall back to.
- Add a **narrowing** flag for the quick tier (e.g. `--quick`, or reuse the existing test/backend
  filter) that schedules only the **primary machinery**: `backend=docker, isolation=container`
  (the core create → agent → sentinel → diff/apply path) plus the "required non-matrix" tests
  (`smoke_test.py:329`). Quick is **still strict** — Docker is primary machinery, so Docker
  absence fails. It does NOT schedule podman / gVisor(`-enhanced`) / container-privileged(dind) /
  containerd-vm / tart / all-docker-providers. (Confirm the exact quick set during impl; keep it
  to "does the primary path work," not leaf coverage.)
- Preserve structural exclusion (`dind_applies`/`isolation_check_applies`/`uncovered_backends`)
  and the end-of-run uncovered report — those are not skips.
- Rewrite the `smoke_test.py:253-260` comment (it currently *documents* the skip mistake).
- JUnit rendering: `skipped` only for the carve-out-listed path; a scheduled-but-absent backend
  on a controlled machine is a `failure`.

### C. Makefile — rename the smoke tiers (DECIDED)

- **`smoketest` → runs everything** (the full OS matrix; takes over today's `smoketest-full`
  body: `--full --debug --all-docker-providers`, Linux root-escalation kept). Strict.
- **`smoketest-quick` → new** (primary machinery only: `scripts/smoke_test.py --quick …` per §B).
  Strict — Docker must be present. This replaces the *fast dev signal* that the old lenient base
  tier provided, minus the leniency.
- **Remove `smoketest-full`** (folded into `smoketest`). Update `releasetest` (`Makefile:277`) to
  chain `smoketest` instead of `smoketest-full`; update the `.PHONY` + the `DOCKER_HOST` export
  line (`Makefile:30,32`) for the renamed targets.
- `integration` target (`Makefile:171-178`) — stop swallowing docker absence: the shell
  `if … else echo "Docker unavailable — skipping"` must go, so the suite fails when Docker is
  absent. (Under the carve-out list the Go gate returns 0 itself, so the Makefile needs no skip.)
- Rewrite the `## integration:` and `## smoketest*` header comments to the new policy.

### D. CI workflow

- `.github/workflows/ci.yml` (runs `make check`, `make integration`, `make e2e`,
  `make integration-podman`) — add the carve-out env **only** to steps whose backend the runner
  can't provision. Docker is present on the Linux runner → do **not** opt those out (they must
  stay mandatory). Audit what the runner actually provides (nested-virt for Kata? podman socket?)
  and opt out precisely those. `make check` needs no backend (already backend-free) — no change.

### E. Docs

- `docs/contributors/principles/testing-principles.md` §6 — rewrite the "Skip with `t.Skipf` if
  the backend isn't available locally" line. New wording: a platform-possible backend is
  mandatory; absence FAILS; the `integration` build tag keeps these out of the *unit* gate; the
  only skip is the CI carve-out env; structurally-impossible pairings are *excluded from
  scheduling*, not skipped. (This is the exact line that misled the earlier skip mistake — see
  the `sandbox allowed` fix as the "make it backend-free instead" worked example.)
- `docs/contributors/architecture/testing.md:61` — "each integration package self-skips when its
  daemon/host is absent" → fails, except under the carve-out; note the structural-exclusion vs
  absence-failure distinction and the env var.
- `CLAUDE.md` — update the testing paragraph(s): the smoketest description, and reconcile the
  `rsync`/`uv` skip lines (see Open decision 3).
- `docs/contributors/decisions/working-notes.md` — add **D112**: the mandatory-infra policy, the
  carve-out env, the structural-vs-absence distinction, and that it supersedes the base-tier
  smoke-skip. Cite DEV §10 / TEST §6.
- Optionally a short `backend-idiosyncrasies.md` note isn't warranted (this is policy, not a
  backend quirk).

### F. Dev tooling (rsync / python3 / uv) → required, no carve-out (DECIDED)

The Python surface is part of the app (contributors can modify it), so it must be checked; and
`rsync`/`python3` are installable everywhere including CI. These get **no** carve-out — they are
unconditionally required; absence is a hard failure (they are not backends, so
`YOLOAI_TEST_UNCONTROLLED_BACKENDS` does not apply to them).

- **`make python-test` / `python-typecheck`** — stop skipping silently when `uv` is absent;
  **fail** with a clear "install uv" message. `make check` then requires `uv` for a green run.
- **`CLAUDE.md`** — reverse the two skip lines: the `uv` "skip silently … so fresh clones still
  get a green `make check`" (line ~52) becomes "required; `make check` fails without it — install
  `uv`"; the `rsync` "lifecycle tests skip when `rsync` is absent" (line ~35) becomes "required".
- **`rsync`** (`internal/orchestrator/lifecycle/lifecycle_test.go:950,1047,1109`) — `t.Skip("rsync
  not installed")` → require (fail). `rsync` backs the in-place `reset` code path.
- **`python3`** (`runtime/seatbelt/seatbelt_test.go:708`) — `t.Skip("python3 not on PATH")` →
  require.

> **Consequence — the restart env must have `uv` (and `python3`, `rsync`) installed**, or
> `make check` itself will now fail on the Python step. This sandbox lacks `uv` (make check here
> already prints "Python type-check skipped (install uv)"), so post-change even the unit gate
> fails here until the environment is fixed — which is the policy working as intended.

## Resolved decisions (maintainer, 2026-07-05)

1. **Carve-out:** list-valued `YOLOAI_TEST_UNCONTROLLED_BACKENDS` (CSV of backends the runner
   can't provision), applied per-step in CI; any backend not listed still fails on absence. Name
   signals "uncontrolled," not "allow-skip." No `CI`/`GITHUB_ACTIONS` auto-detect. (See carve-out
   section.)
2. **Smoke tiers:** rename — **`smoketest` = everything** (full matrix, strict);
   **`smoketest-quick` = primary machinery only** (docker/`container` core path, strict); remove
   `smoketest-full`. Quick is a fast *narrow-but-honest* signal ("does the primary machinery
   work"), not leaf coverage. (See §B, §C.)
3. **Tooling scope:** Python is part of the app and must be checked → **require `python3` + `uv`
   + `rsync`** (fail if absent; no carve-out — they're not backends). `make check` now needs
   `uv`. (See §F.)
4. **Shared helper:** one predicate + two entry points in `internal/testutil`:
   - `func UncontrolledBackends() map[string]bool` (parse the env CSV once) — the single source.
   - `func BackendAbsent(name, reason string) int` — for `TestMain`: returns 0 if `name` is
     carved out, else prints `reason` and returns 1.
   - `func RequireBackend(t, name, reason string)` — for per-test gates (containerd): `t.Skip`
     if carved out, else `t.Fatal`.
   Every backend gate routes through these — one env read, one grep target.

## Verification (REQUIRES the nesting-capable environment)

Run after implementing, where the backends can actually run:

1. **Backends present:** `make integration`, `make smoketest`, `make smoketest-quick` all *run*
   (zero skips of a scheduled backend) and pass.
2. **Deliberate absence:** stop the docker daemon → `make integration` / `make smoketest` /
   `make smoketest-quick` **fail loudly** (not skip). Restore → pass.
3. **Carve-out:** with a backend absent + `YOLOAI_TEST_UNCONTROLLED_BACKENDS=<that-backend>` set
   → downgrades to skip/exit-0 (the CI path); a *different* absent backend still fails.
4. **Tooling (§F):** with `uv` absent, `make check` **fails** on the Python step (was: skipped);
   installing `uv` → green. Same for `rsync`/`python3` in their tests.
5. **This sandbox class (no nesting):** integration/smoke **fail** — the intended signal. (Can
   be observed here even pre-restart once the gates are changed, but the *pass* cases can't.)
6. **Unit gate stays backend-free:** `make check` still needs no daemon (backend-free after the
   `allowed` fix — no integration test leaked into the default gate); it now additionally
   requires the §F tooling (`uv`/`python3`/`rsync`) to be present.
7. `vet-tagged` (`go vet -tags 'integration e2e' ./...`) still typechecks all the edited gates.

## Sequencing

1. Land the shared carve-out helper + convert the six `TestMain`s and containerd gate (A).
2. Smoke harness (B) + Makefile (C) together (they're the `smoketest` pair the maintainer named).
3. CI workflow (D) — must land with/before A–C so the controlled-CI steps don't break.
4. Docs + D112 (E).
5. Verify (all of the above) in the nesting environment; iterate on real failures.

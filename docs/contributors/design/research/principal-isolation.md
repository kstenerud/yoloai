<!-- ABOUTME: Security research backing the multi-principal isolation axis (D59). -->
<!-- ABOUTME: Grounds the deployment-vs-principal split in yoloAI's CURRENT single- -->
<!-- ABOUTME: principal mechanisms, then asks what breaks when a daemon serves many. -->

# Principal isolation (multi-tenant) — security research

**Status: research, pre-design.** Commissioned by [D59](../../decisions/README.md) (which
extends [D58](../../decisions/README.md)) to settle the *isolation* axis of multi-principal
embedding before any plan doc. This file is the dedicated security pass the project's
security-research rule requires; it does **not** decide — it establishes facts and frames
the decisions the plan will make. Mechanisms named here as "recommended direction" still
need the plan's sign-off.

## Framing — the two axes, and what isolation means

When a daemon embeds the yoloAI library and serves **many principals** (Axis-1 = `many` in
D58's table), two orthogonal failures appear:

- **Binding (D58):** the library resolves an *ambient reference* against the wrong identity
  (confused deputy). Fix: the library never synthesizes principal scope.
- **Isolation (D59, this file):** principal A reaches principal B's *resources* (storage,
  workdir, credentials, container, network). Fix: partition + confine, per principal.

The CLI gets isolation **for free** today because process identity == the requesting
principal: the kernel's filesystem ACLs are the caller's own, so `open()` returns `EACCES`
on anything the user can't reach. A daemon running as its own service account **loses that
net** — the kernel enforces the *daemon's* entitlements, not the caller's. Everything below
is a consequence of that single lost guarantee.

**Litmus for every mechanism: does it fail _closed_?** A cross-tenant boundary that fails
open (one missed check leaks) is not a boundary. This is why D59 leans to physical
partition over scattered ACL checks.

## Current state (single-principal baseline)

Grounded in the code as of 2026-06-01 (pointers from a repo survey; trust-but-verify the
exact lines before relying on them in the plan). **None of this partitions by principal —
that is expected; yoloAI is single-principal by design.**

| Surface | Current mechanism | Pointer | Multi-principal gap |
|---|---|---|---|
| Sandbox storage | `DataDir/sandboxes/<name>/`, flat namespace | `store/paths.go` | A can name/enumerate B's sandbox; name collisions |
| Container naming | `yoloai-<name>` (fixed prefix only) | `store/paths.go:InstanceName` | Two principals' `yoloai-my-app` collide at the runtime layer |
| Container listing | reads the sandboxes dir + groups by backend; **no label filter** | `status/status.go:ListSandboxesMultiBackend` | enumeration returns *all* principals' sandboxes |
| Credentials (api keys) | written to ephemeral `/tmp/yoloai-secrets-*` (0600), bind-mounted RO at `/run/secrets/` | `provision/provision.go:CreateSecretsDir`, `mounts/mounts.go` | host temp dir is in **shared `/tmp`**, not the per-principal tree |
| Seed files (`~/.claude`, etc.) | copied into `<sandboxDir>/agent-state/` + `home-seed/` | `provision/provision.go:CopySeedFiles` | live in the sandbox dir → a storage partition protects them *iff* the partition holds |
| In-container uid | fixed `yoloai`/1001 (or host uid under podman keep-id / gVisor) | `store/meta.go:ContainerUser` | **all principals' containers run as the same uid** → no in-kernel user separation between them |
| Network | per-container netns (Docker default) + per-sandbox allow-list (iptables/ipset in entrypoint) | `runtime/isolation.go`, `network.go` | netns separates by default, but allow-lists aren't principal-scoped; no explicit cross-sandbox deny |
| Workdir / aux dirs | host path taken **as-is**; bind (`:rw`/`:ro`) or copy (`:copy`) or overlay | `state/state.go:DirSpec`, `mounts/mounts.go` | **no `filepath.Clean`/symlink canonicalization** on the host path |
| `files/` exchange | `<sandboxDir>/files/` ↔ `/yoloai/files/`; **has** a traversal guard | `store/paths.go:FilesDir`, `files.go:validateExchangePath` | guard is per-sandbox; cross-principal rests on POSIX perms only |
| Audit / attribution | **none**; `meta.json` has no owner/principal field | `store/meta.go:Meta` | no record of *who* created/accessed a sandbox |

The forward-looking note in `yoloai.go` ("`/var/lib/yoloai`, multi-tenant per-user roots
must pass this") confirms the seam was anticipated but **not enforced**: `Layout` takes a
`DataDir`/`HomeDir` but nothing makes a daemon partition them per principal.

## The four commissioned questions

### Q1 — Principal-id unforgeability (the partition's root of trust)

D59 chose a **physical partition** (`sandboxes/<principal>/<name>/`), which fails closed:
you can't construct a path to B's sandbox without B's id segment. That guarantee is **only
as strong as the id**. Open questions for the plan:

- **Who mints the id, and from what?** It must come from the daemon's *authenticated*
  principal context (the session/request auth), never from caller-supplied data. The
  library receives an already-authenticated, already-authorized id — consistent with the
  D58 invariant (library never synthesizes principal scope).
- **What characters are legal?** The id becomes a path segment, so it must be sanitized
  against traversal (`..`, `/`, NUL, leading `-`, length) **before** it touches
  `filepath.Join`. A naive id is itself a traversal vector — the partition that protects
  against cross-tenant access must not become the injection point. Prefer an opaque,
  validated token (e.g. a hash/uuid) over a human string, or sanitize+length-cap rigorously.
- **CLI compatibility.** Single-principal CLI should use a fixed default segment so its
  on-disk layout is unchanged. Confirm the default can't collide with a real daemon id.
- **Container naming must carry the id too** (see Blind spot B1) — the partition is
  filesystem-only; the runtime namespace is a *separate* collision/enumeration surface.

**Recommended direction:** opaque validated id from the auth layer; strict segment
sanitization at the `Layout`/partition boundary; default segment for CLI. *Decision owed to
the plan.*

### Q2 — Path confinement for workdir + aux dirs (the EACCES replacement)

This is the sharpest gap. Today the host path is taken **as-is with no canonicalization**
(`state/state.go`), and the CLI's kernel ACLs make that safe. A daemon must re-impose what
the kernel was doing for free:

- **Authorized-roots allow-list per principal.** Workdir + each aux dir must resolve
  *inside* a set of roots the principal is authorized for. Resolve with `filepath.EvalSymlinks`
  **then** check containment — checking before resolving is a classic bypass.
- **Symlink / TOCTOU (this is real, not hypothetical).** With no canonicalization today, a
  symlink *inside* a `:copy`/`:rw` workdir or aux dir can point at `/etc/shadow` or another
  principal's tree. Worse, **apply writes back** to the host: following a symlink on
  write-back escapes the confined root. The container sees the bind-mounted tree and can
  *create* such symlinks during the agent run, so the check must happen at
  copy/diff/apply time, not just at mount time (TOCTOU between mount and apply).
- **Apply write-back containment.** `apply` must refuse to write outside the authorized
  roots even if the diff names such a path — a typed refusal (§2 comply-or-complain), the
  daemon decides the reaction.

**Recommended direction:** per-principal authorized-roots; `EvalSymlinks`-then-contain on
every host path at mount, copy, diff, **and** apply; treat write-back as the highest-risk
operation. *This warrants its own design section in the plan; symlink/TOCTOU handling is
subtle.*

### Q3 — Enumeration leaks

`ListSandboxesMultiBackend` reads the whole sandboxes dir with no filter
(`status/status.go`). Under a physical partition, `List` naturally scopes to
`sandboxes/<principal>/` — **the partition does the filtering**, which is the fails-closed
win. But verify the *whole* read surface scopes, not just the headline `List`:

- Discovery verbs (`Agents`/`Backends`/`Archetypes`) read shared deployment state — fine,
  but confirm they leak no per-principal signal.
- Locks (`SandboxLockPath` lives in `sandboxes/<name>.lock` — *next to*, not inside, the
  dir; under partition it must move to `sandboxes/<principal>/<name>.lock`).
- `trash/` and `vscode-cli/` are per-principal (D59) — they must partition too, or `prune`
  / token-seed reads cross tenants.
- Shared-subtree reads (profiles/cache) must not reveal that *another* principal built a
  given profile/image (timing/existence side-channels — low severity, note it).

**Recommended direction:** partition makes enumeration fail-closed by construction; audit
every filesystem read path (not just `List`) to confirm it roots under the principal
segment. *Mechanical follow-up for the plan.*

### Q4 — The credential bundle (HomeDir dissolution)

D59 dissolves `HomeDir` into a typed credential/preferences bundle the caller supplies.
Research findings that shape it:

- Credentials currently take **two paths**: api keys → ephemeral `/tmp/yoloai-secrets-*` →
  `/run/secrets/` (RO); agent auth files (`~/.claude/.credentials.json`) and home prefs →
  copied into `<sandboxDir>/agent-state/` + `home-seed/`. The bundle must cover **both**:
  named secrets *and* seed-file content/locations.
- **`/tmp` is shared deployment space.** Even with a storage partition, the secrets staging
  dir is in `/tmp` — a daemon should stage per-principal secrets inside the principal's
  partition (or a private tmpfs), 0600, owned correctly, and clean up deterministically.
- **The bundle is the *what/where* split (D58 decision-4).** The library declares the
  *what* (agent definition's `APIKeyEnvVars` + state dirs); the caller supplies *where*/
  content. The library stops resolving `~/.claude` itself — the highest-stakes change,
  because that's real secrets, not path strings.
- **`${VAR}`-in-config under option B.** With raw-load + boundary-resolve, a daemon supplies
  a per-principal (or empty) env map; `${VAR}` in a shared profile resolves against
  *whose* env? Likely: shared deployment config resolves against deployment env (or not at
  all), principal-supplied config against principal env. The plan must state the rule —
  this is where a confused-deputy could re-enter through config.

**Recommended direction:** a typed bundle of {named secrets, seed sources, env map, identity
(uid/gid, git identity)}; stage secrets inside the principal partition; the library never
reads a host home. *Bundle shape is a plan deliverable.*

## Blind spots beyond the four (the part D59 didn't ask for)

The survey surfaced surfaces the four questions don't cover. Severity is my assessment —
**verify before relying.**

### B0 — Container escape reframes the entire filesystem partition *(CRITICAL — read first)*

The physical partition (Q1/Q3) protects against a principal using the **API** to reach
another's storage. It does **nothing** against an agent that *escapes its container*.
Containers share the host kernel; a container escape (kernel bug, misconfig, a mounted
docker socket, `CAP_SYS_ADMIN` for overlay) gives access to the **whole** `DataDir` —
every principal's partition at once. And yoloAI's threat model already treats **agent
output as untrusted** (`security-principles.md`) and runs agents *with intent to let them
act freely*. So:

- The cross-tenant boundary's real strength = the **runtime isolation** strength, not the
  filesystem partition. Backends rank: **Tart VM** (separate kernel) > **gVisor**
  (userspace kernel) > **plain Docker/Podman** (shared kernel) — plain containers are the
  weakest cross-tenant boundary and are the default.
- `:overlay` mode needs `CAP_SYS_ADMIN` (documented tradeoff) — that capability widens
  escape surface, and in multi-tenant it widens it *across* tenants.
- **Implication for the plan:** a multi-principal daemon on plain Docker may need to declare
  that cross-tenant isolation is "container-grade, not VM-grade," or require gVisor/Tart for
  true multi-tenant. This is a *policy* statement the daemon owns — but the library should
  surface the backend's isolation grade so the daemon can enforce it. **This likely deserves
  its own decision.**

### B1 — The runtime daemon is a second confused-deputy surface *(MAJOR)*

D58/D59 framed the *filesystem* confused-deputy. The **container runtime** (Docker socket /
containerd) is the same problem one layer down: yoloAI holds one root-equivalent connection
on behalf of *all* principals. Container names (`yoloai-<name>`) have no principal segment,
so:

- Two principals collide on `yoloai-my-app`; whoever creates second either fails or hijacks.
- `docker ps`-style enumeration (and yoloAI's own backend listing) returns all principals'
  containers — a principal could target another's container by name for stop/exec/logs if a
  verb takes a raw name.
- **Recommended:** principal-segment the instance name (`yoloai-<principal>-<name>` or a
  hashed form) **and** label containers (`com.yoloai.principal=<id>`) so runtime ops filter
  by label, mirroring the filesystem partition. Without this, the filesystem partition is
  half a boundary.

### B2 — Shared deployment resources as a poisoning / code-exec vector *(MAJOR)*

Profiles and base images are **shared, and building them runs arbitrary Dockerfile
commands**. If any principal can create/modify a shared profile (or influence a base-image
build, or poison the build cache), they achieve **code execution in every other principal's
sandboxes**. Questions the plan must answer:

- Are profiles deployment-scoped (operator-curated, principals can't write) or can principals
  define their own? D59 put `profiles/` in the *shared* tree — that's safe **only if
  principals can't write it**. If principals need custom profiles, those must move to the
  per-principal partition (and per-principal image namespaces), or be operator-gated.
- Base-image build locks (`*-base-locks/`) and the build cache are shared — confirm a
  principal can't poison a cached layer another principal consumes.
- **Recommended:** treat all shared deployment state as **read-only to principals**; any
  principal-authored build artifact (custom profile/image) lives in the principal partition
  with a principal-scoped image tag.

### B3 — Shared in-container uid breaks intra-host separation *(MODERATE)*

All principals' containers run as the same uid (1001, or the daemon's). If two containers
ever share *any* mount (a shared aux dir, a misconfigured volume, the `/tmp` secrets dir),
file permissions won't separate them. Also weakens defense-in-depth if a container escape
lands as a known uid. **Recommended:** consider per-principal in-container uid ranges
(user-namespace remap) for multi-tenant; at minimum, never share a writable mount across
principals.

### B4 — Secrets in logs + log-access scoping *(MODERATE)*

Agents are chatty and may echo secrets; yoloAI captures agent logs (`AgentLog` verb,
`agent-state`/log streams). In multi-tenant: (a) the `AgentLog`/`LogPaths` verbs must scope
to the principal's partition (covered by Q3 if logs live under the partition — **verify they
do**); (b) consider that logs may contain secrets, so log retention/access is itself a
credential-exposure surface. **Recommended:** confirm all log paths root under the principal
partition; document that logs may contain secrets.

### B5 — Principal-handle revocation / staleness *(MODERATE — the D58 Axis-2 piece)*

D58 noted long-lived bindings add *staleness*: a web-session or daemon handle can outlive the
principal's authorization (logout, key rotation, access revoked). The embedder-lifetime
handle (D59 decision-3) needs a revocation story: how is an in-flight or cached handle
invalidated, and what happens to a long-running sandbox whose principal's access was revoked
mid-run? **Recommended:** the handle carries no long-lived secret material itself (it
references the bundle, which the daemon can invalidate); define what a revoked-mid-run
sandbox does (orphan? freeze? destroy?). *Mostly a daemon-policy question, but the library
must not make revocation impossible.*

### B6 — Resource quotas / noisy-neighbor *(LOW-MODERATE, partly out of scope)*

One principal spawning many sandboxes exhausts shared resources (DataDir disk, the runtime
daemon, host CPU/mem). This is availability, not confidentiality, and partly a daemon
concern — but the per-principal partition makes per-principal disk accounting *possible*,
which the plan should at least not preclude. **Recommended:** note it; defer quota mechanism
to the daemon.

### B7 — Audit / attribution *(LOW for isolation, but a gap)*

No principal field in `meta.json`, no audit trail. Not an isolation *mechanism*, but a
multi-tenant deployment needs to attribute actions for incident response. **Recommended:**
add an owner/principal field to sandbox metadata (also reinforces the partition — metadata
can be cross-checked against the path segment as defense-in-depth), and note an audit-log
hook as future daemon work.

## What this feeds back into D58/D59 and the plan

- **The partition is necessary but not sufficient.** B0 (escape) and B1 (runtime deputy) and
  B2 (shared-resource poisoning) mean a filesystem partition alone does **not** deliver
  multi-tenant isolation. The plan must either (a) scope the daemon's multi-tenancy claim to
  the backend's isolation grade, or (b) require gVisor/Tart for true multi-tenant — a
  **new decision** beyond D59.
- **Two new partition surfaces** join the filesystem one: the **runtime namespace** (names +
  labels, B1) and the **secrets staging** location (Q4 — out of `/tmp`).
- **Path confinement (Q2)** is the single most code-invasive piece (symlink/TOCTOU on
  mount/copy/diff/apply) and should be its own plan section.
- **Shared vs principal-writable** for profiles/images (B2) is a scope question the D59
  shared/per-principal table glossed — it needs an explicit "principals cannot write shared
  deployment state" rule, or a per-principal profile/image namespace.

## Open verification items (not yet confirmed — needs external/empirical check)

- Exact container-escape surface per backend + whether `:overlay`'s `CAP_SYS_ADMIN` is
  cross-tenant-relevant (verify against current backend configs; cross-ref
  `backend-idiosyncrasies.md`, `sandboxing.md`, `linux-vm-backends.md`).
- Whether agent log paths + `runtime-config.json` already live *under* `sandboxDir` (so the
  partition covers them) — survey said yes for `files/`; confirm for all log streams.
- Whether the build cache / base-image layers can be poisoned cross-principal (Docker layer
  cache semantics).
- gVisor's actual cross-tenant guarantee vs plain Docker (claims need a real source, not
  marketing — per the project's factual-accuracy rule).

## Cross-references

- Decisions: [D58](../../decisions/README.md) (binding axis), [D59](../../decisions/README.md)
  (isolation axis).
- Principles: [`architecture-principles.md §3`](../../principles/architecture-principles.md)
  (the emerging frame), [`security-principles.md`](../../principles/security-principles.md)
  (containment-not-prevention, agent-output-untrusted, default-deny — the dispositions this
  research applies).
- Related research: [`security.md`](security.md) (credentials, network isolation, proxy),
  [`sandboxing.md`](sandboxing.md) + [`linux-vm-backends.md`](linux-vm-backends.md) (backend
  isolation grades, for B0).

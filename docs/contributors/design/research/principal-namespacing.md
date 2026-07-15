> **ABOUTME:** Research backing the runtime-namespace partition for multi-principal embedding,
> following on from principal-isolation.md's B1 finding. Verifies naming constraints across every
> backend and frames the schemes for folding a principal id into the runtime instance name — the
> reasoning trail behind the D62 namespacing decision.

# Principal namespacing (runtime instance names) — naming research

**Status: research with the namespacing decision now made (owner, 2026-06-03).** Follow-on to
[`principal-isolation.md`](principal-isolation.md), which surfaced blind spot **B1**: the
container/runtime namespace is principal-blind, so two principals each owning a sandbox
named `my-app` collide on the runtime instance name `yoloai-my-app`. A companion code
audit confirmed the **filesystem** layout is *not* a library-side blocker (the library
owns an opaque `DataDir`; a daemon hands each principal a distinct `DataDir`, giving a
fails-closed filesystem partition). The **runtime namespace is** the real library-side
blocker. This file establishes the verified naming constraints across every surface a
principal id would touch, frames the schemes for combining principal + sandbox name into a
runtime instance name, and records the decision the owner made from that framing.
Commissioned in the spirit of
[D58](../../decisions/working-notes.md)/[D59](../../decisions/working-notes.md) (binding + isolation
axes of multi-principal embedding).

> **Decision (2026-06-03).** The binding ceiling is containerd's enforced **`maxLength=76`**,
> not Docker's 63 — verified below (no backend uses the instance name as a DNS label or
> hostname, so the 63-octet DNS-label limit never applied). Against that 76 ceiling the
> instance name is the **deterministic concatenation `yoloai-<principal>-<name>`** (scheme
> (a)) with **`P_max = 8`** and **`N_max = 56`**: `7 + 8 + 1 + 56 = 72 ≤ 76` with a 4-char
> margin, so `MaxNameLength` **stays 56 (no breaking change)**. **No library-side hashing
> and no fallback** — the daemon supplies a bounded, unique, charset-safe `PrincipalSegment`
> (it owns the principal registry, so uniqueness is deterministic, not gambled on a
> birthday bound). This is [D58](../../decisions/working-notes.md)'s "library never synthesizes
> principal scope" applied to *length*. The schema is **two parsed boundary types** —
> `SandboxName` (≤56, containerd-conformant grammar, also closing DF16) and
> `PrincipalSegment` (≤8, alphanumeric, non-empty, daemon-supplied/validated, never
> synthesized) — so the instance name is **valid-by-construction**. `com.yoloai.principal` /
> `com.yoloai.sandbox` labels carry identity for fail-closed enumeration; a reserved default
> segment elides for the CLI so single-principal names stay exactly `yoloai-<name>`. The
> SHA-truncation / hybrid recommendation in the first draft of Q5 is **superseded** by this;
> the superseded analysis is retained below for the reasoning trail. Sizing the truncation,
> the 63-vs-76 budget, and the "shrink `MaxNameLength`" tradeoff are **no longer open**.

## The concrete blocker (ground truth)

`store/paths.go:130` —

```go
func InstanceName(name string) string { return "yoloai-" + name }
```

Principal-blind: the only namespacing is the fixed `yoloai-` prefix. Every backend takes
this string verbatim as the host-visible instance handle:

- Docker — `cfg.Name` is passed straight to `ContainerCreate(..., cfg.Name)`
  (`runtime/docker/docker.go:304`).
- containerd — `client.NewContainer(ctx, cfg.Name, ...)` *and*
  `client.WithNewSnapshot(cfg.Name, img)` (`runtime/containerd/lifecycle.go:246,235`),
  so the name is **both** the container id and the snapshot key.
- Tart — `tart clone <image> <cfg.Name>` then `tart run <cfg.Name>`
  (`runtime/tart/tart.go:217`); `instancePrefix = "yoloai-"` is the backend's own
  copy of the prefix (`tart.go:591`).
- Seatbelt — the name only derives the on-disk sandbox dir
  (`runtime/seatbelt/seatbelt.go:513-518`); there is **no** host-global
  process/VM registry keyed by the name (sandbox-exec is tracked by PID).

**Host-global, name-derived resources (the surprise from the audit).** On the containerd
backend the instance name leaks into *host-global* Linux/CNI namespaces, not just the
container id:

- Network namespace: `nsName := "yoloai-" + containerName` → `/var/run/netns/yoloai-yoloai-<name>`
  (`runtime/containerd/cni.go:193,129`). This is a **host-global** named netns —
  double-prefixed, so it eats *two* `yoloai-` prefixes of the budget.
- libcni results cache: `/var/lib/cni/results/yoloai-<containerName>-eth0` (`cni.go:150`).
- host-local IPAM lease files at `/var/lib/cni/networks/yoloai/<IP>` store the
  container name as their content and are matched by it (`cni.go:159-181`).

So a principal segment must survive being folded into the netns name too (Linux netns
names live at `/var/run/netns/<name>`, a normal filesystem path component — practical cap
is the filesystem `NAME_MAX` of 255, **not** the binding constraint). Docker bridge mode
(the default backend) does *not* derive a host-global netns from the name — Docker manages
the sandbox netns internally — so this host-global leak is **containerd-specific**. Verify
no other host-global derivation exists if a new backend is added.

`runtime/isolation.go` was checked: it is pure isolation-mode→OCI-runtime mapping
and derives **nothing** from the instance name. Good.

## Current naming rules (ground truth — cite these)

- `internal/config/names.go:10` — `const MaxNameLength = 56`.
- `internal/config/names.go:14` — `var ValidNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)`.
- The comment at `names.go:8-9` states the rationale verbatim: *"Docker container names are
  limited to 63 chars; with the 'yoloai-' prefix (7 chars), the name portion can be at most
  56."* So today max runtime name = 7 + 56 = **63**. **That rationale is mistaken** — 63 is
  the RFC-1123/RFC-1034 DNS-label limit, which does not bind because the instance name is
  never used as a DNS label or hostname (see the Q1 conclusion below). The real ceiling is
  containerd's `maxLength=76`. `MaxNameLength=56` happens to be safe either way, so the
  comment's *number* is fine even though its *reason* is wrong.
- `ValidateName` (`store/paths.go:113-127`) enforces `MaxNameLength` + `ValidNameRe` + a
  leading-`/`/`\` guard.

## Q1 — Verified naming constraints across every surface

For each surface: the verified rule, with a source (official docs / actual source-code
regex). The binding constraint is the **MIN length** across all surfaces that a real
principal id would touch.

| Surface | Charset / regex (verified) | Case | Min len | Max len | Source |
|---|---|---|---|---|---|
| **Docker container name** | `[a-zA-Z0-9][a-zA-Z0-9_.-]+` (sep not first char) | sensitive | 2 (regex `+`) | no hard cap in the regex; **63 practical** (DNS label, RFC 1034/1123) | moby `daemon/names/names.go` `RestrictedNameChars` [1]; 63 practical [5] |
| **Docker label key** | reverse-DNS convention: begin/end lower-case letter, lower-case alnum + `.` + `-`, no consecutive `.`/`-`. *Guidelines, not daemon-enforced.* | (convention) lower | — | no documented hard limit (**UNVERIFIED**) | Docker docs "object labels" [2] |
| **Docker label value** | any UTF-8 string (JSON/XML/etc.) | n/a | 0 | no documented hard limit (**UNVERIFIED**) | Docker docs "object labels" [2] |
| **Docker image tag** | `[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}` (no leading `.`/`-`) | sensitive | 1 | **128** | Docker tag reference grammar [3] |
| **Docker repo/image name** | lower-case alnum + `.`/`_`/`-` separators, path components `/`-joined | lower | 1 | per-component limits; legacy 30-char repo namespace cap (old) | distribution reference grammar [3] |
| **containerd id / name / snapshot key** | `^[A-Za-z0-9]+(?:[._-](?:[A-Za-z0-9]+))*$` — sep must be **surrounded** by alnum (no leading/trailing/double sep) | sensitive | 1 | **76** (`maxLength`) | containerd v2.2.2 `pkg/identifiers/validate.go:34-42` [4] (verified in module cache) |
| **Tart VM name** | passed verbatim to `tart clone/run`; Tart treats it as a directory name under its VM home | sensitive | 1 | filesystem `NAME_MAX` 255 (**UNVERIFIED** that Tart imposes nothing tighter) | Tart CLI (no published name grammar) — **UNVERIFIED** |
| **Seatbelt instance** | no host-visible instance name (derives sandbox dir only) | — | — | — (n/a) | `seatbelt.go:513-518` (code) |
| **Linux named netns (containerd only)** | filesystem path component at `/var/run/netns/<name>` | sensitive | 1 | `NAME_MAX` 255 | `cni.go:129`; Linux `NAME_MAX` |
| **CNI results cache / IPAM lease (containerd)** | filename / file content keyed by container name | sensitive | 1 | `NAME_MAX` 255 | `cni.go:150,159` |

**The binding length constraint is 76 characters — containerd's enforced `maxLength`.**
This *corrects the first-draft conclusion of 63.* The 63 figure is the RFC-1034 §3.5 /
RFC-1123 §2.1 DNS-label limit, and it binds **only if the instance name is used as a DNS
label or hostname.** It is **not** — verified across every backend:

- **Docker** sets **no** `Hostname` in `container.Config`
  (`runtime/docker/docker.go`); Docker assigns the *container ID* (not the name) as
  the hostname (confirmed by `runtime/monitor/sandbox-setup.py:901` "Docker assigns
  the container ID as hostname"), and `ContainerCreate` is called with an empty
  `network.NetworkingConfig{}` — **no network alias**, so the name is never a resolvable
  host on any Docker network.
- **containerd** builds the OCI spec with `oci.WithDefaultSpec()` + `oci.WithImageConfig`
  and **no** `oci.WithHostname` (`runtime/containerd/lifecycle.go`); the name is the
  container id + snapshot key, neither of which is DNS-resolved.
- **Tart / Seatbelt** never register the name as a hostname or network alias either.

So the DNS-label limit never applied to yoloAI's instance names; the in-code comment at
`names.go:8-9` (which derives 56 from "Docker container names are limited to 63 chars") is
**reasoning from a constraint that does not bind here.** The true MIN across the surfaces a
real instance name must satisfy simultaneously is **76 (containerd) < 128 (image tag) <
255 (netns / Tart filesystem)** — containerd is the binding ceiling because it is the only
one with a *hard, daemon-enforced* length cap that the name (as container id *and* snapshot
key) must pass. **Source for 76:** containerd v2.2.2 `pkg/identifiers/validate.go:34-42`
(`maxLength = 76`), verified in the pinned module cache [4].

Budget against 76: `76 − 7 (yoloai-) − 1 (separator) = 68`, so `P + N ≤ 68`. The locked
schema spends `8 + 56 = 64 ≤ 68` (margin 4). `MaxNameLength = 56` is unchanged.

**The binding charset constraint is the containerd identifier regex**, which is *stricter*
than yoloAI's `ValidNameRe`: containerd forbids leading/trailing/consecutive separators
(`a..b`, `a-`, `.a` are all rejected), whereas `ValidNameRe` permits a trailing `.`/`-`/`_`
and consecutive separators. A name yoloAI accepts today (e.g. `my-app-`) is a **valid
sandbox name but an invalid containerd id.** This is a pre-existing latent gap independent
of multi-principal; note it for the plan. (Docker's `+` also means container names must be
≥2 chars, while `ValidNameRe`'s `*` allows a 1-char name — another minor pre-existing
divergence.)

## Q2 — What shape is the principal id?

The daemon's **auth layer** mints the id (never yoloAI — consistent with the D58 invariant
that the library never synthesizes principal scope; `principal-isolation.md §Q1`). The id
becomes **both** a filesystem path segment **and** a runtime-name segment, so whatever its
native shape, it must be sanitized to the intersection of all charsets before either use.
`principal-isolation.md §Q1` already covers unforgeability and traversal-sanitization — not
repeated here. The *naming-specific* concern is **length × charset**:

| Realistic id shape | Length | Charset | Naming hazard |
|---|---|---|---|
| Opaque UUIDv4 (canonical) | 36 (`8-4-4-4-12` with hyphens) | hex + `-` | hyphens are legal everywhere; 36 chars busts the `P_max = 8` segment → the daemon must compress it |
| UUID, hyphenless hex | 32 | `[0-9a-f]` | safe charset; still 32 chars |
| Email / human username | unbounded | PII; `@ + . _` etc., often illegal/over-long | must be hashed or replaced; never use raw (PII + charset + length) |
| Opaque short token (e.g. base32, 8–16 chars) | 8–16 | alnum (if base32-no-pad) | the friendliest shape; daemon would have to mint these |

**Takeaway:** the library cannot assume the id is short or charset-safe — so it must not be
*handed* a raw id. The choice was (a) the daemon supplies an already-bounded, charset-safe
**`PrincipalSegment`** (push the constraint up to the auth layer), or (b) the library
**hashes/truncates** whatever it receives. **Decision: (a), no fallback.** The daemon owns
the principal registry, so it can mint a short unique segment deterministically; library
hashing would spend budget *gambling* (birthday bound) on the very cross-tenant-uniqueness
property the daemon can simply *guarantee*. This is the length-axis reading of
[D58](../../decisions/working-notes.md)'s "library never synthesizes principal scope": the library
receives a `PrincipalSegment` it only *validates* (≤8, alphanumeric, non-empty,
non-sentinel) and never derives. The unbounded shapes below (UUID, email) are therefore the
**daemon's** problem to compress into ≤8 chars before handing the segment down — not the
library's.

## Q3 — Combining principal + sandbox name into the instance name

All arithmetic is against the **76-char binding budget** (containerd `maxLength`; the 63
DNS-label figure does not bind — see Q1) and the fixed `yoloai-` prefix (7 chars), leaving
**69 chars** for `{principal-segment, sandbox-name, separators}`.

### Scheme (a) — Prefix / concatenation: `yoloai-<principal>-<name>` — **CHOSEN**

`7 (yoloai-) + P (principal) + 1 (sep) + N (name)` ≤ 76 ⟹ **P + N ≤ 68**.

This is the **decided scheme**, made viable by two things the first draft (which budgeted
against 63 and assumed a raw UUID/email principal id) lacked: (1) the real ceiling is 76,
not 63, freeing 13 extra chars; (2) the principal id is **not** carried raw — the daemon
hands down a bounded **`PrincipalSegment` (`P_max = 8`)**. With `P = 8` and the existing
**`N_max = 56`**: `7 + 8 + 1 + 56 = 72 ≤ 76`, a **4-char margin**, and `MaxNameLength`
**stays 56**. The first draft's "shrink to 19" problem was entirely an artifact of hashing
a 36-char UUID against a 63 budget — neither premise survived verification.

- The containerd netns double-prefix is comfortable: `/var/run/netns/yoloai-yoloai-<principal>-<name>`
  = `14 + 8 + 1 + 56 = 79` against `NAME_MAX` 255 — far under cap.
- Delimiter ambiguity (a `-` in both segments makes the split ambiguous) is **not** a
  problem here because reversal never relies on parsing the name: identity is read from the
  `com.yoloai.principal` / `com.yoloai.sandbox` labels, not by string-splitting the handle.

| Property | Assessment |
|---|---|
| Collision-safety | **exact** — no hashing, no birthday bound; the daemon guarantees `PrincipalSegment` uniqueness from its registry |
| Length-safety | **good** — bounded by construction (`P ≤ 8`, `N ≤ 56`, `≤ 72 ≤ 76`) |
| Human-debuggability | **best** — `docker ps` shows `yoloai-<principal>-<name>` directly |
| Reversibility (container→principal) | via labels (authoritative), not name-splitting |
| Enumeration filtering | by `com.yoloai.principal` **label** (real filter, fail-closed), not by name prefix |
| Blast radius on 24 call sites | low for the *name* (still deterministically computed from name+principal) but `InstanceName` gains a `PrincipalSegment` parameter → all 24 sites thread it |

### Scheme (b) — Hash the principal (or principal+name) — *NOT CHOSEN (no fallback)*

> Retained for the reasoning trail. Rejected because the daemon can *guarantee* segment
> uniqueness from its registry, so hashing would replace a guarantee with a birthday-bound
> gamble and burn budget on hex it doesn't need. The "no fallback" decision means the
> library has **no** hash path at all — an over-long or unsafe `PrincipalSegment` is a
> validation *error*, not something the library silently compresses.

`yoloai-<short-hash>-<name>` (hash principal only) or fully `yoloai-<hash(principal|name)>`.

- **Hash choice:** truncated SHA-256 (already in stdlib; no new dep; not a security MAC, so
  plain SHA-256 truncation is fine — the id's *unforgeability* is the daemon's job per
  `principal-isolation.md §Q1`, the hash only needs collision resistance for *naming*).
- **Collision math (birthday bound).** A truncated hash of `b` bits collides with ~50%
  probability at ≈ `2^(b/2)` distinct inputs. For a **64-bit** truncation (16 hex chars):
  50% at ~4.3×10⁹ inputs; for a realistic deployment of, say, 10⁶ sandboxes the collision
  probability is ≈ `(10⁶)² / 2^65` ≈ **1.5×10⁻⁸** — negligible. A **48-bit** (12 hex) tag
  gives ≈ `(10⁶)²/2^49` ≈ 1.8×10⁻³ at 10⁶ — too high; **80-bit** (20 hex) is comfortable at
  any plausible scale. *Recommend ≥64-bit, lean 80-bit (20 hex) if the budget allows.*
  (How many sandboxes per deployment is itself **UNVERIFIED** — the plan should pick a
  target scale; the math above lets it size the truncation.)
- **Length:** `yoloai-` (7) + 20-hex hash (20) + `-` (1) + N. If hashing **principal only**:
  `28 + N ≤ 63` ⟹ **N ≤ 35** (shrink from 56, but far better than (a)'s 19). If hashing
  **principal+name fully** (`yoloai-<40-hex-or-truncated>`), N drops out entirely and the
  name is unbounded *on the host* — but then the on-host name is fully opaque.

| Property | Assessment |
|---|---|
| Collision-safety | probabilistic; sized by truncation width (above) |
| Length-safety | **good** — bounded segment regardless of id shape (robust to email/username) |
| Human-debuggability | **poor** — `docker ps` shows an opaque hash; *must* be mitigated by labels (below) + storing the principal↔name mapping in the filesystem partition |
| Reversibility | **not** reversible from the name alone → needs the label or the on-disk map |
| Enumeration filtering | weak by name; **must** move to label filtering |
| Blast radius on 24 call sites | same threading as (a); lookups that recompute the name still work (deterministic hash) |

### Scheme (c) — Label-only + opaque/random instance name

Container name carries **no** semantic info (random or a per-sandbox stored id);
`com.yoloai.principal=<id>` + `com.yoloai.sandbox=<name>` labels carry identity; **all
enumeration filters by label.** This is the direction `principal-isolation.md B1`
explicitly recommends (label + filter, mirroring the filesystem partition).

The decisive question this scheme raises: **do yoloAI's lookups compute the name, or do
they look it up by stored id?** From the 24 call sites
(`grep -rn "InstanceName(" --include=*.go internal/ | grep -v _test`), characterized:

- **Compute-name-to-create** (the name is an *input* to create): `create/create.go:176,185`
  (cleanup-on-failure `Remove`), `launch/launch.go:71,110`. These mint the name.
- **Compute-name-to-look-up** (recompute the same deterministic name to address an
  existing instance): `attach.go:61`, `engine.go:199,229`, `terminal.go:48`,
  `launch/vmworkdir.go:32,39`, `launch/teardown.go:35`, `patch/apply.go:29`,
  `status/status.go:167,297,392`, `lifecycle/start.go:280`, `lifecycle/reset.go:91,113,338`,
  `lifecycle/lifecycle.go:33-34`. **Every one of these takes a sandbox `name` (already
  known from the on-disk sandbox dir) and recomputes `InstanceName(name)`** to call
  `Exec`/`Stop`/`Remove`/`DetectStatus` on the backend.

The crucial finding: **no call site looks a container up by scanning the runtime and
matching a name** — they always already hold the sandbox `name` (from the filesystem) and
recompute the handle. The one true *enumeration* path is
`status/status.go:ListSandboxesMultiBackend`, which (per `principal-isolation.md` Q3/B1)
reads the **sandboxes dir**, not the runtime — it does not call `InstanceName` to discover
instances. So **the runtime is addressed by a deterministically-derived handle, never
searched by name.** That means scheme (c)'s "random name" is feasible *only if* the derived
handle is replaced by a **stored** handle: each sandbox's `meta`/`environment.json` would
persist its runtime instance id, and all 24 sites would read it instead of computing it.
That is a larger refactor (a new stored field + 24 read-site changes) but removes name
derivation entirely.

| Property | Assessment |
|---|---|
| Collision-safety | exact (random/UUID name, store the mapping) — no birthday risk if 128-bit random |
| Length-safety | **best** — name is a fixed-width opaque token, name length decoupled from principal+sandbox entirely |
| Human-debuggability | poor on the name, but **labels make `docker ps --filter label=com.yoloai.principal=<id>` first-class** |
| Reversibility | via label or stored map (not the name) |
| Enumeration filtering | **strongest** — real label filter, the fails-closed mirror of the filesystem partition |
| Blast radius on 24 call sites | **highest** — must add a stored runtime-id field and convert all 24 compute-sites to read it |

### Hybrid `yoloai-<principal-hash>-<name>` **plus** labels — *first-draft pick, SUPERSEDED*

> The first draft recommended this (scheme (b)'s hashed name + scheme (c)'s labels). The
> final decision keeps the **labels** half but replaces the **hashed** name with scheme
> (a)'s **plain `PrincipalSegment`** — once the ceiling is 76 and the daemon supplies a
> bounded segment, hashing buys nothing and costs human-readability. The decided scheme is
> therefore "(a) plain concat **plus** (c) labels": deterministic recompute (zero new stored
> field, the 24 sites just thread the segment), a fully human-greppable
> `yoloai-<principal>-<name>` handle, and a real `com.yoloai.principal` label filter for
> enumeration.

## Q4 — Backward compatibility for the single-principal CLI

`principal-isolation.md §Q1` requires the CLI's on-disk layout to be unchanged via a fixed
default segment. The runtime name must likewise stay **exactly `yoloai-<name>`** for the
CLI/single-principal case.

**Mechanism: a sentinel "default principal" whose segment elides.** Define a reserved
default principal (e.g. the empty principal `""`, or a reserved sentinel like `_local`).
`InstanceName(principal, name)` special-cases it to emit the legacy `yoloai-<name>` with no
principal segment; any non-default principal gets its segment per the chosen scheme.

Non-collision proof obligation, per scheme:
- **(a)/(b) prefix or hash:** the sentinel elides its segment, so a real principal's name is
  `yoloai-<P>-<name>` and the CLI's is `yoloai-<name>`. These collide **iff** a real
  principal segment `P` could be empty or could make `<P>-<name>` reparse as a bare
  `<name>`. Guarantee non-collision by (i) forbidding the empty/sentinel value as a real
  principal id at the partition boundary (the same validation `principal-isolation.md §Q1`
  already demands), and (ii) for hashing, the hash output is fixed-width hex and can never
  equal the sentinel. **Safe.**
- **(c) label-only:** the CLI can keep computing `yoloai-<name>` and simply *also* set
  `com.yoloai.principal=<sentinel>`; the random-name variant would change CLI names, so for
  CLI compatibility the label-only scheme should retain the deterministic legacy name for
  the default principal. **Safe** with that caveat.

The reserved sentinel must be excluded from the legal principal-id charset (mirrors the
`base`-is-reserved profile rule). Decision owed to the plan.

## Q5 — Decision (owner, 2026-06-03)

**Decided direction: scheme (a) plain concatenation + (c) labels — `yoloai-<principal>-<name>`,
`P_max = 8`, `N_max = 56`, no library hashing, no fallback.**

- **Name:** the deterministic concatenation `yoloai-<principal>-<name>`. No hash. Both
  segments are parsed boundary types so the handle is **valid-by-construction**:
  - **`SandboxName`** — `≤ 56` chars, **containerd-conformant grammar**
    (`^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$`: separator surrounded by alphanumerics, no
    leading/trailing/consecutive separators, no 1-char-then-separator edge). Parsing here
    also **closes DF16** — `ValidNameRe`'s looseness (`my-app-` accepted) disappears once the
    name is a parsed type instead of a regex-validated string. `MaxNameLength` stays **56**.
  - **`PrincipalSegment`** — `≤ 8` chars, alphanumeric, non-empty, **daemon-supplied and
    library-*validated*, never synthesized** (the [D58](../../decisions/working-notes.md) invariant
    on the length axis). The library rejects an out-of-spec segment; it never hashes or
    truncates to rescue one (the "no fallback" decision).
- **Budget check:** `7 (yoloai-) + 8 (P) + 1 (sep) + 56 (N) = 72 ≤ 76` (containerd ceiling),
  4-char margin. No surface is exceeded (image-tag 128, netns/Tart 255 are looser).
- **Labels:** set `com.yoloai.principal=<id>` and `com.yoloai.sandbox=<name>` on every
  created instance (Docker labels; containerd labels; Tart has no native label — store the
  mapping in the sandbox dir for Tart). Move `ListSandboxesMultiBackend` and any future
  runtime-enumeration to **filter by label**, making cross-principal enumeration fail-closed
  by construction — the runtime mirror of the filesystem partition. Labels (not name-string
  splitting) are the authoritative principal↔instance map.
- **CLI compatibility:** a reserved default principal elides its segment → `yoloai-<name>`
  unchanged for the single-principal/CLI case; the default value is forbidden as a real
  `PrincipalSegment`, so a real principal can never collide with the elided form.

**Why not the alternatives:**
- (b) hashing / (the first-draft hybrid) — *rejected.* It existed only to compress a raw,
  possibly-long, possibly-unsafe id against a (mistaken) 63 budget. With the real 76 ceiling
  and a daemon-supplied bounded `PrincipalSegment`, both premises vanish: hashing would trade
  the daemon's *deterministic uniqueness guarantee* for a birthday-bound *probability* and
  make `docker ps` unreadable, for no length benefit.
- (c) pure label-only with a random/stored name — *rejected as unnecessary.* It demands a new
  stored-runtime-id field and converting all 24 compute-sites to reads, because the name
  would carry no derivable identity. Since **no call site searches the runtime by name**
  (every one recomputes a deterministic handle from the on-disk sandbox name), plain
  deterministic concat preserves every existing lookup with **zero new stored state** — the
  larger refactor buys nothing here. We still adopt (c)'s *labels* for enumeration.

**Folded into the schema (pre-existing, independent of multi-principal):** the `SandboxName`
parsed type **is** the fix for DF16 — a yoloAI-valid name can no longer be an invalid
containerd identifier, because the only constructor enforces the containerd grammar. This
also subsumes the DF15 parse-don't-validate straggler for sandbox names (the validate-style
`ValidateName` is replaced by parsing into `SandboxName`).

## Open / unverified items

*Closed by the decision:* the 63-vs-76 budget (verified: 76), the hash-truncation width and
per-deployment scale (no hashing — N/A), and whether `MaxNameLength` should shrink (it does
not — stays 56). The following remain open and feed the implementing plan:

- **Docker label key/value length caps** — the Docker docs give *conventions* but no
  documented hard length limit for keys or values. UNVERIFIED. *Check:* moby source for any
  label-length validation; in practice the `com.yoloai.principal=<id>` value is short, so
  this is low-risk, but confirm before relying on labels for arbitrary-length ids.
- **Tart VM name grammar/length** — Tart publishes no name-validation grammar; the code
  passes the name verbatim to `tart clone/run` and as a directory under the VM home.
  UNVERIFIED that Tart imposes nothing tighter than the filesystem `NAME_MAX` (255).
  *Check:* Tart source / empirical test with a 60-char name. (The decided 72-char max handle
  is well under 255, so this is only a charset concern, not length.)
- **containerd snapshot-key reuse** — `WithNewSnapshot(cfg.Name, img)` keys the snapshot on
  the same name; the `SandboxName` grammar is the containerd *identifier* grammar, which is
  the same validation containerd applies to snapshot keys — so a valid-by-construction
  handle is valid as a snapshot key too. Confirm empirically once implemented.
- **Other host-global name-derived resources** — the audit found the containerd CNI netns +
  IPAM lease + results cache (all under `NAME_MAX` 255, non-binding). UNVERIFIED that no
  *other* backend (or future backend) derives a host-global resource from the name.
  *Check:* re-grep on any new backend.

## Cross-references

- Research: [`principal-isolation.md`](principal-isolation.md) — **B1** (runtime confused
  deputy, the parent of this study), **Q1** (principal-id unforgeability/sanitization, the
  charset/forgery half not repeated here), **Q3** (enumeration leaks — labels here are the
  runtime mirror of the filesystem partition there).
- Decisions: [D58](../../decisions/working-notes.md) (binding axis — library never synthesizes
  principal scope), [D59](../../decisions/working-notes.md) (isolation axis — physical partition),
  [D62](../../decisions/working-notes.md) (**the namespacing decision recorded from this study** —
  deterministic `yoloai-<principal>-<name>`, `P≤8`/`N≤56`, no library hashing).
- Code ground truth: `store/paths.go:113-132` (`ValidateName`,
  `InstanceName`); `internal/config/names.go:10,14` (`MaxNameLength`, `ValidNameRe`);
  `runtime/{docker/docker.go:304, containerd/lifecycle.go:235-246,
  containerd/cni.go:129-193, tart/tart.go:217-591, seatbelt/seatbelt.go:513-518}`.

## Sources (verified external facts)

[1] moby container-name charset (`RestrictedNameChars = [a-zA-Z0-9][a-zA-Z0-9_.-]`):
<https://github.com/moby/moby/blob/master/daemon/names/names.go>
[2] Docker object labels (key/value conventions; reverse-DNS; no documented length cap):
<https://docs.docker.com/engine/manage-resources/labels/>
[3] Docker tag grammar (`tag-start tag-follow{0,127}`, 128-char max) + distribution
reference grammar: <https://docs.docker.com/reference/cli/docker/image/tag/>
[4] containerd identifier validation (`maxLength = 76`, regex
`^[A-Za-z0-9]+(?:[._-](?:[A-Za-z0-9]+))*$`) — verified in the project's pinned module
cache `containerd/v2@v2.2.2/pkg/identifiers/validate.go:34-42`; upstream:
<https://github.com/containerd/containerd/blob/main/pkg/identifiers/validate.go>
[5] RFC 1034 §3.5 / RFC 1123 §2.1 — DNS label limit of 63 octets (the 63-char container-name
practical cap and the basis for yoloAI's `MaxNameLength = 56`).

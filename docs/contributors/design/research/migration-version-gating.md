# Migration-version gating — stepping-stone upgrades & detect-and-refuse

ABOUTME: Verified prior art for retiring an on-disk feature via a designated
ABOUTME: migration version + a newer version that detects and refuses leftovers.

Status: **Verified 2026-06-30.** Backs the `:overlay` retirement migration path
([plans/retire-overlay-reflink-copy.md](../plans/retire-overlay-reflink-copy.md),
D109) and, more generally, any future "remove an on-disk format" change. Pairs
with [crash-safe-migration.md](crash-safe-migration.md) (the *mechanics* of a
crash-safe transform) — this file is the *upgrade UX / version-gating* half.

> Decision context: overlay→copy flatten cannot be done by the post-removal binary
> (the git baseline is container-internal — see the codebase map in the retire
> plan), so the conversion must run in a **migration version** that still supports
> overlay, while the new binary carries zero overlay-read code and instead
> **detects + refuses** a leftover overlay sandbox and points at the migration
> version. This is the textbook "stepping-stone upgrade" pattern; this file mines
> how mature systems do it.

## The pattern, and who implements which parts

Every system below implements some subset of three moves:
**(a)** a mandatory intermediate ("stepping-stone") version you must pass through;
**(b)** the conversion is performed *while the old format is still supported*;
**(c)** the newer version *detects + refuses* legacy state rather than reading it.

| System | (a) stepping-stone | (b) convert-while-supported | (c) detect + refuse |
|---|---|---|---|
| Elasticsearch | last minor of prev major | Upgrade Assistant / reindex | node fails to start |
| PostgreSQL | both binaries present | `pg_upgrade` (separate step) | control-file version check |
| MongoDB | successive majors, FCV gate | set FCV before/after | FCV-change command errors |
| Kafka | sequential metadata.version | IBP pinned old → bump last | broker/controller self-terminates |
| Kubernetes | one minor at a time | StorageVersionMigration / webhooks | removed API 404s; pre-warn header |
| Debian | no release-skipping | (policy only) | none (policy-unsupported only) |

## Verified facts

### Elasticsearch / OpenSearch — the cleanest match

- **Stepping-stone is mandatory:** "To perform a major upgrade from 8.x to 9.x …
  you must first upgrade to 8.19.x" — the conversion tooling (Upgrade Assistant)
  lives *only* in that last minor.
  ([prepare-to-upgrade](https://www.elastic.co/docs/deploy-manage/upgrade/prepare-to-upgrade))
- **Convert while supported:** index compatibility is one major back only;
  "Indices created in 7.x or earlier must be reindexed, deleted, or archived …
  before upgrading to 9.x." The Upgrade Assistant reads the deprecation-info API +
  logs and guides resolution; the machine-readable equivalent is
  `GET /_migration/deprecations` (the UI is Kibana-only).
- **Detect + refuse (from source):** on startup `IndexMetadataVerifier` throws
  `IllegalStateException` and the node won't start, with a message naming **the
  offending index, its version, the minimum supported version, and the exact
  remedy + where to do it** ("should be re-indexed in Elasticsearch
  (Version.CURRENT.major - 1).x before upgrading"). This five-element message is
  the **gold standard** for a refusal.
  ([IndexMetadataVerifier.java](https://raw.githubusercontent.com/elastic/elasticsearch/main/server/src/main/java/org/elasticsearch/cluster/metadata/IndexMetadataVerifier.java))
  — The rendered phrasing changed across releases ("was created with version" →
  "has current compatibility version"), i.e. it is version-specific.

### PostgreSQL — the sharpest "new binary can't read old state"

- "For *major* releases … the internal data storage format is subject to change";
  minor releases never do. ([upgrading](https://www.postgresql.org/docs/current/upgrading.html))
- **Both installations must coexist at conversion time:** "Always run the
  `pg_upgrade` binary of the new server, not the old one" with `-b oldbin -B
  newbin -d olddata -D newdata`. ([pgupgrade](https://www.postgresql.org/docs/current/pgupgrade.html))
- **Refuse:** "There are checks in place that prevent you from using a data
  directory with an incompatible version … so no great harm can be done by trying
  to start the wrong server version." (reads `PG_VERSION` + `global/pg_control`;
  the literal `FATAL: database files are incompatible` string is source-level, not
  in the manual — a UX gap, see takeaways).
- **Not idempotent — revert+retry:** on a failed schema restore "you will have to
  revert to the old cluster … modify the old cluster so the … restore succeeds."

### MongoDB — `featureCompatibilityVersion` (FCV)

- FCV "enables or disables the features that persist data incompatible with
  earlier versions," decoupling *running the new binary* from *enabling
  on-disk-incompatible features* — keeping a clean downgrade open until you opt in.
  ([setFeatureCompatibilityVersion](https://www.mongodb.com/docs/manual/reference/command/setfeaturecompatibilityversion/))
- **No skipping; raise FCV last:** upgrade to 7.0 requires running 6.0 with FCV
  6.0 first; order is FCV-at-old → swap binaries → raise FCV last. Downgrade
  mirrors: lower FCV + strip incompatible data *first*, then downgrade binaries.
  ([7.0-upgrade](https://www.mongodb.com/docs/manual/release-notes/7.0-upgrade-standalone/))
- **Friction gate:** 7.0 added a required `confirm: true` on the FCV change; omit
  it and the command fails with a warning — a deliberate "are you sure" on a
  hard-to-reverse change.
- **Stuck transitional state:** if an FCV change stalls, "any subsequent FCV
  downgrade attempts will also fail … You must complete the FCV upgrade before
  trying to downgrade." A cautionary anti-pattern (see takeaways).

### Kafka — protocol/metadata version gating

- **Two-phase:** binary version is decoupled from wire protocol via
  `inter.broker.protocol.version` (IBP) / KRaft `metadata.version`: deploy new
  binaries pinned to the *old* protocol, then bump the protocol *last* and roll
  again. ([upgrade](https://kafka.apache.org/33/getting-started/upgrade/))
- **Sequential, downgrade-blocked:** KIP-778 — KRaft upgrades "must proceed
  sequentially through intermediate versions"; "once the brokers begin using the
  latest protocol … it will no longer be possible to downgrade"; lossy downgrades
  require an explicit `--unsafe` flag.
- **Refuse by self-termination:** on an incompatible `metadata.version` a
  controller "will resign and terminate" and a broker "will unregister itself and
  terminate" (KIP-584); the controller validates and rejects an `UpdateFeatures`
  request before applying.

### Kubernetes — skew policy + staged deprecation

- **One minor at a time:** "Skipping MINOR versions when upgrading is
  unsupported." ([version-skew-policy](https://kubernetes.io/releases/version-skew-policy/))
- **Removal is conversion-bridged:** APIs removed only by bumping group version,
  with lossless round-trip conversion between served versions; stored objects are
  rewritten via StorageVersionMigration / CRD conversion webhooks before a version
  is dropped. `kubectl convert` rewrites manifests (now a separate plugin).
  ([deprecation-guide](https://kubernetes.io/docs/reference/using-api/deprecation-guide/))
- **Best-in-class pre-warning:** since 1.19 the apiserver emits an RFC-7234
  `Warning` header naming the deprecated version, **the removal release**, and the
  replacement; `--warnings-as-errors` fails CI; the
  `apiserver_requested_deprecated_apis{removed_release=…}` metric finds stale usage
  *fleet-wide before upgrading*. The removed-API runtime path, by contrast, returns
  a generic `no matches for kind` — a *worse* refusal than its own warning path.
  ([blog: warnings](https://kubernetes.io/blog/2020/09/03/warnings/))

### Debian — no release-skipping (weakest enforcement)

- "Only upgrades from Debian 11 (bullseye) are supported" — buster→bookworm must
  pass through bullseye. Communicated by docs structure only; **policy-unsupported,
  not an apt-level block** — and correspondingly the easiest to get wrong.
  ([bookworm release notes](https://www.debian.org/releases/bookworm/amd64/release-notes/ch-upgrading.html))

## Design takeaways for yoloAI

### 1. Detect-and-refuse, don't auto-convert — and write a *good* refusal

The systems that touch user data at rest (ES, PostgreSQL, MongoDB) **never silently
auto-convert across a major boundary** — they refuse and point to an explicit
conversion step. PostgreSQL is the sharpest precedent and exactly our situation:
the new binary *cannot* read the old state, conversion is a separate invocation,
and that is treated as a feature. This **validates** the decision (new binary =
zero overlay-read code + a plain-int stamp check + fail fast).

A good refusal message has all five elements of the ES `IndexMetadataVerifier`
string: **(1)** the specific offending object (which sandbox), **(2)** the version
found vs the minimum supported, **(3)** the exact remedy, **(4)** *where* to do it
(name the migration binary version), **(5)** fail hard and early (no degraded
half-run). Anti-patterns the corpus reveals: PostgreSQL's real refusal text lives
only in source (users hit an undocumented string); Kubernetes' removed-API path
returns a bare `no matches for kind` that names neither cause nor fix. **A bare
"unrecognized/not found" is a bad refusal; one that names cause + fix + tool is a
good one.** Recommended shape:

> `Error: sandbox "<name>" uses the retired :overlay format (state version <N>);`
> `this build supports version <M>+ only. Convert it with yoloai <MIG_VERSION>:`
> `run "yoloai-<MIG_VERSION> migrate <name>" to flatten it to :copy, then retry.`
> `See <url>.`

### 2. Communicate the stepping-stone concretely

- **Name the exact version, never "an older version."** ES says "8.19.x", Mongo
  "the 6.0-series", Debian "Debian 11". The refusal message and docs must name the
  precise migration binary (ideally a pin/download command) — because the #1
  failure mode is the intermediate becoming unfindable.
- **Make conversion idempotent / resumable** — and beat the precedents' gaps:
  pg_upgrade is explicitly *not* idempotent (fail → revert → retry); Mongo's
  stalled FCV *blocks the inverse op* until resolved. yoloAI's `migrate` should be
  safely re-runnable: an already-converted sandbox is a no-op success; a
  partially-converted one resumes or is cleanly re-done. Convert to a side location
  and **flip the plain-int stamp last** (the analog of Mongo raising FCV last /
  Kafka bumping IBP last) so the stamp advances only when the conversion is fully
  durable. This is the crash-safe-migration substrate (DF68) applied.
- **Ship a pre-upgrade audit verb.** The best precedents expose a *queryable*
  "are you ready?" signal — Mongo `getParameter` FCV, k8s `apiserver_requested_
  deprecated_apis` + `--warnings-as-errors`, ES deprecation-info API. yoloAI should
  list every sandbox still on the overlay stamp (e.g. `yoloai migrate --check`, or
  surface it in `system status`/`doctor`) **while still on the old binary**, so the
  user learns *before* the new binary's hard refusal — which is the backstop, not
  the discovery mechanism.
- **Friction on the destructive step.** If flatten consumes the overlay upper,
  Mongo's required `confirm: true` is justified prior art for a confirm gate.

### 3. Pitfalls to design against

- **Users skip the stepping-stone.** Universal risk. Defenses: hard refusal *not*
  a warning (ES/PG/Mongo/Kafka all block; Debian only "policy-unsupported" and is
  the weakest); name the exact tool in the error so the skip self-corrects; ship
  the pre-upgrade audit so the requirement is discovered before the binary is
  already upgraded past the conversion path.
- **Partial conversions** are the hardest case (PG revert-and-retry; Mongo stuck
  FCV). Design against with **atomic stamp-flip**: keep the sandbox readable as
  overlay until the copy is fully durable, flip the stamp last, make a re-run
  detect-and-finish. Never leave a sandbox neither binary will touch.
- **The intermediate version becomes unavailable** — the structural weakness of
  *all* stepping-stone schemes, under-addressed by the corpus. Because yoloAI is a
  single distributed binary: **(a)** fold `migrate` into the *last release that
  still supports overlay* and keep that release published (pin it in the refusal +
  docs + BREAKING-CHANGES.md); **(b)** keep the **detector** (stamp check + named
  pointer) in the new binary *indefinitely* — it's cheap and turns a skipped
  upgrade into an actionable message instead of a generic parse failure. **The
  legacy *reader* can be deleted from the new binary; the legacy *detector* must
  never be.**

## Relationship to other work

- Backs [plans/retire-overlay-reflink-copy.md](../plans/retire-overlay-reflink-copy.md)
  (D109) — resolves its Open Question 1 (migration policy) toward
  migration-version-converts + new-binary-detect-and-refuse.
- Pairs with [crash-safe-migration.md](crash-safe-migration.md) — the atomic
  stamp-flip / idempotent-resumable conversion here *is* that substrate (DF68).
- Migration philosophy: dumb plain-int stamps; explicit fail-fast `migrate` owns
  recognition/validation (feedback: migration-versioning-philosophy).

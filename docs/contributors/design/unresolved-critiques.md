<!-- ABOUTME: Active queue of open design critiques for yoloAI. Resolved items drain to -->
<!-- ABOUTME: resolved-critiques.md; deferred to deferred-critiques.md; abandoned to abandoned-critiques.md. -->

# Open critiques

Active design critiques awaiting action. Each is drained to one of three co-located sinks once
settled: [`resolved-critiques.md`](resolved-critiques.md) (applied),
[`deferred-critiques.md`](deferred-critiques.md) (parked with a `Trigger:`), or
[`abandoned-critiques.md`](abandoned-critiques.md) (dropped with a `Why:`). Keep only live items
here — resolved entries belong in the sink, not as stubs.

> The **2026-05-30 Post-F1-Close round** is fully drained: G1/G2/G3/G4/G5/G6/G8 →
> [`resolved-critiques.md`](resolved-critiques.md); G7 (extension residue) →
> [`abandoned-critiques.md`](abandoned-critiques.md) (D66); the D53 read-model reshape closed with
> commit `2916e24`; carried-forward findings F6/F7/F9 done 2026-06-01.

---

## 2026-06-03 Public-API "right reasons" round (A1–A4)

A fresh critique pass over the public surface (root `yoloai` package: `Client`/`SystemClient` +
`Sandbox`/`Workdir` sub-handles, ~18 option structs, ~40 type aliases), checking that each design
choice was made for a **good reason** (ease of use, discovery, clean surface, separation of
concerns, no unnecessary coupling) rather than a **bad** one (legacy/back-compat, implementation
difficulty). Originally four findings; A2/A3 share a root cause (eager backend construction).

> **Disposition so far.** **A1** → resolved via the "alias by default, mirror on demand" principle
> ([`development-principles.md`](../principles/development-principles.md) §4); drains to
> [`resolved-critiques.md`](resolved-critiques.md). **A4** → original public-struct-tag premise
> abandoned ([`abandoned-critiques.md`](abandoned-critiques.md)); the genuine residual (CLI `--json`
> has no structural convention) split to [`unresolved-findings.md`](unresolved-findings.md) DF17.
> **A2/A3** remain live below (disposition chosen: lazy backend init; not yet implemented).

### A1 — mirror-vs-alias is decided by implementation convenience, not contract design

**The choice.** Whether a public type is hand-mirrored or aliased to its internal definition is
decided by "did the internal struct happen to need a field dropped/renamed?":
`CloneOptions`/`ResetOptions` are mirrored ("so the public surface doesn't expose
internal/sandbox.X" / "carries a Name field the handle supplies, so it's dropped"), while
`StartOptions = sandbox.StartOptions` is aliased ("its fields are all legitimate start-time knobs,
so no field cleanup is needed"). Same class of type (an embedder-held struct), opposite treatment;
the discriminator is *effort*, not contract. ~15 result/struct aliases inherit this:
`patch.ApplyResult`, `ExportResult`, `AppliedCommit`, `BaselineChange`, `BaselineLogEntry`,
`sandbox.TagInfo`, `TagOutcome`, `TransferTagsResult`, the `lifecycle` `Start/Reset/DestroyResult`
trio, `runtime.VMCensus/VMSlot/MountSpec/PortMapping`. Each exposes an **internal struct's field
layout as the public contract** — the public field set moves in lockstep with the internal struct,
and a field added internally appears publicly with no boundary to catch it. (The F1 detector
catches *internal-typed* fields, not field-shape coupling.) This is the exact coupling F1
hand-mirrored away for `Info`/`Environment`/`BackendReport`, left inconsistent everywhere else.

**Bad reason:** implementation difficulty. **Fix direction:** decide *one* principle — either all
embedder-held contract structs get a stable public definition, or consciously accept aliasing flat
structs as YAGNI (the detector guards leaks; mirroring is duplication) and document it. Today it's
neither — it's accidental.

**Exempted (good reason, leave as-is):** pure enum/typed-string aliases — `BackendName`,
`AgentName`, `IsolationMode`, `DirMode`, `NetworkMode`, `Status`, `AgentStatus`, `LogSource`,
`NoticeLevel`. Underlying is `string`, no hidden fields; aliasing avoids conversion churn.

### A2 — the surface is split on "needs a live backend," fragmenting the per-sandbox noun

**The choice.** `NewWithOptions` requires `Backend` and eagerly opens it (`newRuntime`,
yoloai.go:249); `SystemClient` is layout-only. So operations on the *same noun* (sandbox named X)
split across two handles by an implementation property: backend-free reads →
`client.System().Prompt(name)`/`.AgentLog(name)`/`.Files(name)`/`.Unlock(name)`/
`.SandboxMetadata(name)`/`.VscodeAttach(name)`; backend-bound ops →
`client.Sandbox(name).Inspect()`/`.Attach()`/`.Exec()`/`.Stop()`/`.Workdir()…`. The embedder must
know which ops need a backend running. Stated reason (memory + comments): "building a Sandbox opens
the backend… a layout-only Sandbox whose Stop/Exec panic would be a footgun."

**Bad reason:** implementation difficulty. **Fix direction:** a lazy `Client` (open the backend on
first op that needs it) would let *all* per-sandbox ops live on `Sandbox(name)` — backend-free
reads never trigger a connection; `Stop`/`Exec` against an unavailable backend return a typed error
instead of panicking. Trade-off: lazy-init complexity (thread-safety, close semantics).

### A3 — `SystemClient` is a junk drawer; the "agent" noun has no home

**The choice.** `SystemClient` fuses six unrelated concerns: backend-free per-sandbox readers
(A2), host/fleet admin (`DiskUsage`/`Doctor`/`Build`/`Check`/`Prune`/`EmptyTrash`), config
(`Config()`), discovery (`Agents`/`Backends`/`Archetypes`), schema/migration
(`DataDirStatus`/`CreateFresh`/`Migrate`), and a backend-specific admin (`TartBases`). "System"
isn't one concern. Separately, D53's consumer model promised **three nouns — sandbox / changes /
agent** — but only `Sandbox` and `Workdir` (changes, a *clean* boundary) got handles; the **agent**
noun was never given one. Agent-interaction ops are smeared by the backend-liveness axis:
`Attach`/`SendInput`/`CaptureTerminal`/`ContainerLogs` on `Sandbox`, but `AgentLog`/`Prompt` on
`SystemClient`. The per-sandbox readers are filed under "System" *only* because they don't open a
backend.

**Bad reason:** same root as A2. **Fix direction:** largely dissolves if A2 is fixed —
`SystemClient` shrinks to honest host/fleet/admin, and the agent-interaction ops consolidate onto
the sandbox noun (optionally a `Sandbox.Agent()` sub-handle mirroring `Workdir()`).

> **A4 drained** — original premise abandoned ([`abandoned-critiques.md`](abandoned-critiques.md));
> CLI `--json` structural-convention residual tracked as [`unresolved-findings.md`](unresolved-findings.md) DF17.

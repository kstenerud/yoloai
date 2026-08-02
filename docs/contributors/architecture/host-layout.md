> **ABOUTME:** The on-disk layout under `~/.yoloai/` — the CLI/library namespace split, the
> per-namespace schema-version stamps, and what each path holds. The reference for "where does
> yoloAI keep this on disk" and how the startup migration gate reasons about that layout.

# Host Directory Layout

The CLI splits `~/.yoloai/` into two namespaces: `library/` (everything the
embeddable engine owns — what the library `Layout` is pointed at) and `cli/`
(CLI-only app state). The split is a CLI convention; an embedder that passes an
explicit `DataDir` gets the engine subtree directly under that path, with no
`library/` segment (see D60). Each namespace carries its own plain-text-integer
`.schema-version` stamp.

**Startup gate (D61).** The root `PersistentPreRunE` runs a read-only migration
gate (`internal/cli/gate.go`) before any command touches the data dir. It
create-freshes a genuinely new install (absent/empty `TOP`), fails fast with
"run `yoloai system migrate`" when a realm is out of date, surfaces an
inconsistent-data-dir error when exactly one realm is uninitialized, or proceeds.
It never migrates silently — all mutation of an existing dir lives in the
explicit `yoloai system migrate` command (`internal/cli/system/migrate.go`).
`version`, `help`, `completion`, and `migrate` are gate-exempt via the
`cliutil.AnnotationSkipMigrationGate` annotation.

```
~/.yoloai/
├── cli/                     # CLI-only app state (not the library's)
│   ├── .schema-version      # CLI realm stamp (plain int; cliutil CLIStatus/MigrateCLI)
│   ├── state.yaml           # CLI state (first_run_tip_shown)
│   └── extensions/
│       └── <name>.yaml      # User-defined extension commands
└── library/                 # Engine-owned — see "library/ contents" below
```

`library/` is what the library `Layout` resolves to (or the embedder's explicit
`DataDir`):

```
library/
├── .schema-version      # Library realm stamp (plain int; config.RealmStatus/MigrateLibrary)
├── config.yaml              # Global config (tmux_conf, model_aliases)
├── defaults/
│   ├── config.yaml          # User defaults (agent, model, isolation, etc.; active when no --profile)
│   └── tmux.conf            # Optional; written by setup when baked-in tmux config is in use
├── profiles/
│   └── <name>/
│       ├── config.yaml      # Profile settings (merged over baked-in defaults, not over defaults/)
│       ├── Dockerfile       # Optional; FROM yoloai-base
│       └── tmux.conf        # Optional tmux config override
├── sandboxes/
│   └── <name>/                   # Exactly three entries — every file sits in a tier
│       ├── host/              # Host-only tier — never shared into any guest
│       │   ├── environment.json   # Sandbox metadata (agent, workdir, baseline SHA)
│       │   ├── sandbox-state.json # Per-sandbox runtime state (agent_files_initialized, etc.)
│       │   ├── agent.json         # Resolved agent config
│       │   ├── netpolicy.json     # Network policy
│       │   ├── network-diag.txt   # Network diagnostics
│       │   ├── context.md         # Host-side reference copy of the environment description
│       │   ├── injector.json      # Credential injector pid/addr record
│       │   ├── injector.log       # Injector host-side log
│       │   ├── injector-token     # Injector placeholder token (never reaches a guest)
│       │   └── backend/           # Backend-specific files
│       │       ├── instance.json  # Backend instance config
│       │       ├── profile.sb     # SBPL sandbox profile (seatbelt)
│       │       ├── pid            # Process ID file
│       │       └── stderr.log     # Backend stderr log
│       ├── ro/                # Guest-read tier — the guest must not write these
│       │   ├── runtime-config.json # Runtime config (agent cmd, tmux settings)
│       │   ├── prompt.txt         # Agent prompt (if provided)
│       │   ├── resume-prompt.txt  # Prompt for a resumed session
│       │   ├── machine-id         # Stable per-sandbox machine id
│       │   ├── home-seed/         # Files symlinked into sandbox HOME
│       │   ├── secrets/           # Credentials staged for one launch, then removed
│       │   └── bin/               # Executable scripts
│       │       ├── sandbox-setup.py   # Consolidated setup script (all backends)
│       │       ├── status-monitor.py  # Idle detection monitor
│       │       └── diagnose-idle.sh   # Idle detection diagnostic
│       └── rw/                # Guest-read-write tier, and the guest's flat view
│           ├── agent-status.json  # Agent status (written by status monitor)
│           ├── setup.log          # Guest setup output (tart)
│           ├── logs/              # Agent, monitor and sandbox logs
│           ├── agent-runtime/     # Mounted at agent's StateDir (e.g., ~/.claude/, ~/.gemini/)
│           ├── files/             # Bidirectional file exchange (shared files directory)
│           ├── cache/             # Agent cache (HTTP responses, cloned repos)
│           ├── home/              # Sandbox HOME directory (seatbelt, tart)
│           ├── vscode-cli/        # VS Code CLI state
│           ├── tmux.sock          # Per-sandbox tmux socket (seatbelt) — at the
│           │                       # tier root, not under tmux/: a Unix socket
│           │                       # path is capped at 104 bytes (DF169)
│           ├── tmux/              # Tmux runtime
│           │   └── tmux.conf      # Tmux configuration
│           ├── work/
│           │   └── <caret-encoded-path>/  # Copy of workdir with internal git repo
│           └── <name> -> ../ro/<name>     # Links surfacing the read-only tier flat
└── cache/                   # Global cache directory (e.g., overlay detection, base image checksum)
```


## The sandbox dir is tiered by guest access

A sandbox directory is exactly three physical tiers — `host/` (never shared), `ro/`
(guest-read), `rw/` (guest-read-write) — so that a file's guest-access class is **where it
sits**, not an entry on a list that has to be maintained. There is no un-tiered place at the
root to put a new file. The invariant:

> **Host-only state is never inside a guest-visible region, and the guest reaches each
> region at the narrowest access it needs.**

Docker upholds it by binding named items and never the sandbox root; tart and seatbelt now
uphold it too, which is what closed DF136 and DF148 on every backend.

**Each backend expresses the tiers with what it has**, and the two mechanisms are not equally
strong:

- **docker, podman, containerd, apple** bind each needed file individually at its own
  read-only/read-write mode. The tiers never appear as directories in the guest, and `host/`
  is unreachable because nothing names it.
- **tart** publishes one VirtioFS share per guest-facing tier (`ro` mounted `:ro`, `rw`
  read-write) and none for `host/`, which is therefore absent from the guest's namespace.
  Its read-only tier is a real read-only mount and holds unconditionally.
- **seatbelt** has no mount namespace — the sandboxed process sees the real directory — so
  every tier boundary is an SBPL rule. Grants are per tier, and both non-writable tiers carry
  an **explicit trailing deny**, because on seatbelt the absence of a grant is not a denial:
  it holds only while nothing broader grants the same access, and the profile grants broadly
  (the temp tree, the caches, any enclosing mount). That applies to reads as much as writes —
  `host/` is denied both (DF161, DF170).

**The guest always sees one flat root, and on the two directory-sharing backends that root is
the `rw/` tier itself**, with each `ro/` entry surfaced inside it as a *relative* symlink
(`rw/bin -> ../ro/bin`, assembled by `config.AssembleGuestView`). The in-sandbox scripts join
every path from a single directory and are deliberately untouched by tiering. Two consequences
worth knowing:

- **A relative link resolves on both sides.** In a tart guest the tiers are two shares named
  for their tiers under one mount, so `../ro/x` means the same file there as on the host. An
  absolute target could only ever be right on one side.
- **The host never reads through the view.** Every host-side path comes from the builders, so
  it addresses the real tier. A guest that deletes or replaces one of these links breaks only
  its own view — it cannot make the host read a file of its choosing, which is what keeps
  DF148 closed.

An entry no one has classified is moved to `host/`, loudly: that direction can only remove
guest access (a missing file, which gets reported), where defaulting to `rw/` would silently
hand the agent something nobody classified. Classification lives in one table,
`internal/config/sandbox_tier.go`, which is also the v5→v6 migration's specification.

Build paths with the `internal/config` helpers (`EnvironmentPath`, `BinPath`, …), never an
ad-hoc `filepath.Join` on a sandbox dir: the builder is what makes a file's tier a single
place to change, and a call site that re-derives the layout silently opts out of it. This is
not hypothetical tidiness — every defect found while finishing the tiering was an ad-hoc join
that kept compiling and started pointing at nothing.

Design, sequencing and per-backend detail:
[sandbox-share-tiering.md](../archive/plans/sandbox-share-tiering.md).

## Build-staleness markers are keyed by backend

Several paths above cache a "have I already built this?" checksum next to the thing it
describes — the global `cache/` base-image marker, and a `.last-build-checksum` in each profile
directory. **These markers must be keyed by the backend that wrote them**, because the artifact
they vouch for is not shared: docker, podman, containerd and apple each keep their own image
store, so a marker written by one backend answers a question about an image another backend does
not have.

Both do this today: `baseImageChecksumPath(layout, backendKey)` for the base image (DF56) and
`profileChecksumPath(profileDir, backendKey)` for profile images (DF150). The profile marker was
unqualified until 2026-07-27, and the failure was neither hypothetical nor rare — build a profile
under docker, run it under `--backend podman`, and podman skipped a build whose image it did not
have, then failed pulling a local-only tag. It failed in both directions, on any host with two
container backends.

The invariant generalizes past checksums: **any host-side marker that stands in for a
backend-managed artifact is keyed by backend, or it is a lie for every other backend.**

**And the key is a backend *name*, which is a proxy for a store rather than the store itself.**
Where one name means one store — apple, containerd — the proxy is exact. The docker backend can
be pointed at OrbStack, Docker Desktop or Colima, so it is not: `"docker"` names three possible
stores. The base image handles this by not relying on a host-side marker at all, stamping the
checksum onto the image (`baseChecksumLabel`) so staleness travels with the image into whatever
store holds it. **When the artifact can carry its own staleness, that beats any host-side
marker** — the marker is what you use when it cannot. The profile path cannot yet, and that gap is
[DF152](../design/findings-unresolved.md).

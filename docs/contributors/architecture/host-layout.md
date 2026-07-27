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
│   └── <name>/
│       ├── environment.json   # Sandbox metadata (agent, workdir, baseline SHA)
│       ├── sandbox-state.json # Per-sandbox runtime state (agent_files_initialized, etc.)
│       ├── runtime-config.json # Runtime config (agent cmd, tmux settings)
│       ├── agent-status.json  # Agent status (written by status monitor)
│       ├── context.md         # Sandbox environment description (dirs, network, resources)
│       ├── prompt.txt         # Agent prompt (if provided)
│       ├── log.txt            # Session log
│       ├── monitor.log        # Status monitor debug log
│       ├── bin/               # Executable scripts
│       │   ├── sandbox-setup.py   # Consolidated setup script (all backends)
│       │   ├── status-monitor.py  # Idle detection monitor
│       │   └── diagnose-idle.sh   # Idle detection diagnostic
│       ├── tmux/              # Tmux runtime
│       │   ├── tmux.conf      # Tmux configuration
│       │   └── tmux.sock      # Per-sandbox tmux socket (seatbelt)
│       ├── backend/           # Backend-specific files
│       │   ├── instance.json  # Backend instance config
│       │   ├── profile.sb     # SBPL sandbox profile (seatbelt)
│       │   ├── pid            # Process ID file
│       │   └── stderr.log     # Backend stderr log
│       ├── agent-runtime/     # Mounted at agent's StateDir (e.g., ~/.claude/, ~/.gemini/)
│       ├── files/             # Bidirectional file exchange (shared files directory)
│       ├── cache/             # Agent cache (HTTP responses, cloned repos)
│       ├── home-seed/         # Files symlinked into sandbox HOME
│       ├── home/              # Sandbox HOME directory (seatbelt)
│       └── work/
│           └── <caret-encoded-path>/  # Copy of workdir with internal git repo
└── cache/                   # Global cache directory (e.g., overlay detection, base image checksum)
```


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

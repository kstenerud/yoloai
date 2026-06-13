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


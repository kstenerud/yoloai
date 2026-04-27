# System Overview

Visual guide to how yoloAI's components fit together. For detailed file indexes, type catalogs, and code pointers, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Layer Diagram

The codebase follows a strict top-down dependency flow. CLI commands call sandbox operations, which delegate to a pluggable runtime backend. No layer reaches upward.

```
┌─────────────────────────────────────────────────────────┐
│                      cmd/yoloai                         │
│                   (binary entry point)                  │
└──────────────────────────┬──────────────────────────────┘
                           │
┌──────────────────────────▼──────────────────────────────┐
│                    internal/cli                         │
│         Cobra command tree, flag parsing, TUI           │
└──────┬────────────────┬──────────────────┬──────────────┘
       │                │                  │
       ▼                ▼                  ▼
┌────────────┐  ┌──────────────┐  ┌──────────────┐
│  config/   │  │   agent/     │  │  extension/  │
│  profiles  │  │  definitions │  │  custom cmds │
└──────┬─────┘  └──────┬───────┘  └──────────────┘
       │               │
       ▼               ▼
┌──────────────────────────────────────────────────┐
│                   sandbox/                       │
│     Create, lifecycle, diff, apply, clone        │
└──────────────┬──────────────────┬────────────────┘
               │                  │
               ▼                  ▼
┌──────────────────────┐  ┌──────────────┐
│      runtime/        │  │  workspace/  │
│  interface, registry │  │  git, copy   │
└──────────┬───────────┘  └──────────────┘
           │
     ┌─────┼──────┬──────────┬────────────┐
     ▼     ▼      ▼          ▼            ▼
  docker podman containerd  tart      seatbelt
                 (Linux)   (macOS)    (macOS)
```

## Backend Plugin Architecture

Backends register themselves via `init()` functions. The registry maps names to factory functions. When the CLI needs a runtime, it calls `runtime.New()` which looks up the factory and creates the backend. Platform-specific backends only register on supported platforms (containerd on Linux; tart and seatbelt on macOS).

```
internal/cli/helpers.go            runtime_imports_linux.go
  import _ "runtime/docker"          import _ "runtime/containerd"
  import _ "runtime/seatbelt"
  import _ "runtime/tart"

Each backend's init() calls:
  runtime.Register(name, factory)

                ┌──────────────────────────────┐
                │      runtime/registry.go     │
                │                              │
                │  backends map[string]Factory  │
                │                              │
                │  Register(name, factory)     │
                │  New(ctx, name) → Runtime    │
                │  Available() → []string      │
                └──────────────┬───────────────┘
                               │
        ┌──────────┬───────────┼──────────┬────────────┐
        ▼          ▼           ▼          ▼            ▼
   "docker"   "podman"   "containerd"  "tart"    "seatbelt"
        │          │           │          │            │
        ▼          ▼           ▼          ▼            ▼
   ┌─────────────────────────────────────────────────────┐
   │             runtime.Runtime interface               │
   │                                                     │
   │  Setup · Create · Start · Stop · Remove · Inspect   │
   │  Exec · GitExec · InteractiveExec                   │
   │  Capabilities · RequiredCapabilities                │
   │  Prune · Logs · DiagHint · Close                    │
   └─────────────────────────────────────────────────────┘
```

## Sandbox Lifecycle

A sandbox progresses through well-defined states. The `active` state has sub-states based on agent activity. Stopped sandboxes can be restarted; removed ones still have their directory on disk for inspection.

```
              yoloai new
                  │
                  ▼
             ┌─────────┐
             │  active  │ ← yoloai start (from stopped)
             └────┬─────┘
                  │
        agent activity changes
         ┌────┬──┴───┬─────┐
         ▼    ▼      ▼     │
      idle  done  failed   │   (detected by status monitor)
         │    │      │     │
         └────┴──┬───┘     │
                 │         │
          yoloai stop      │
                 │         │
                 ▼         │
             ┌─────────┐   │
             │ stopped  │──┘
             └────┬─────┘
                  │
           yoloai destroy
                  │
                  ▼
             ┌─────────┐
             │ removed  │  (container gone, sandbox dir remains)
             └────┬─────┘
                  │
            delete dir
                  │
                  ▼
               (gone)

  Special states:
    broken      → sandbox dir exists but environment.json missing/corrupt
    unavailable → backend not running (e.g. Docker stopped)
```

## Workdir Modes

Each mounted directory has a mode that controls how changes are tracked and isolated. The primary workdir is positional; auxiliary dirs use the `-d` flag. The mode suffix (`:copy`, `:overlay`, `:rw`) is appended to the path.

```
Host                            Container
────                            ─────────

:copy (default for workdir)
┌──────────┐   full copy    ┌──────────────┐
│ original │ ──────────────►│ work/<path>/ │ ← agent edits here
│ project/ │                │ (git baseline)│
└──────────┘                └──────────────┘
     ▲                             │
     │     diff: git diff vs       │
     └──── apply: git apply ◄──────┘


:overlay (Linux only, needs CAP_SYS_ADMIN)
┌──────────┐                ┌──────────────────────┐
│ original │ ─── lower ────►│ overlayfs merged/    │ ← agent sees this
│ project/ │   (read-only)  │  upper/ has changes  │
└──────────┘                └──────────────────────┘
     ▲                             │
     │     diff/apply via git      │
     └──── inside container ◄──────┘


:rw (live bind-mount)
┌──────────┐   bind-mount   ┌──────────────┐
│ original │ ◄─────────────►│ same files   │ ← changes are immediate
│ project/ │   (read-write) │              │
└──────────┘                └──────────────┘
     no diff/apply needed — changes already live


:ro (read-only, aux dirs only)
┌──────────┐   bind-mount   ┌──────────────┐
│ original │ ──────────────►│ same files   │ ← read-only inside
│ library/ │   (read-only)  │              │
└──────────┘                └──────────────┘
```

## Create Flow

Creating a sandbox happens in three stages: prepare resolves all configuration, seed copies agent credentials and config files into the sandbox directory, and build/start launches the container or VM.

```
yoloai new myproject ./src:copy
          │
          ▼
┌─────────────────────── PREPARE (create_prepare.go) ──────────┐
│                                                               │
│  resolve profile chain  →  merge config  →  build image      │
│  resolve agent (CLI → profile → defaults → "claude")         │
│  resolve model + aliases                                     │
│  create sandbox dir (~/.yoloai/sandboxes/myproject/)         │
│  parse workdir modes, validate paths                         │
│  check dirty repos (warn unless --force)                     │
│  validate isolation prerequisites (caps check)               │
│  detect credentials                                          │
│                                                               │
│  output: sandboxState struct                                  │
└──────────────────────────────┬────────────────────────────────┘
                               │
                               ▼
┌─────────────────────── SEED (create_seed.go) ─────────────────┐
│                                                                │
│  copy agent seed files from host                               │
│    (e.g. ~/.claude/.credentials.json → agent-runtime/)         │
│  patch container settings (e.g. skip permission prompts)       │
│  copy user-configured agent_files                              │
│                                                                │
└──────────────────────────────┬─────────────────────────────────┘
                               │
                               ▼
┌─────────────────────── BUILD & START (create.go) ─────────────┐
│                                                                │
│  prepare workdir (copy files / create overlay dirs)            │
│  create git baseline (for :copy mode)                          │
│  generate runtime-config.json (entrypoint configuration)       │
│  prepare prompt delivery (interactive paste / headless arg)    │
│  assemble InstanceConfig (mounts, ports, caps, resources)      │
│                                                                │
│  runtime.Create()  →  runtime.Start()                          │
│                                                                │
│  save environment.json (sandbox metadata)                      │
│  attach to tmux session (unless --detach)                      │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

## Diff/Apply Flow

The core workflow: an AI agent makes changes inside the sandbox, the user reviews a diff, then applies approved changes back to the host. This protects the original project from unreviewed modifications.

```
  Agent works inside sandbox
            │
            ▼
  yoloai diff myproject
            │
            ▼
   ┌────────────────────────────────┐
   │  For each :copy/:overlay dir: │
   │    git add -A (stage changes) │
   │    git diff --binary baseline │
   └───────────────┬────────────────┘
                   │
                   ▼
         ┌──────────────────┐
         │   patch output   │ ← user reviews this
         │   (unified diff) │
         └────────┬─────────┘
                  │
        user approves
                  │
                  ▼
  yoloai apply myproject
                  │
                  ▼
   ┌─────────────────────────────────────┐
   │  For each :copy/:overlay dir:      │
   │    generate binary patch            │
   │    apply to host (git apply)        │
   │    advance baseline SHA in meta     │
   └─────────────────────────────────────┘
                  │
                  ▼
    Host project updated with agent's changes
```

## Configuration Hierarchy

Configuration merges bottom-up: baked-in defaults are overridden by user defaults, then profile settings, then CLI flags. Profiles can extend other profiles, forming a chain that merges left to right.

```
  ┌──────────────────────────────────────┐
  │           CLI flags                  │  ← highest priority
  │  --agent codex --model o3 --cpus 4  │
  └─────────────────┬────────────────────┘
                    │ overrides
  ┌─────────────────▼────────────────────┐
  │       Profile config chain           │
  │  ~/.yoloai/profiles/<name>/          │
  │         config.yaml                  │
  │                                      │
  │  my-project → python-base → base    │
  │  (child overrides parent)            │
  └─────────────────┬────────────────────┘
                    │ overrides
  ┌─────────────────▼────────────────────┐
  │         User defaults                │
  │  ~/.yoloai/defaults/config.yaml      │
  └─────────────────┬────────────────────┘
                    │ overrides
  ┌─────────────────▼────────────────────┐
  │       Baked-in defaults              │  ← lowest priority
  │  (config/defaults.go)                │
  └──────────────────────────────────────┘


  Two config scopes:

  Global config (~/.yoloai/config.yaml)     Profile/defaults config
  ─────────────────────────────────────     ──────────────────────────
  tmux_conf                                 agent, model, isolation
  model_aliases                             backend, network, resources
                                            env, mounts, ports, setup...

  config.IsGlobalKey(key) routes commands to the correct file.
```

## Capability Detection

The `yoloai doctor` command probes the host environment, checks each backend's prerequisites, and reports what's available. Failing checks include fix instructions so users can resolve issues.

```
  yoloai doctor
       │
       ▼
  ┌───────────────────────────────┐
  │  caps.DetectEnvironment()     │
  │                               │
  │  isRoot?  isWSL2?             │
  │  inContainer?  kvmGroup?      │
  └───────────────┬───────────────┘
                  │
                  ▼
  ┌───────────────────────────────────────────────────┐
  │  For each registered backend:                     │
  │                                                   │
  │    runtime.New(ctx, name)                         │
  │         │                                         │
  │         ├── RequiredCapabilities(baseMode)         │
  │         │     → [HostCapability, ...]             │
  │         │                                         │
  │         └── For each SupportedIsolationModes():   │
  │               RequiredCapabilities(mode)           │
  │                    → [HostCapability, ...]         │
  └───────────────────────┬───────────────────────────┘
                          │
                          ▼
  ┌───────────────────────────────────────────────────┐
  │  caps.RunChecks(ctx, capabilities, environment)   │
  │                                                   │
  │  For each HostCapability:                         │
  │    run Check(ctx) function                        │
  │    if failed:                                     │
  │      Permanent(env)?  → Unavailable               │
  │      else             → NeedsSetup + Fix steps    │
  └───────────────────────┬───────────────────────────┘
                          │
                          ▼
  ┌───────────────────────────────────────────────────┐
  │  caps.FormatDoctor(reports, output)               │
  │                                                   │
  │  Backend     Mode                Status            │
  │  ──────────  ──────────────────  ────────          │
  │  docker      container           Ready             │
  │  docker      container-enhanced  NeedsSetup        │
  │  tart        vm                  Ready             │
  │  containerd  vm                  Unavailable        │
  │                                                   │
  │  ▸ gvisor-runsc: install runsc (instructions...)  │
  └───────────────────────────────────────────────────┘
```

# Data Flow

## Sandbox Creation (`yoloai new`)

```
NewNewCmd (cli/lifecycle/new.go)
  → cliutil.WithClient (cli/cliutil/client.go)
    → yoloai.Client.CreateSandbox  (calls create.Run(ctx, deps, opts) in orchestrator/create/create.go)
      → EnsureSetup: create dirs, seed resources, build image, write config.yaml
      → prepareSandboxState (orchestrator/create/create.go):
          resolve profile chain → applyConfigDefaults
          → resolveAndApplyArchetype: load .yoloai.yaml → CLI > yaml > auto-detect priority
              → devcontainer: load devcontainer.json → merge ports/env/mounts, set workspaceFolder
              → compose: set isolation=container-privileged, archetypeDockerDRequired=true
              → transparency output (signals, bullets, suppress hint)
          → validate name/agent/workdir/auxdirs (DirSpecs already parsed upstream by cliutil.ParseDirArg) → safety checks
          → :copy dirs: CopyDir (clonefile on macOS; walk + io.Copy → copy_file_range reflink on Linux) → removeGitDirs → gitBaseline
          → :overlay dirs: createOverlayDirs (upper/ovlwork in sandbox state)
          → seed phase (provision/ leaf):
              copySeedFiles → copyAgentFiles → ensureContainerSettings → seedHomeConfig
          → readPrompt → resolveModel → buildAgentCommand
          → SaveMeta (environment.json) → SaveSandboxState (sandbox-state.json)
          → write prompt.txt, log.txt, runtime-config.json
          → WriteContextFiles (context.md + agent instruction file — orchestrator/create/context.go)
      → buildAndStart (orchestrator/launch/launch.go):
          createSecretsDir (config env vars + API keys from layout.Env host snapshot; staged under layout.SecretsStagingDir, "" = os.TempDir — D63)
          → buildMounts (workdir + aux dirs, overlay mount configs for :overlay dirs)
          → runtime.Create (with CAP_SYS_ADMIN for :overlay) → runtime.Start
          → runtime.Inspect (verify running) → cleanup secrets
```

## Runtime Backend Initialization

```
runtime.New(ctx, "docker")  (runtime/registry.go)
  → lookup factory in backends map (populated by init() registrations)
  → factory(ctx) → e.g. docker.New(ctx) → Docker SDK ping → DockerRuntime
```

Backends register themselves at import time via blank imports:
- `client.go`: imports docker, podman, seatbelt, tart
- `runtime_imports_linux.go`: imports containerd (Linux only)
- `internal/cli/runtime_imports_linux.go`: same for CLI binary

## Diff (`yoloai diff`)

```
newDiffCmd (cli/diff.go)
  → GenerateMultiDiff (copyflow/diff.go)
    → loadDiffContext: LoadMeta → resolve all directories from meta.Directories
    → For each directory:
      → :copy/:overlay mode: stageUntracked (git add -A) → git diff --binary <baseline>
      → :rw mode: git diff HEAD on live host dir
    → Combine diffs with directory-prefixed headers
```

## Apply (`yoloai apply`)

Two modes — squash and selective:

**Squash (default):**
```
applySquash (cli/apply.go)
  → For each :copy/:overlay directory in meta.Directories:
    → GeneratePatch (copyflow/apply.go): git diff --binary against baseline
    → CheckPatch: git apply --check
    → Confirm with user
    → ApplyPatch: git apply
    → AdvanceBaseline: update environment.json baseline SHA to HEAD
```

**Selective (commit refs):**
```
applySelectedCommits (cli/apply.go)
  → For each :copy/:overlay directory in meta.Directories:
    → ResolveRefs (copyflow/apply.go): resolve short SHAs / ranges
    → GenerateFormatPatchForRefs: git format-patch per commit
    → ApplyFormatPatch: git am --3way
    → AdvanceBaselineTo: advance baseline to contiguous prefix
```

## Overlay Mount Flow (`:overlay` directories)

Overlay mode uses Linux kernel overlayfs for instant setup with the diff/apply workflow:

```
create_prepare.go:
  → createOverlayDirs: create upper/ovlwork dirs in sandbox state

create_instance.go:
  → buildMounts: build overlay mount configs for runtime-config.json, add CAP_SYS_ADMIN

entrypoint.sh (Docker container, root phase):
  → mount overlayfs using runtime-config.json overlay_mounts
sandbox-setup.py (container, user phase):
  → git baseline (git init + commit) in mounted directories

diff.go / apply.go:
  → exec git commands inside container for overlay dirs (same as :copy)

lifecycle.go (reset):
  → clearOverlayDirs: rm -rf upper/ovlwork for instant reset
```

## Container Start/Restart (`yoloai start`)

```
lifecycle.Start(ctx, deps, name, opts) (orchestrator/lifecycle/lifecycle.go)
  → DetectStatus (orchestrator/status/status.go): runtime.Inspect + status file read
  → StatusActive: no-op
  → StatusDone/Failed: relaunchAgent via tmux respawn-pane
  → StatusStopped: runtime.Start
  → StatusRemoved: recreateContainer (rebuild state from environment.json via runtime.Create + runtime.Start)
```

## Capability Detection + Repair Advisory (`yoloai doctor`)

```
doctorcmd.NewCmd (cli/doctorcmd/doctor.go)
  → System.Doctor() — capability report:
    → caps.DetectEnvironment() — probe host (root, WSL2, container, KVM group)
    → For each registered backend:
      → runtime.New(ctx, name) — try to connect
      → rt.RequiredCapabilities(baseMode) — get base checks
      → For each rt.SupportedIsolationModes():
        → rt.RequiredCapabilities(mode) — get mode-specific checks
      → caps.RunChecks(capabilities, env) → []CheckResult
      → caps.ComputeAvailability(results) → Ready/NeedsSetup/Unavailable
    → caps.FormatDoctor(reports, output) — render table with fix instructions
  → System.Prune({DryRun:true}) + System.DiskUsage() — read-only advisory:
    → Reclaimable now    (RemovedItems)              → "yoloai system prune"
    → Reclaimable cached (CachedBytes, no rebuild)   → "yoloai system prune"
    → Reclaimable images (ImageBytes, forces build)  → "yoloai system prune --images"
    → Unreviewed work    (RefusedDataBearing)  → "yoloai diff / yoloai destroy"
    → Trash              (TrashContents)       → recover with mv / reclaim via prune
```

doctor is **pure read + delegate**: it never deletes or quarantines —
it only reports and prints the command that does the work. Exit code is 1
only when a backend NeedsSetup; advisory sections never affect it.

## Sandbox-dir recoverability classification (`System.Prune`)

Prune classifies every dir under `sandboxes/` by *recoverability*, not by
"brokenness". The bulk path only ever **removes** zero-stakes items; anything
that might hold user data is refused-and-reported or quarantined, never
silently deleted. The classifier (`classifySandboxes` in `system.go`)
crosses the `store.LoadMeta` failure kind with `sandbox.ProbeWorkData`:

```
meta loads cleanly                                   → known     (untouched; used for backend orphan matching)
data detected (ProbeWorkData = WorkDataPresent)      → refuse    (RefusedDataBearing — user runs diff/destroy)
missing meta + no work dir   (never-init)            → delete    (RemovedItems, PruneKindSandboxDir)
corrupt / version-too-new meta, no detectable data   → trash     (Trashed — quarantined to TrashDir, recover with mv)
```

`ProbeWorkData(sandboxDir)` (package `orchestrator`) detects work **host-side, no
container needed**: copy-mode dirs via `detectChanges` on `work/<enc>/.git`;
overlay-mode dirs via a non-empty `work/<enc>/upper/`. It returns
WorkDataNone / WorkDataPresent / WorkDataAmbiguous so corrupt-meta dirs with
ambiguous content default to trash (the safe choice), not deletion.

Quarantine is a plain `os.Rename` into `~/.yoloai/library/trash/<name>`
(`store.QuarantineSandbox`); there is no dedicated restore command — recover
with `mv`. The CLI confirms before emptying trash (it may hold wanted data);
`--yes` skips the prompt.


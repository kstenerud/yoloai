> **ABOUTME:** A router from each major yoloAI operation to the code that performs it — sandbox
> creation, backend init, diff, apply, start/restart, doctor, and prune classification. It names
> entry points and hops; it does not restate their rationale, which lives in the source (D124).

# Data Flow

This is a **map, not an explanation**. Each flow below names the real entry point and the hops
worth knowing, so you can find the code. The *why* of any step — why `create` does not launch,
why compose refuses to auto-escalate isolation, why `doctor` exits 1 — lives in the doc comment
on the function itself, which is the single source of truth. Follow the pointer; don't trust a
summary here over the code.

Why it works this way: this file previously restated those rationales, and every restatement had
drifted from the code by the time anyone read it (D124). Names below are gate-checked
(`TestRepoHygiene_ArchitectureDocRefs_Resolve`); prose is not, so there is deliberately little of
it.

## Sandbox creation (`yoloai new`)

**`create` provisions; it does not launch.** The CLI starts the sandbox as a separate step. See
the comment on `create.Run` — it is the authority on this split.

```
NewNewCmd (internal/cli/lifecycle/new.go)
  → newCreateClient → yoloai.Client.CreateSandbox (client.go)
      → Engine.EnsureSetup (internal/orchestrator/engine.go): scaffold dirs, defaults, base image
      → Engine.Create (internal/orchestrator/engine_lifecycle.go)
        → create.Run (internal/orchestrator/create/create.go)
          → prepareSandboxState:
              validateAndLoadConfig                    (name/agent)
              → resolveProfileAndArchetype → applyConfigDefaults → resolveAndApplyArchetype
                  (internal/orchestrator/create/prepare_archetype.go)
              → replaceSandboxIfNeeded                 (--replace teardown)
              → parseAndValidateDirs                   (internal/orchestrator/create/prepare_dirs.go)
              → createAndSeedSandbox                   ← SEED runs before workdir copy
                  → envsetup.SeedSandbox (internal/envsetup/envsetup.go)
              → setupAllWorkdirs                       ← copy + baseline runs after seed
              → buildConfigAndEnvironment
                  → invocation.ReadPrompt / ResolveModel / BuildAgentCommand
              → writeStatFiles
                  → store.SaveEnvironment / store.SaveSandboxState
                  → envsetup.WriteContextFiles (internal/envsetup/context.go)
          (returns — no container exists yet)

[separate call from the CLI, unless --no-start:]
Sandbox.Start → Engine.Start → lifecycle.Start (internal/orchestrator/lifecycle/start.go)
  → StatusRemoved branch → recreateContainer (internal/orchestrator/lifecycle/restart.go)
    → launch.LaunchContainer (internal/orchestrator/launch/launch.go)
        → envsetup.ResolveSecretEnv → brokerCredentials → envsetup.StageSecretEnv   (D63)
        → mounts.Build (internal/orchestrator/mounts/mounts.go)
        → buildAndStart → rt.Create → rt.Start → verifyInstanceRunning
        → deferred cleanup of the staged secrets dir
```

Two orderings that surprise people, both load-bearing: the **seed phase precedes the workdir
copy**, and **`buildAndStart` is never reached from `create.Run`**.

## Runtime backend initialization

```
runtime.New(ctx, name, layout) (runtime/registry.go)
  → backends map lookup (each backend's init() calls runtime.Register)
  → factory(ctx, layout) → e.g. docker.New (runtime/docker/docker.go) → Ping → *docker.Runtime
```

Backends register via blank import at package load:

| File | Registers |
| --- | --- |
| `client.go` (repo root) | apple, docker, podman, seatbelt, tart |
| `runtime_imports_linux.go` (repo root, `//go:build linux`) | containerd |

There is no CLI-specific imports file — the CLI links the root `yoloai` package and inherits its
registrations.

## Diff (`yoloai diff`)

The multi-directory loop lives in the **CLI**, not in `copyflow`; `copyflow` diffs one directory.

```
NewDiffCmd (internal/cli/workflow/diff.go)
  → runDiffCmd → store.LoadEnvironment → env.Dirs
    → diffSingle  (default)  → Workdir.Diff
    → diffAll     (--all)    → diffOneDirAll per tracked dir, prefixed output
      → engine.GenerateWorkingDiff (internal/orchestrator/engine_workdir.go)
        → copyflow.GenerateDiff (copyflow/diff.go)
            → loadDiffContext → resolve workDir / baseline / mode
            → :copy → git add -A → git diff --binary <baseline>
            → :rw   → git diff HEAD against the live host dir
```

## Apply (`yoloai apply`)

**The default is the commit series, not a squash.** Squash is opt-in via `--no-commit`. See
`dispatchApply` for the routing.

```
NewApplyCmd (internal/cli/workflow/apply.go) → dispatchApply
  ├ refs given    → applySelectedCommits (internal/cli/workflow/apply_selective.go)
  ├ --no-commit   → applyNoCommit        (internal/cli/workflow/apply_nocommit.go)
  └ default       → runApplyFormatPatch  (internal/cli/workflow/apply_format_patch.go)

  commit-series → Workdir.Apply(ApplyModeCommits) → engine.ApplySeries
    → copyflow.ApplySeries (copyflow/apply.go)
        → ResolveRefs | ListCommitsBeyondBaseline
        → GenerateFormatPatchForRefs | GenerateFormatPatch
        → git.ApplyFormatPatch (internal/git/ops.go)   — git am --3way
        → AdvanceBaselineTo | AdvanceBaseline

  no-commit → Workdir.Apply(ApplyModeNoCommit) → engine.ApplyAll
    → copyflow.ApplyAll (copyflow/apply.go)
        → GeneratePatch → git.CheckPatch → [confirm] → git.ApplyPatch → AdvanceBaseline
```

## Container start/restart (`yoloai start`)

```
lifecycle.Start (internal/orchestrator/lifecycle/start.go)
  → status.DetectStatus (internal/orchestrator/status/status.go): rt.Inspect + agent-status.json
  → StatusActive / StatusIdle → no-op
  → StatusDone / StatusFailed → handleTerminalStatus → relaunchAgent (tmux respawn-pane)
  → StatusSuspended           → handleSuspendedResume            (tart)
  → StatusStopped             → handleStoppedOrRemovedStatus  (removes the dead container first)
  → StatusRemoved             → handleStoppedOrRemovedStatus  (nothing to remove)
```

Stopped and Removed **converge** on `handleStoppedOrRemovedStatus` → `recreateContainer`; there
is no "just resume the stopped container" path. Whether the old container is removed first is a
host-filesystem-backend question — its doc comment explains why Seatbelt must skip the remove.

## Doctor (`yoloai doctor`)

`doctor` is **pure read + delegate**: it reports and prints the command that does the work.

```
doctorcmd.NewCmd (internal/cli/doctorcmd/doctor.go) → runDoctor
  → System.Doctor (system.go)
      → caps.DetectEnvironment (runtime/caps/detect.go)
      → per backend: runtime.New → per supported isolation mode:
          runtime.RequiredCapabilitiesFor (runtime/runtime_optional.go)
          → caps.RunChecks → caps.ComputeAvailability (runtime/caps/check.go)
  → System.Prune(DryRun) + System.DiskUsage + System.VMCensus + System.NetLiveness
  → formatDoctor (internal/cli/doctorcmd/doctor_format.go)
  → doctorExitError
```

The base isolation mode carries no capability check — it is reported Ready unconditionally.
`doctorExitError` owns the exit-code rule (three conditions, not one); its doc comment is the
authority.

## Sandbox-dir recoverability classification (`System.Prune`)

Prune classifies each dir under `sandboxes/` by *recoverability*, not brokenness. The bulk path
only removes zero-stakes items; anything that might hold user data is refused-and-reported or
quarantined, never silently deleted.

```
System.Prune (system.go) → classifySandboxes
  → store.LoadEnvironment
      ok  → known (untouched; used for backend orphan matching)
      err → orchestrator.ProbeWorkData (internal/orchestrator/status/status.go)
              work entry has .git      → workprobe.DetectChanges (git status --porcelain)
              no .git, non-empty upper/ → WorkDataPresent   [legacy :overlay only — see below]
  → applyBrokenClassifications:
      WorkDataPresent                 → refuse (RefusedDataBearing → user runs diff/destroy)
      meta absent, and WorkDataNone   → delete (RemovedItems, PruneKindSandboxDir)
      otherwise                       → trash  (Trashed → store.QuarantineSandbox)
```

Quarantine is a plain `os.Rename` into `<DataDir>/trash/<name>` (`store.QuarantineSandbox`).
There is no restore command — recover with `mv`. The CLI confirms before emptying trash;
`--yes` skips the prompt.

The `upper/` probe is **legacy detection only**. `:overlay` was retired (D109) and nothing
creates an `upper/` dir any more; the branch fires only for a pre-v4 sandbox that has not yet
been through `yoloai system migrate`. See `store.DirModeOverlay`, which documents its own
retirement.

> **ABOUTME:** macOS-only build brief for confine-host-side-git — the apple GitExecer wiring and
> seatbelt sandbox-exec git confinement, requiring real Apple Silicon to author and verify.

# Build brief (macOS agent): confine work-copy git on apple + seatbelt

**Status:** IMPLEMENTED — both tasks shipped and merged to main (D113): apple's `GitExecer`
(`GitExecInConfinement: true`, `runtime/apple/apple.go`) and seatbelt's sandbox-exec-wrapped git
with a dedicated tight SBPL profile (`GenerateGitProfile`, `runtime/seatbelt/profile.go`), both
verified by the malicious-filter containment test extended to apple and seatbelt (`57d5328b`
"Merge confine-git-macos: confine apple + seatbelt work-copy git"). Design is settled in
[confine-host-side-git.md](confine-host-side-git.md) — read it first for the threat model and
rationale. This brief was the actionable plan for the parts that require real macOS (Apple
Silicon, macOS 26+): they cannot be authored or verified on Linux because `sandbox-exec` and
Apple's `container` CLI don't exist there. The cross-platform pieces (Fix 3 probe hardening; the
`copy-mode-history.md` core) were handled separately on Linux.

## Why this is macOS-only

`make check`'s `crosscheck` (`GOOS=darwin GOARCH=arm64 go vet ./...`) will *typecheck* the
apple/seatbelt code on Linux, but nobody can *run* it there. The seatbelt work in particular
is empirical: you author an SBPL profile, then discover by running which `mach-lookup` /
`process-exec` denials break real git and legitimate filters. That loop only exists on macOS.

## Prerequisites (verify before starting)

- macOS 26+ on Apple Silicon.
- **seatbelt:** `sandbox-exec`, `tmux`, `jq`, `git`, and `git-lfs` on PATH (see
  `runtime/seatbelt/build.go:17` for the prereq check). Confirm `yoloai` can create/run a
  seatbelt sandbox.
- **apple:** Apple's `container` CLI installed and working (see `runtime/apple/apple.go`).
  Confirm `yoloai` can create/run an apple sandbox and that `container exec` works.
- Sanity: `make check` is green on this Mac before you change anything.

## Scope — two tasks

### Task A — apple: wire the GitExecer it already has (small)

Apple already exposes `Exec` via `container exec` (`runtime/apple/apple.go:291`); it simply
never implemented `GitExecer`, so `GitRunsInConfinement(apple)` is false and work-copy git
runs on the host with filters live (RCE).

1. Implement `runtime.GitExecer` on the apple `Runtime`, mirroring tart
   (`runtime/tart/tart.go:546-560`). The work copy is mounted into the VM at the same path, so:
   `GitExec(ctx, name, _hostPath, workDir string, args ...string)` →
   `Exec(ctx, name, append([]string{"git", "-C", workDir}, args...), <user>)`, returning
   stdout. Match tart's signature and stdout/err contract exactly (callers in `internal/git`
   depend on it).
2. Set `GitExecInConfinement: true` in apple's capabilities (`runtime/apple/apple.go:60-68`).
3. Add the compile-time assertion `var _ runtime.GitExecer = (*Runtime)(nil)` (as tart has at
   `tart.go:166`).
4. Confirm the git binary + any filter tools the test repos use are present in the apple base
   image (same requirement the container backends already carry).

**Done when:** `GitRunsInConfinement(apple) == true`, and the malicious-filter integration
test (below) passes on apple.

### Task B — seatbelt: sandbox-exec-wrapped git + a dedicated tight profile (the real work)

Seatbelt has no VM to exec into. "In-confinement git" means running **git itself under an SBPL
profile** so any filter it spawns inherits the confinement. Two parts, both required — the
wrapper alone is unsafe under today's profile (F5: it grants `(allow process-exec)` and
`(allow mach-lookup)` unrestricted — `runtime/seatbelt/profile.go`, `writeProfileHeader`).

**B1 — GitExecer via sandbox-exec.** Implement `GitExecer` for the seatbelt `Runtime`:
run `sandbox-exec -f <gitProfilePath> git -C <workDir> <args…>` on the host, capturing stdout
(same contract as tart/apple). Write the git profile to a per-op temp file (sandbox-exec `-f`
takes a file path; `-p` inline is possible but a file is cleaner for a multi-rule profile).
Set the capability so `GitRunsInConfinement(seatbelt)` becomes true, and add the
`var _ runtime.GitExecer` assertion.

**B2 — a dedicated, tight git profile (do NOT reuse the permissive agent profile).** Author a
minimal SBPL profile *for the git op only* — git needs far less than a full coding agent, so it
can be tightened hard without risking agent functionality. Target rules:

- `(deny default)`.
- `file-read* file-write*` scoped to the work copy **and its `.git`** only (the sandbox
  work-copy subpath). Read-only access to the system + toolchain paths git and filters need
  (reuse the path set from `writeProfileSystemPaths`, `runtime/seatbelt/profile.go:58`).
- **Deny `mach-lookup`** — the primary escape vector. Start with a blanket deny; if git or a
  filter genuinely needs a service, add the *minimal* allowlist and record which and why.
- Allow `process-exec`/`process-fork` **only within tool directories** (`/usr/bin`,
  `/opt/homebrew/bin` and the arm64 Homebrew prefix, the profile image's toolchain dirs) so
  legitimate filters (git-lfs, git-crypt) still run, but a payload cannot exec arbitrary
  binaries elsewhere or escape. This bounds a malicious filter to container-equivalent blast
  radius: run tools + read/write the work copy, no host escape.
- No network in this profile (netpolicy governs egress). Edge: LFS *smudge* needs network, but
  the diff/format-patch path exercises *clean* (local), not smudge — so the diff path shouldn't
  need it. If a real repo needs smudge-with-network during diff, record it and defer.

Starting skeleton (iterate empirically — treat as a draft, not gospel):

```scheme
(version 1)
(deny default)
(allow process-fork)
;; exec only within tool dirs — tighten/extend to your image's toolchain
(allow process-exec (subpath "/usr/bin") (subpath "/usr/libexec/git-core")
                    (subpath "/opt/homebrew/bin") (subpath "/opt/homebrew/opt"))
(deny mach-lookup)                     ;; add minimal allowlist ONLY if git/filters break
(allow file-read*  (subpath "/usr") (subpath "/System") (subpath "/private/var/db/timezone")
                    (literal "/dev/null") (literal "/dev/urandom"))
(allow file-read* file-write* (subpath "<WORKCOPY_SUBPATH>"))  ;; the mounted work copy + .git
(allow file-read* file-write* (subpath "<TMP_FOR_GIT>"))       ;; git temp, if needed
```

**Iteration methodology.** Run this battery under the profile and tighten until the malicious
case is contained while every legit case still passes:

- git ops: `status --porcelain`, `add -A`, `diff --binary <baseline>`, `format-patch --stdout -1 <sha>`.
- a **legit filtered repo**: a `filter.redact.clean` and a Git LFS repo — assert diff/apply
  round-trips byte-correctly (the Approach-A corruption in the RCE doc must NOT reproduce).
- the **malicious repo**: a `filter.pwn.clean` that tries to (a) write a marker *outside* the
  work copy, (b) exec a binary outside the tool dirs, (c) reach a mach service — all must be
  **denied** by the profile, and git must still complete the non-malicious part.

## Definition of done (both tasks)

1. **Extend the malicious-filter integration test** (`internal/orchestrator/integration_test.go:502`
   — it plants a `filter.x.clean` and asserts no host marker is created) to run on **apple** and
   **seatbelt**. Post-fix, the host marker must never appear on either.
2. **Seatbelt profile assertions** (new test): under the git profile, a filter payload cannot
   (a) exec outside the tool dirs, (b) write outside the work copy, (c) reach a denied mach
   service; and a legit git-lfs / clean-filter repo diffs/applies correctly.
3. `GitRunsInConfinement` is true for both apple and seatbelt; `git.NewSandbox` routes their
   work-copy git in-confinement (`internal/git/git.go:55-61`).
4. **`make check` green on the Mac** (gofmt, golangci-lint, go mod tidy, vet-tagged, crosscheck,
   all Go tests, python targets).

## Docs to update (part of "done" — see confine-host-side-git.md "Docs / decisions")

- **Fix the stale claim in `copy-mode-git-rce.md`:** it lists seatbelt as the sole residual and
  omits **apple** entirely, and doesn't note that host-side hardening leaves
  filters/textconv/fsmonitor live. Add apple; state the neutralization gap. (Coordinate — the
  Linux side may also touch this file; prefer whatever is already committed and reconcile.)
- **Decision entry** in `decisions/working-notes.md`: the invariant ("no git on an
  agent-writable repo outside confinement"), the apple/seatbelt fixes, and the
  dedicated-git-profile choice for seatbelt.
- **Backend-idiosyncrasy** entry in `backend-idiosyncrasies.md` (+ symptom-index row): the
  seatbelt `sandbox-exec` `mach-lookup`/`process-exec` escape surface and the dedicated-profile
  mitigation. This is exactly the kind of non-obvious backend behavior that dir is for.
- Update `confine-host-side-git.md`'s status and the capability/security matrix to reflect
  apple/seatbelt now confined.

## Coordination note

The Linux side is handling **Fix 3** (broken-metadata probe: strict `-c core.fsmonitor=false`
hardening for the host-side `git status` in `status.ProbeWorkData`/`workprobe.DetectChanges`)
and the cross-platform **copy-mode-history** core. Those touch different files
(`runtime/runtime.go` hardening args, `internal/orchestrator/status/`, `internal/workspace/`,
`internal/orchestrator/create/`) than this brief (`runtime/apple/`, `runtime/seatbelt/`), so a
merge should be clean. If you see them already in the tree, don't redo them.

## Key references

- Design + threat model: `confine-host-side-git.md`; companion `copy-mode-history.md`.
- Dispatch predicate: `runtime.GitRunsInConfinement` (`runtime/runtime.go:312`); caps
  (`GitExecInConfinement`) at `runtime/runtime.go:248-257`; `GitExecer` interface at
  `runtime/runtime_optional.go:101`.
- Model to mirror (tart): `GitExec` `runtime/tart/tart.go:546-560`; assertion `tart.go:166`.
- apple `Exec`: `runtime/apple/apple.go:291`; caps `apple.go:60-68`.
- seatbelt profile generation: `runtime/seatbelt/profile.go` (`writeProfileHeader:37`,
  `writeProfileSystemPaths:58`, `writeProfileSandboxDir:91`, `writeProfileMountRules:100`);
  prereqs `runtime/seatbelt/build.go:17`.
- host-side hardening today (hooks only): `runtime.GitHardeningArgs` (`runtime/runtime.go:325`);
  applied at `internal/git/git.go:96`.
- malicious-filter test to extend: `internal/orchestrator/integration_test.go:502`.
- standards: `docs/contributors/standards/go.md`, `shell.md`.

## Open questions to resolve empirically (record answers in the docs)

- Minimal `mach-lookup` allowlist git/filters need under sandbox-exec (or confirm blanket deny).
- Exact tool-dir allowlist for `process-exec` on this image (arm64 Homebrew prefix, git-core
  libexec, any filter binaries).
- Whether any diff-path filter needs network (LFS smudge) — if so, how it composes with
  netpolicy. → `questions-unresolved.md`.

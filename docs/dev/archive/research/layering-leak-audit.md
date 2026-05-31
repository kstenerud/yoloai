# Layering Leak Audit: Backend Abstraction Above the Runtime Boundary

**Date:** 2026-05-23  
**Scope:** `internal/cli/`, `yoloai.go` (public API), `sandbox/` (flagged where leaks are clear). Backend packages (`internal/runtime/docker/`, etc.) are out of scope.

---

## 1. Executive Summary

### Finding Counts

| Classification | HIGH | MEDIUM | LOW | Total |
|---|---|---|---|---|
| PRESENTATION | 3 | 2 | 3 | 8 |
| ORCHESTRATION | 2 | 3 | 1 | 6 |
| LEAK | 3 | 5 | 4 | 12 |
| **Total** | **8** | **10** | **8** | **26** |

### Structural Facts

- `yoloai.go`: **331 lines**
- `internal/cli/` (all `.go` files): **15,992 lines**
- `internal/cli/` does **NOT** import the `yoloai` package — `internal/cli` builds orchestration directly against `sandbox/` and `runtime/`, making `yoloai.Client` a parallel, non-authoritative entry point.
- `internal/cli/` **does** import `runtime/` directly: `helpers.go`, `apply_export.go`, `apply_selective.go`, `apply_squash.go`, `apply_overlay.go`, `diff.go`, `destroy.go`, `attach.go`, `exec.go`, `reset.go`, `sandbox_bugreport.go`, `sandbox_vscode.go`, `sandbox_clone.go`, `system_check.go`, `system_disk.go`, `system.go`, `new.go`, `runtime_imports_linux.go`.
- `internal/cli/` **does** import `sandbox/` directly: `apply.go`, `apply_overlay.go`, `apply_squash.go`, `apply_selective.go`, `apply_export.go`, `apply_format_patch.go`, `baseline.go`, `bugreport_writer.go`, `diff.go`, `new.go`, `destroy.go`, `list.go`, `sandbox_clone.go`, and more.
- `internal/cli/system_runtime.go` imports `runtime/tart` **by concrete type** (not just as a blank registration) — the only CLI file to break the backend abstraction at the import level.
- `sandbox/setup.go` imports `runtime/docker` for `EmbeddedTmuxConf()`. `sandbox/create.go` imports `runtime/tart` for the `--runtime` flag path, both with type assertions to concrete runtime types.

---

## 2. Findings

### `yoloai.go` (public API)

**L1**
- **Location:** `yoloai.go:60-61`
- **Reference:** `// Backend selects the runtime backend: "docker", "tart", or "seatbelt".`
- **Classification:** LEAK
- **Severity:** HIGH
- **Note:** The public API doc comment lists only 3 of 5 backends (omits `podman` and `containerd`). The comment will be wrong whenever backends are added or removed; it encodes knowledge of the full backend set in the wrong place.
- Decision: __

**L2**
- **Location:** `yoloai.go:36-39` (imports), `yoloai.go:83`, `yoloai.go:303`, `yoloai.go:328`
- **Reference:**
  ```go
  _ "github.com/kstenerud/yoloai/runtime/docker"   // register backend
  _ "github.com/kstenerud/yoloai/runtime/podman"   // register backend
  _ "github.com/kstenerud/yoloai/runtime/seatbelt" // register backend
  _ "github.com/kstenerud/yoloai/runtime/tart"     // register backend
  ```
  and `return "docker"` (two occurrences as fallback)
- **Classification:** ORCHESTRATION
- **Severity:** HIGH
- **Note:** The public API hard-codes `"docker"` as the default backend in two private helpers (`resolveBackendFromConfig` and `newRuntime`). These are duplicates of the same logic in `internal/cli/helpers.go`. Every new backend requires updating both sites. The blank imports are necessary for registration; those are fine. The default literal is the leak.
- Decision: __

---

### `internal/cli/helpers.go`

**L3**
- **Location:** `helpers.go:14-17` (imports), `helpers.go:27-28`, `helpers.go:77-79`, `helpers.go:85-86`, `helpers.go:116-131`, `helpers.go:134-139`
- **Reference:**
  ```go
  _ "github.com/kstenerud/yoloai/runtime/docker"
  podmanrt "github.com/kstenerud/yoloai/runtime/podman" // SocketExists helper
  _ "github.com/kstenerud/yoloai/runtime/seatbelt"
  _ "github.com/kstenerud/yoloai/runtime/tart"
  ```
  ```go
  if targetOS == "mac" { if isolation == "vm" { return "tart" }; return "seatbelt" }
  if isolation == "vm" || isolation == "vm-enhanced" { return "containerd" }
  ```
- **Classification:** ORCHESTRATION
- **Severity:** HIGH
- **Note:** `resolveBackend` and `detectContainerBackend` are the main OS/isolation → backend routing chokepoint for the CLI. This is where the mapping lives. The `podmanrt.SocketExists()` call is the reason `podman` is imported as a named import (not just blank) — CLI code directly calls into a concrete backend package for socket detection logic that arguably belongs in the runtime registry or a capability-query interface.
- Decision: __

**L4**
- **Location:** `helpers.go:122-131`
- **Reference:**
  ```go
  if dockerAvailable() {
      return "docker", warning
  }
  if podmanrt.SocketExists() {
      return "podman", warning
  }
  return "docker", warning // will fail hard in newRuntime()
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** `dockerAvailable()` at line 134 hard-codes `/var/run/docker.sock` as the Docker socket path. This knowledge should live in the docker backend, not the CLI. It also checks `DOCKER_HOST` env var — duplicating detection logic that the Docker SDK already performs internally.
- Decision: __

---

### `internal/cli/new.go`

**L5**
- **Location:** `new.go:53`
- **Reference:**
  ```go
  cmd.Flags().String("isolation", "", "Isolation mode: container (default), container-enhanced (gVisor), container-privileged (--privileged, use for Docker-in-Docker), vm (Kata+QEMU), vm-enhanced (Kata+Firecracker)")
  ```
- **Classification:** PRESENTATION
- **Severity:** HIGH
- **Note:** The `--isolation` flag help string maps every isolation mode to its underlying implementation technology (gVisor, Kata+QEMU, Kata+Firecracker, Docker-in-Docker). This is user-facing and intentional for discoverability, but it hard-codes the mode→technology mapping in a flag string. If the VM backend switches from QEMU to another hypervisor, this string must be updated manually.
- Decision: __

**L6**
- **Location:** `new.go:305-340` (`validateIsolationOSCombo`)
- **Reference:**
  ```go
  "  container   macOS sandbox-exec (seatbelt)\n"+
  "  vm          Full macOS VM (Tart)"
  ```
  (repeated 3 times in error messages for different invalid combos)
- **Classification:** LEAK
- **Severity:** HIGH
- **Note:** Error messages in `validateIsolationOSCombo` enumerate the backend names Seatbelt and Tart by their product names with implementation details. The same table is repeated three times across 30 lines. If a new macOS backend were added, all three messages would need updating. The comment in the seeded findings refers to `new.go:309-338`; this is the same block.
- Decision: __

**L7**
- **Location:** `new.go:319-326`
- **Reference:**
  ```go
  return sandbox.NewUsageError(
      "--isolation container-enhanced (gVisor) is not supported on macOS due to a bug\n" +
          "that causes Claude Code to hang indefinitely during initialization.\n\n" +
          "For details, see: https://github.com/anthropics/claude-code/issues/35454")
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** This error explicitly names gVisor and references a platform-specific bug workaround in the error message. A general `IsolationAvailableOn(isolation, hostOS)` capability check returning a structured reason would be more maintainable. The bug link is useful but could be in the capability metadata, not embedded in CLI validation code.
- Decision: __

---

### `internal/cli/info.go`

**L8**
- **Location:** `info.go:36-115` (`knownBackends` slice)
- **Reference:**
  ```go
  var knownBackends = []backendInfo{
      {Name: "docker", Description: "Linux containers (Docker)", Detail: ...},
      {Name: "podman", Description: "Linux containers (Podman)", Detail: ...},
      {Name: "tart", Description: "macOS VMs (Apple Virtualization)", Detail: ...},
      {Name: "seatbelt", Description: "macOS process sandbox (sandbox-exec)", Detail: ...},
      {Name: "containerd", Description: "Linux VMs via Kata Containers ...", Detail: ...},
  }
  ```
- **Classification:** PRESENTATION
- **Severity:** HIGH
- **Note:** `knownBackends` is a hard-coded table of every backend with descriptions, platform requirements, install hints, and tradeoffs. It is the main user-facing backend registry. If a backend is added or removed, this table must be updated independently of the runtime registry. It is also duplicated in concept by `sandbox/setup.go:availableBackends()` (L15 below). A runtime `Descriptor()` method already exists — backends could self-describe, and `info.go` could iterate the registered backends.
- Decision: __

---

### `internal/cli/diff.go`

**L9**
- **Location:** `diff.go:172,175`
- **Reference:**
  ```go
  return fmt.Errorf("overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** The term "overlay sandbox" in the error is Docker-overlayfs-specific jargon leaking into the CLI error layer. A user on a non-Docker backend who somehow encounters this message would be confused. The seeded finding is confirmed here.
- Decision: __

**L10**
- **Location:** `diff.go:97-98`
- **Reference:**
  ```go
  return sandbox.NewPlatformError("ref-based diff is not supported for :overlay sandboxes (commits are not individually addressable from the host)")
  ```
- **Classification:** PRESENTATION
- **Severity:** LOW
- **Note:** Uses ":overlay" as a user-facing mode name, which is correct user vocabulary, not an implementation leak. Included for completeness. This is the documented mode name.
- Decision: __

---

### `internal/cli/attach.go`

**L11**
- **Location:** `attach.go:135,139,196`
- **Reference:**
  ```go
  // This is the primary check and works even when docker exec is
  // unreliable (e.g. gVisor on ARM64).
  // 2. docker exec: run "tmux has-session -t main" inside the container.
  ...
  // Fallback: docker exec tmux has-session.
  ```
- **Classification:** LEAK
- **Severity:** LOW
- **Note:** Comments in `waitForTmux`/`pollTmuxReady` name Docker exec and gVisor as the concrete mechanisms motivating the two-path design. The production code itself is generic (`rt.Exec()`); this is a comment-level leak. Low impact but useful signal about design rationale that should ideally be in `docs/dev/backend-idiosyncrasies.md`, not inline.
- Decision: __

---

### `internal/cli/list.go`

**L12**
- **Location:** `list.go:195-197`
- **Reference:**
  ```go
  backend := info.Meta.Backend
  if backend == "" {
      backend = "docker" // fallback for old sandboxes without backend field
  }
  ```
- **Classification:** ORCHESTRATION
- **Severity:** LOW
- **Note:** Hard-coded `"docker"` as default for sandboxes that predate the `backend` field in `meta.json`. This is a migration shim, not a routing decision. The same pattern appears in `sandbox/inspect.go:533`.
- Decision: __

---

### `internal/cli/profile.go`

**L13**
- **Location:** `profile.go:62-64` (scaffold comment)
- **Reference:**
  ```go
  scaffold := `# backend: docker   # optional backend constraint
  # tart:
  #   image: my-vm    # Tart backend only`
  ```
- **Classification:** PRESENTATION
- **Severity:** LOW
- **Note:** The scaffolded `config.yaml` comment example names `docker` and `tart` as example values. These are legitimate user-facing examples. The `TartImage` field in `profileInfoJSON` (line 271) similarly leaks Tart as the only backend with a named extra field in the JSON output struct.
- Decision: __

**L14**
- **Location:** `profile.go:697`
- **Reference:**
  ```go
  fmt.Fprintf(cmd.OutOrStdout(), "Note: if a Docker image 'yoloai-%s' exists, remove it with: docker rmi yoloai-%s\n", name, name)
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** After deleting a profile, the CLI always prints a Docker-specific cleanup hint regardless of the user's configured backend. A Podman user would have `podman rmi yoloai-<name>` as the correct command, and a Tart/Seatbelt user wouldn't need it at all. The hint should either query the active backend or be removed.
- Decision: __

---

### `internal/cli/help.go`

**L15**
- **Location:** `help.go:174`
- **Reference:**
  ```go
  b.WriteString("       http://host.docker.internal:11434\n")
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** The help text for local models hard-codes `host.docker.internal` as the Ollama URL example. This hostname is Docker/Podman-specific and does not work with Tart or Seatbelt backends. A backend-agnostic phrase like "the host's IP address" or a note that this varies by backend would be more accurate.
- Decision: __

---

### `internal/cli/bugreport_writer.go`

**L16**
- **Location:** `bugreport_writer.go:120-129`
- **Reference:**
  ```go
  backends := []backendEntry{
      {"docker", "docker", []string{"version", "--format", "..."}},
      {"podman", "podman", []string{"version", "--format", "..."}},
      {"tart", "tart", []string{"--version"}},
      {"seatbelt", "", nil}, // built-in
  }
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** The bug report writer hard-codes which binaries to query for versions and how to query them, per backend. This knowledge (binary name, version flags) is essentially duplicated from each backend's own `Descriptor()`. If a backend changed its version-query CLI or a new backend were added, this list would silently become stale. `containerd` is also missing from this list. Confirmed as seeded finding.
- Decision: __

---

### `internal/cli/config.go`

**L17**
- **Location:** `config.go:101`, `config.go:178`
- **Reference:**
  ```go
  // Uses dotted paths for nested keys (e.g., tart.image).
  // ...
  // or an entire section (tart).
  ```
- **Classification:** PRESENTATION
- **Severity:** LOW
- **Note:** Help text for `config set` and `config reset` uses `tart.image` and `tart` as example key paths. These are legitimate examples of real config keys. Low concern — purely informational.
- Decision: __

---

### `internal/cli/system_check.go`

**L18**
- **Location:** `system_check.go:128-134`
- **Reference:**
  ```go
  return fmt.Errorf("yoloai-base image not found — run 'yoloai system build --backend %s'", backend)
  ```
- **Classification:** PRESENTATION
- **Severity:** LOW
- **Note:** Error message includes the backend name in a `yoloai system build` hint. This is correct and useful — the user needs to know which backend to build for. Included for completeness.
- Decision: __

---

### `internal/cli/system_runtime.go`

**L19**
- **Location:** `system_runtime.go:13`, `system_runtime.go:124-134`, `system_runtime.go:129`
- **Reference:**
  ```go
  import "github.com/kstenerud/yoloai/runtime/tart"
  
  func openTartRuntime(ctx context.Context) (*tart.Runtime, func(), error) {
      rt, err := newRuntime(ctx, "tart")
      tartRuntime, ok := rt.(*tart.Runtime)  // type assertion to concrete type
  ```
- **Classification:** LEAK
- **Severity:** HIGH
- **Note:** `system_runtime.go` is the only CLI file that imports a concrete backend package (`runtime/tart`) as a named import and type-asserts the `runtime.Runtime` interface to `*tart.Runtime`. This file exists solely to expose Tart's Apple simulator runtime management (iOS/tvOS/watchOS/visionOS base VMs). The commands (`yoloai system runtime create/list/delete`) are intrinsically Tart-only features with no analog in other backends. The question is whether this makes it a justified special case or a design gap in the runtime interface.
- Decision: __

**L20**
- **Location:** `system_runtime.go:64,162,273`
- **Reference:**
  ```go
  available, note := checkBackend(ctx, "tart")
  if !available {
      return sandbox.NewUsageError("Tart backend not available: %s\n\nInstall Tart: brew install ...", note)
  }
  ```
- **Classification:** PRESENTATION
- **Severity:** MEDIUM
- **Note:** Three `system runtime` subcommands each guard themselves with an explicit Tart availability check and print an install hint. Reasonable for a Tart-only feature surface, but the repetition (3×) is a maintenance burden if the check or message changes.
- Decision: __

**L21**
- **Location:** `system_runtime.go:390-395`
- **Reference:**
  ```go
  func runTartCommand(ctx context.Context, args ...string) (string, error) {
      cmd := exec.CommandContext(ctx, "tart", args...)
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** The CLI directly shells out to the `tart` binary for certain operations (listing base VMs) rather than calling through the runtime interface. This bypasses the abstraction even for the backend that has a dedicated runtime package.
- Decision: __

---

### `internal/cli/help/` (embedded help files)

**L22**
- **Location:** `help/flags.md:13`, `help/flags.md:27-29`
- **Reference:**
  ```
  --backend <name>    Runtime backend (docker, tart, seatbelt)
  --security <mode>   OCI runtime security mode: standard, gvisor,
                      kata, kata-firecracker (docker/podman only;
  ```
- **Classification:** LEAK
- **Severity:** HIGH
- **Note:** `flags.md` documents a `--security` flag that **does not exist** in the current codebase (it was superseded by `--isolation`). This is stale help text with the old API surface. A user reading this help file would try `--security gvisor` and get an "unknown flag" error. Also the `--backend` enumeration omits `podman` and `containerd`.
- Decision: __

**L23**
- **Location:** `help/security.md:64-78`, `help/security.md:71,75,78`
- **Reference:**
  ```
  gvisor            Userspace kernel (gVisor/runsc) — syscall interception,
  kata              Kata Containers with QEMU VM isolation (experimental).
  kata-firecracker  Kata Containers with Firecracker microVM (experimental).
     yoloai config set security gvisor
     yoloai new task . --security gvisor
  ```
- **Classification:** LEAK
- **Severity:** HIGH
- **Note:** `security.md` is entirely built around the obsolete `--security` flag and old mode names (`gvisor`, `kata`, `kata-firecracker`). The current API uses `--isolation` with `container-enhanced`, `vm`, `vm-enhanced`. This help topic is wholly misaligned with current behavior. Users following these instructions would get errors or silently wrong behavior.
- Decision: __

**L24**
- **Location:** `help/config.md:21-23`
- **Reference:**
  ```
  backend          Runtime backend: docker, tart, seatbelt
  security         OCI runtime security mode (docker/podman only):
                   standard, gvisor, kata, kata-firecracker (experimental)
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** `config.md` documents a `security` config key that no longer exists in the config schema (replaced by `isolation`), and the `backend` list omits `podman` and `containerd`. Stale help documentation.
- Decision: __

**L25**
- **Location:** `help/workdirs.md:14,27`
- **Reference:**
  ```
  yoloai new task ./my-project:overlay   # overlay mount (Docker only)
  - Docker backend only (not available with seatbelt or tart).
  ```
- **Classification:** PRESENTATION
- **Severity:** LOW
- **Note:** The `:overlay` mode is legitimately Docker/Podman/containerd-only (requires Linux kernel overlayfs inside the container). Naming Docker specifically is accurate but slightly narrow — Podman and containerd also support `:overlay`. The restriction should say "container backends only" or "requires Linux kernel overlayfs inside the container."
- Decision: __

---

### `sandbox/` package (flagged leaks only)

**L26**
- **Location:** `sandbox/setup.go:104-113` (`availableBackends`)
- **Reference:**
  ```go
  {"docker", "Linux containers; portable, lightweight, fast"},
  {"podman", "Linux containers; daemonless, rootless by default"},
  {"seatbelt", "macOS sandbox; near-instant, uses host tools, less isolation"},
  {"tart", "macOS VMs; native macOS env, strong isolation, heavier"},
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** `sandbox/setup.go` contains its own hard-coded backend list (without `containerd`) for the interactive setup wizard. This is a second copy of the backend registry, separate from `info.go:knownBackends`. If `containerd` should appear in setup, it won't. The runtime registry already knows which backends are available via `runtime.IsAvailable()`.
- Decision: __

**L27**
- **Location:** `sandbox/setup.go:307,423` and `sandbox/setup.go:21`
- **Reference:**
  ```go
  import dockerrt "github.com/kstenerud/yoloai/runtime/docker"
  fmt.Fprint(m.output, string(dockerrt.EmbeddedTmuxConf()))
  fileutil.WriteFile(destPath, dockerrt.EmbeddedTmuxConf(), 0644)
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** `sandbox/setup.go` imports the docker runtime package to access the embedded `tmux.conf`. The tmux configuration is not Docker-specific — it is used by all backends that run tmux inside the sandbox. `EmbeddedTmuxConf` belongs in a neutral package (e.g., `runtime/resources` or `sandbox/resources`), not in `runtime/docker`.
- Decision: __

**L28**
- **Location:** `sandbox/create.go:26,541-575` and `sandbox/create.go:574-576`
- **Reference:**
  ```go
  import "github.com/kstenerud/yoloai/runtime/tart"
  tartRuntime, ok := m.runtime.(*tart.Runtime)
  if !ok {
      return NewUsageError("--runtime flag only supported on tart backend (macOS VMs)")
  }
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** `sandbox/create.go` type-asserts to `*tart.Runtime` to implement the `--runtime` (Apple simulator) feature. The code comment acknowledges this was done as "backend dispatch by type assertion, not string comparison" (W10 of remediation plan), treating it as a known-acceptable pattern. However, importing a concrete backend package into `sandbox/` crosses the architectural boundary that keeps `sandbox/` backend-agnostic.
- Decision: __

**L29**
- **Location:** `sandbox/create_instance.go:126-131`
- **Reference:**
  ```go
  // container-enhanced (gVisor) does not support overlayfs inside the container.
  // Catch this combination early before Docker fails with an opaque error.
  if state.isolation == "container-enhanced" && hasOverlayDirs(state) {
      return fmt.Errorf(
          ":overlay directories require --isolation container; " +
              "--isolation container-enhanced uses gVisor, ...")
  ```
- **Classification:** LEAK
- **Severity:** MEDIUM
- **Note:** `sandbox/create_instance.go` checks the isolation mode string `"container-enhanced"` against a hard-coded value and names gVisor in the error. This belongs in the runtime layer — e.g., `runtime.SupportsOverlayDirs(isolation)` returning false for `container-enhanced`. The comment `Catch this combination early before Docker fails with an opaque error` reveals Docker-specific knowledge motivating the check.
- Decision: __

**L30**
- **Location:** `sandbox/create_prepare.go:347`
- **Reference:**
  ```go
  hint = "use host.docker.internal instead"
  ```
- **Classification:** LEAK
- **Severity:** LOW
- **Note:** When an agent config contains a localhost address and the backend supports network isolation, the error hint says `host.docker.internal`. This is Docker-specific; the correct hostname varies by backend (Podman also uses this hostname, but Tart and Seatbelt do not). Should use `desc.Name` in the hint or route through a backend capability.
- Decision: __

**L31**
- **Location:** `sandbox/create_prepare.go:860,878,900-901,965-974`
- **Reference:**
  ```go
  bullets = append(bullets, "backend=tart required (Apple Silicon macOS VM)")
  bullets = append(bullets, "dockerd will auto-start before lifecycle commands")
  "docker Compose devcontainers are not supported; ..."
  bullets = append(bullets, "isolation set to container-privileged (postStartCommand uses docker compose)")
  ```
- **Classification:** LEAK
- **Severity:** LOW
- **Note:** Archetype application in `sandbox/` emits user-facing bullets that name specific backends (tart) and Docker concepts (dockerd, docker compose). These are correct in context (Compose devcontainers genuinely require Docker-in-Docker) but the sandbox layer should not know these backend names — the archetype engine could call into a capability query rather than hard-coding.
- Decision: __

---

## 3. Top 10 Most Concerning

Ranked by structural impact and user-visible harm:

1. **L22 + L23 (help/flags.md + help/security.md — stale `--security` flag):** Users following these help pages will get unknown-flag errors. The entire `security.md` topic is misaligned with the current `--isolation` API. This is a correctness bug in user-facing documentation that already shipped.

2. **L19 (system_runtime.go — `*tart.Runtime` type assertion in CLI):** Only file in `internal/cli/` that imports a concrete backend package as a named import and casts through the interface. Tart-specific simulator management (`yoloai system runtime`) has no abstraction whatsoever. Structural ceiling: any new Tart capabilities require CLI changes, not just backend changes.

3. **L8 (info.go — `knownBackends` hard-coded table):** The user-facing backend registry in the CLI is a parallel structure to the runtime registry. Adding/removing a backend requires updating `knownBackends` independently. Already diverged from the runtime registry (`containerd` is described here but omitted from `sandbox/setup.go:availableBackends` — L26). Creates a three-way sync problem (runtime registry + `info.go` + `setup.go`).

4. **L3 (helpers.go — routing table + `podmanrt.SocketExists()` import):** The CLI's backend selection logic requires importing a concrete backend package (`podman`) to call `SocketExists()`. This means the routing function can only be generalized by adding a capability to the runtime interface. It is also the only "real" backend routing code; duplication in `yoloai.go` (L2) means there are two divergent routing functions.

5. **L6 (new.go — `validateIsolationOSCombo` error tables repeated 3×):** Validation and error messages for isolation mode combinations enumerate macOS-specific backends (Seatbelt, Tart) and Linux-specific isolation modes (gVisor, Kata+QEMU, Kata+Firecracker) in 30 lines of if/else. Repeated 3 times. This is the most backend-knowledge-dense block in `internal/cli/`.

6. **L1 (yoloai.go — public API doc comment incomplete backend list):** Public API comment says `"docker", "tart", or "seatbelt"` — wrong, omits `podman` and `containerd`. The public API is the interface most likely to be consumed by external embedders, and this misleads them about valid values.

7. **L27 (sandbox/setup.go imports `runtime/docker` for tmux config):** `EmbeddedTmuxConf` is not Docker-specific, but it lives in `runtime/docker`. The sandbox layer imports a concrete backend package for a resource it needs. This is the kind of cross-layer dependency that tends to accrete.

8. **L16 (bugreport_writer.go — per-backend version query table):** The bug report writer hard-codes binary names and version-query flags per backend. `containerd` is missing from the list. If a backend is added, bug reports silently omit it. Backend `Descriptor()` already exists but isn't used here.

9. **L14 (profile.go — Docker-specific cleanup hint on profile delete):** Always prints `docker rmi yoloai-<name>` regardless of user's backend. Wrong for Podman users, irrelevant for Tart/Seatbelt users.

10. **L15 (help.go — `host.docker.internal` in Ollama example):** The built-in help for local models cites a Docker-specific hostname. Tart and Seatbelt users get wrong guidance. Low to fix, but it's in the primary interactive help shown during setup.

---

## 4. CLI Orchestration Overlap

These files contain non-trivial orchestration logic that is duplicated or absent from `yoloai.Client`. `internal/cli/` does not import `yoloai` — the public API and the CLI are sibling implementations, not a hierarchy.

| File | Lines | Presentation / Orchestration ratio | Note |
|---|---|---|---|
| `new.go` | 341 | ~40/60 | Most flag parsing; orchestration is `executeNewCreate` + `validateIsolationOSCombo`. Both should be in `yoloai.Client.Run`. |
| `apply.go` + `apply_*.go` | ~1,025 total | ~20/80 | Almost pure orchestration. `yoloai.Apply` exists but doesn't cover overlay or format-patch paths. |
| `diff.go` | 617 | ~30/70 | Heavy orchestration. `diffOverlay`/`diffSingle`/`hasOverlayDirs` are policy logic absent from `yoloai.Client`. |
| `attach.go` | 277 | ~35/65 | `waitForTmux`/`pollTmuxReady` are non-trivial orchestration with backend-fallback logic; not in `yoloai.Client`. |
| `destroy.go` | 246 | ~30/70 | Orchestration present in `yoloai.Client.Destroy`, but CLI adds `--force` / unapplied-changes check that's duplicated. |
| `info.go` | 336 | ~15/85 | Almost entirely a data table (`knownBackends`) plus rendering. Nearly zero reuse from `yoloai.Client`. |
| `helpers.go` | 243 | ~10/90 | Pure orchestration: backend routing, runtime wiring. Entirely absent from `yoloai.Client` (which has its own private copy). |
| `system_runtime.go` | 475 | ~10/90 | Tart-only feature management. No equivalent in `yoloai.Client`. Would require new Client methods or a Tart-specific extension. |
| `list.go` | 223 | ~40/60 | Rendering is half; the grouping/status logic is orchestration absent from `yoloai.Client`. |
| `bugreport_writer.go` | 375 | ~5/95 | Nearly all orchestration/data gathering; no equivalent in `yoloai.Client`. |

**Key observation:** `internal/cli/` does not call `yoloai.Client` at all. The CLI and the public API are competing implementations of the same product. Any feature added to the CLI must be separately added to `yoloai.Client` to be accessible to embedders. The `yoloai.Client.Apply` method exists but does not cover the overlay and format-patch apply paths that CLI users get. This gap grows with every new CLI feature.

---

## 5. Open Questions

**Q1:** Is the `yoloai system runtime` surface (`system_runtime.go`) intended to be part of the permanent API, or is it a temporary CLI convenience pending a proper runtime-capability interface? The type assertion to `*tart.Runtime` works but sets a precedent. If other backends develop unique management surfaces (e.g., a Kata-specific VM inspector), will they each get their own `system_kata.go`?

**Q2:** Should `podmanrt.SocketExists()` be moved into the `runtime.Registry` or a `Probes()` interface so that `detectContainerBackend` (L3/L4) doesn't need to name-import the podman package? Or is the current pattern acceptable given that Docker/Podman are the only two interchangeable container backends?

**Q3:** `sandbox/create_instance.go:applyOverlayAndCaps` checks `isolation == "container-enhanced"` directly (L29). `runtime/isolation.go` already has `IsolationContainerRuntime()` and `IsolationEnforcesInSandboxIptables()`. Should there be an `IsolationSupportsOverlayDirs(isolation string) bool` function there, so this check doesn't name `"container-enhanced"` directly in sandbox code?

**Q4:** `security.md` and `flags.md` describe a `--security` flag with mode names (`gvisor`, `kata`, `kata-firecracker`) that no longer exist. Is this intentional dead content (to keep old links alive), or should it be updated to the current `--isolation` API? If `--security` was a released, public flag, it may need a deprecation note rather than silent removal.

**Q5:** `info.go:knownBackends` and `sandbox/setup.go:availableBackends` are two independent backend registries. The runtime system already has `runtime.IsAvailable(name)` and `runtime.Descriptor()`. Is the intent to consolidate these into a single source of truth driven by the runtime registry, or to keep the CLI-side tables as the authoritative presentation layer?

**Q6:** `help/workdirs.md:27` says `:overlay` is "Docker backend only" but it actually works with Podman and containerd too. Should this be corrected to "container backends only (requires Linux kernel overlayfs inside the container)"?

---

## 6. Recommendations

Recommendations are framed against the proposed architecture in [`docs/design/layering.md`](../../design/layering.md): **CLI as a thin shell over `yoloai.Client` (Pattern C)**, **backend-specific operations in explicitly-scoped subcommand groups (Pattern B, "podman machine" model)**, **capability flags + optional interfaces at the runtime layer (Pattern A)**.

Each row carries a verdict — KEEP (acceptable as-is), MODIFY (change shape, retain intent), HIDE (remove from user surface), or FIXED (already addressed in this pass). The user (decision-maker) can override per row.

| # | Verdict | Recommendation |
|---|---|---|
| L1 | MODIFY | Remove the enumerated backend list from the `yoloai.go` doc comment. Point to `yoloai system backends` or to `runtime.Registered()` as the authoritative source. The comment drifts every time a backend is added. |
| L2 | MODIFY | Consolidate routing into `yoloai.Client` (Pattern C); have `internal/cli/helpers.go` call into it instead of duplicating. Delete the duplicated `"docker"` default literal. |
| L3 | MODIFY | Add an availability-probe capability to `BackendDescriptor` (e.g., `Probe(ctx) (Available bool, Reason string)`); `resolveBackend` iterates registered backends instead of name-importing `podman`. Closes Q2. |
| L4 | MODIFY | Same chokepoint as L3 — push socket-existence logic into the docker package's `Probe()` implementation. Remove `dockerAvailable()` from CLI. |
| L5 | KEEP | Per nerdctl's pattern (B), naming the impl tech in user help is honest documentation, not a leak — the alternative (hiding what powers each mode) is worse for power users. Mark the help line as "🔧 backend-specific" if line length grows. |
| L6 | MODIFY | Replace the 3× repeated error blocks with a single `availableIsolationModesFor(hostOS, targetOS)` helper sourced from the runtime descriptors. Same error content, single chokepoint. |
| L7 | MODIFY | Add `runtime.IsolationAvailability(mode, hostOS) → (ok bool, reason string, link string)`. Lets the CLI render reasons without hard-coding gVisor's name or upstream bug URLs. Bug link becomes capability metadata. |
| L8 | MODIFY | Replace `knownBackends` with iteration over `runtime.Registered()`. `BackendDescriptor` already carries Name + Description; populate it more fully (platforms, requires, notes) and remove the parallel CLI table. Same chokepoint as L26. |
| L9 | MODIFY | Use ":overlay sandbox" (the documented mount-mode vocabulary, per L10) — colon-prefixed is the user-facing name. |
| L10 | KEEP | Documented user-facing mode name. Correct as-is. |
| L11 | MODIFY | Move design rationale to `docs/dev/backend-idiosyncrasies.md` (the project already has this doc); leave a generic comment ("two-path readiness check — host filesystem + in-container probe"). |
| L12 | KEEP | Migration shim for sandboxes that predate the `backend` field. Name the constant (`defaultBackendForLegacyMeta = "docker"`) and centralize in one migration helper. The same shim in `inspect.go:533` should use the same constant. |
| L13 | KEEP | Legitimate user-facing example values in a scaffold comment. The `TartImage` named field in `profileInfoJSON` is the known "BackendSpecific config knobs" leak from the comparator synthesis — accept it as inherent, but consider a `BackendSpecific map[string]any` escape hatch over time rather than proliferating named fields. |
| L14 | MODIFY | Drive the cleanup hint from the active backend's descriptor: `backend.CleanupHint(image)` returns the correct command, or returns empty (Tart/Seatbelt). Removes the unconditional Docker hint. |
| L15 | MODIFY | Replace the hard-coded `host.docker.internal` example with either (a) a backend-aware lookup (`desc.HostFromContainerHostname()`) rendered into the help at runtime, or (b) generic phrasing ("the host machine's IP"). (a) is preferable in interactive help. |
| L16 | MODIFY | Add `VersionString(ctx) (string, error)` to `BackendDescriptor` (or as an optional interface). bugreport iterates the registry — a new backend automatically appears in bug reports. |
| L17 | KEEP | Legitimate config-key example text. |
| L18 | KEEP | Correct, helpful error — guides the user to the next command. The backend name in the hint is correct context. |
| L19 | KEEP + RENAME | Functionally keep — Tart's Apple-simulator surface is genuinely irreducible (Pattern B, the "podman machine" model). But **rename** the command tree from `yoloai system runtime` → `yoloai system tart` (or top-level `yoloai tart`). The current name reads as generic; renaming makes the backend scoping honest. Future per-backend surfaces follow the same convention (`yoloai system kata`, etc.). Closes Q1. |
| L20 | MODIFY | Extract a single `requireTartBackend(ctx)` helper. Largely subsumed once L19's rename moves these commands into a tart-scoped group with one `PersistentPreRunE`. |
| L21 | MODIFY | Move base-VM listing into `runtime/tart` as a typed function (`tart.ListBaseVMs(ctx)`). Once L19 scopes the commands explicitly to Tart, calling into `runtime/tart` is honest — no abstraction theatre. |
| L22 | FIXED | Updated in this pass: `flags.md` now documents `--isolation` + `--os`, and `--backend` lists all five backends. |
| L23 | FIXED | Updated in this pass: `security.md` rewritten around `--isolation` modes (`container`, `container-enhanced`, `container-privileged`, `vm`, `vm-enhanced`) with corrected setup and incompatibility sections. |
| L24 | FIXED | Updated in this pass: `config.md` now documents `isolation` and `os` config keys; backend list includes all five backends. |
| L25 | MODIFY | Update `help/workdirs.md` to say "container backends only (docker, podman, containerd)" instead of "Docker only". Closes Q6. Worth bundling with this pass — same fix character as L22–L24. |
| L26 | MODIFY | Replace `availableBackends()` with iteration over `runtime.Registered()`. Same chokepoint as L8. |
| L27 | MODIFY | Move `EmbeddedTmuxConf` out of `runtime/docker` into a neutral package (`internal/resources/tmux` or `sandbox/tmuxconf`). It's used by every backend that runs tmux. The current location is historical accident. |
| L28 | MODIFY | Add an `AppleSimulatorRuntimes` optional interface that `runtime/tart` implements; `sandbox/create.go` does `if irt, ok := rt.(AppleSimulatorRuntimes); ok { ... }` instead of naming the tart package. Mirrors yoloAI's existing pattern (`UsernsProvider`, `StdioExecer`, etc.). |
| L29 | MODIFY | Add `runtime.SupportsOverlayDirs(isolation string) bool` in `runtime/isolation.go`. Closes Q3. |
| L30 | MODIFY | Same fix shape as L15 — query backend descriptor for host-from-container hostname, or drop the hint. |
| L31 | KEEP (tracked) | LOW severity; acceptable for now. Track as a category: the archetype engine eventually needs a capability-driven "why backend X is required" helper rather than hard-coded backend names in bullets. Bottom of the priority list. |

### Verdict counts

- **KEEP:** 7 (L5, L10, L12, L13, L17, L18, L31)
- **KEEP + RENAME:** 1 (L19)
- **MODIFY:** 19 (L1, L2, L3, L4, L6, L7, L8, L9, L11, L14, L15, L16, L20, L21, L25, L26, L27, L28, L29, L30)
- **FIXED in this pass:** 3 (L22, L23, L24)
- **HIDE:** 0 (no findings recommended for removal from user surface)

---

## 7. Recommended Answers to Open Questions

| Q | Recommended answer |
|---|---|
| **Q1** | Permanent — Tart's simulator-runtime management is a real, irreducible feature. Adopt the Pattern B "podman machine" model: rename `yoloai system runtime` to `yoloai system tart`. Future per-backend management surfaces follow the same convention (`yoloai system kata`, etc.). See L19. |
| **Q2** | Yes — move `SocketExists` (and equivalent probes for other backends) into a `Probe()` capability on `BackendDescriptor`. The CLI iterates rather than name-importing. See L3. |
| **Q3** | Yes — add `IsolationSupportsOverlayDirs(isolation string) bool` in `runtime/isolation.go`. The string check in `sandbox/create_instance.go` goes away. See L29. |
| **Q4** | Update (done in this pass). If `--security` was in a released version, add an entry to `docs/BREAKING-CHANGES.md` documenting the rename to `--isolation` with the value mapping (`standard` → `container`, `gvisor` → `container-enhanced`, `kata` → `vm`, `kata-firecracker` → `vm-enhanced`). Recommend verifying release history before deciding whether the entry is necessary. |
| **Q5** | Yes — consolidate to a single `runtime.Registered()` iteration as the source of truth for `info.go` (L8), `setup.go` (L26), and `bugreport_writer.go` (L16). Three independent registries collapse to one. |
| **Q6** | Yes — update `workdirs.md` to "container backends only (docker, podman, containerd)". See L25. |

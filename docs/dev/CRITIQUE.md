# Critique

## Overall: A- — Well-engineered for beta; near enterprise-ready with a few gaps

---

## 1. Structure & Organization — Strong

The package layout (`cmd/`, `agent/`, `config/`, `runtime/`, `sandbox/`, `internal/cli/`) is clean and matches documented architecture. The `runtime.Runtime` interface is well-designed with no backend-specific types leaking out. The `Client` public API in `yoloai.go` is a nice touch for programmatic use.

---

## 2. Code Quality — Good, with one gap

- `gosec` annotations with rationales on file operations show security awareness
- `workspace/safety.go` (`IsDangerousDir`, `CheckPathOverlap`) is good hardening
- Typed errors in `config/errors.go` and `sandbox/errors.go` are well-designed with
  correct exit-code semantics

**Gap:** `internal/cli/` almost exclusively uses plain `fmt.Errorf` rather than the
typed error constructors, so nearly all CLI errors exit 1 regardless of their actual
category (usage error, config error, auth error, etc.). The typed error system exists
but is not used where it matters most. Several sentinel errors in `sandbox/errors.go`
(`ErrDockerUnavailable`, `ErrMissingAPIKey`, `ErrContainerNotRunning`, `ErrNoChanges`)
appear unused. Both tracked in the backend-agent-extensibility plan (Issues 8 and 10).

---

## 3. Testing — Good, with Gaps

- 84 test files across 199 Go source files (~40% by count)
- Three-tier strategy (unit / integration / e2e) is the right approach
- `internal/testutil/` helpers (`IsolatedHome`, `WaitForActive`, `GoProject`) are well-factored
- All unit tests pass

**Gaps:**
- `:overlay` mode tested in Docker integration but not in Podman or Containerd paths
- Tart and Seatbelt have no integration tests (macOS-only — acceptable if CI is Linux, but worth noting)
- No negative tests for invalid agent definitions or malformed network allowlists
- `defer os.RemoveAll()` in integration test setup can fail silently if tests hang

---

## 4. Architecture Soundness — Good, with One Structural Issue

**Solid:**
- Manager → Runtime dependency injection is clean
- Options structs (`CreateOptions`, `StartOptions`) bundle parameters without leaking
- Podman embedding Docker with socket override is the right pattern for code reuse
- Overlay capability validation uses `IsolationValidator` interface — backends opt in rather than failing at runtime

**Concerns:**

**a) Diff/apply mode branching** — `sandbox/diff.go` and `sandbox/apply.go` switch on `mode` string inline. No interface for "diffable directory." Adding a 4th mount mode (`:sync`, `:snapshot`) would require changes in 5+ files. Not a blocker now, but worth noting.


---

## 5. Documentation Alignment — Good

The user-facing `docs/GUIDE.md` matches implemented commands. Design docs accurately describe the copy/diff/apply model. `ARCHITECTURE.md`'s command→code map and data flow diagrams are current and accurate.

---

## 6. Enterprise Readiness — Mixed

**Good:**
- Multi-sink structured logging (stderr + JSONL + bug report) via `slog`
- `--bugreport` flag generates comprehensive diagnostics
- Graceful degradation with clear error messages for missing Docker, missing API keys, dirty repos
- Secrets mounted at `/run/secrets/` inside container (not baked into image)

**Missing:**
- No log rotation — `log.txt` in sandbox dir grows unbounded
- Exit code for "unapplied changes" is missing — `yoloai destroy` with pending changes returns exit 1 (generic), not a distinct code
- No concurrency controls — multiple simultaneous `yoloai new` calls to the same sandbox are not guarded
- Windows/WSL declared as "should degrade gracefully" but untested

---

## Priority Fixes Before GA

| Priority | Issue | Location |
|----------|-------|----------|
| Low | Log rotation policy | `sandbox/` or external |

---

## Bottom Line

The codebase is genuinely well-engineered — the abstractions are right, the patterns are consistent, the security thinking is present, and the documentation culture is strong. Remaining items are all polish or optional hardening. For a beta product, this is above average.

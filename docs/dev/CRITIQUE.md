# Critique

## Overall: A- — Well-engineered for beta; near enterprise-ready with a few gaps

---

## 1. Structure & Organization — Strong

The package layout (`cmd/`, `agent/`, `config/`, `runtime/`, `sandbox/`, `internal/cli/`) is clean and matches documented architecture. The `runtime.Runtime` interface is well-designed with no backend-specific types leaking out. The `Client` public API in `yoloai.go` is a nice touch for programmatic use.

**Minor drift:**
- `ARCHITECTURE.md` lists `sandbox_network_add.go` / `sandbox_network_remove.go` as separate files — actual code uses a single `sandbox_network.go`
- `config_aliases.go` exists but is omitted from the file index

---

## 2. Code Quality — Strong

- Consistent `fmt.Errorf("%w", err)` wrapping throughout
- Sentinel errors in `sandbox/errors.go` and typed errors in `config/errors.go` (exit codes 2, 3) are correct
- `gosec` annotations with rationales on file operations show security awareness
- `workspace/safety.go` (`IsDangerousDir`, `CheckPathOverlap`) is good hardening

**Issues:**
- **golangci-lint version mismatch** — project targets Go 1.26.1 but linter built with Go 1.25. `make check` fails in CI. This blocks releases.
- One `panic()` in `config/homedir.go` — acceptable for fatal bootstrapping, but should log context first
- `sandbox/lifecycle.go`: if `SaveMeta()` fails after prompt.txt write, state is inconsistent — no rollback

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

**Concerns:**

**a) Diff/apply mode branching** — `sandbox/diff.go` and `sandbox/apply.go` switch on `mode` string inline. No interface for "diffable directory." Adding a 4th mount mode (`:sync`, `:snapshot`) would require changes in 5+ files. Not a blocker now, but worth noting.

**b) Overlay capability not validated pre-create** — `backendCaps()` checks backend support, but `CAP_SYS_ADMIN` availability is only discovered at container run time. Containerd implements `IsolationValidator`; Docker silently skips it. Result: confusing failure mode.

**c) Silent partial diff** — if `git diff` fails on an overlay directory, `GenerateDiff()` continues silently. User may apply incomplete changes with no warning.

**d) No filesystem locking** — rapid concurrent `yoloai new` / `yoloai destroy` with the same sandbox name can race. No lockfile on `~/.yoloai/sandboxes/<name>/`.

---

## 5. Documentation Alignment — Good

The user-facing `docs/GUIDE.md` matches implemented commands. Design docs accurately describe the copy/diff/apply model. `ARCHITECTURE.md`'s command→code map and data flow diagrams are current and accurate.

**Minor drift:**
- `ARCHITECTURE.md` file index references two non-existent network files and omits `config_aliases.go`
- `design/README.md` references `meta.json` but actual filename is `environment.json` (post-migration)
- `--backend auto` detection order (Docker > Podman) not explicitly documented anywhere

---

## 6. Enterprise Readiness — Mixed

**Good:**
- Multi-sink structured logging (stderr + JSONL + bug report) via `slog`
- `--bugreport` flag generates comprehensive diagnostics
- Graceful degradation with clear error messages for missing Docker, missing API keys, dirty repos
- Secrets mounted at `/run/secrets/` inside container (not baked into image)

**Missing:**
- No `yoloai system check` / health check command — CI/CD pipelines need a way to verify prereqs
- No log rotation — `log.txt` in sandbox dir grows unbounded
- Exit code for "unapplied changes" is missing — `yoloai destroy` with pending changes returns exit 1 (generic), not a distinct code
- No concurrency controls — multiple simultaneous `yoloai new` calls to the same sandbox are not guarded
- Windows/WSL declared as "should degrade gracefully" but untested

---

## Priority Fixes Before GA

| Priority | Issue | Location |
|----------|-------|----------|
| High | golangci-lint version mismatch breaks `make check` | `Makefile:23`, `go.mod:3` |
| High | Overlay capability not validated for Docker backend | `sandbox/create.go:58-103` |
| High | Silent partial diff on git failure | `sandbox/diff.go` |
| Medium | No filesystem locking for concurrent operations | `sandbox/` package |
| Medium | ARCHITECTURE.md file index drift | `docs/dev/ARCHITECTURE.md:48,66` |
| Low | Add `yoloai system check` health command | new command |
| Low | Log rotation policy | `sandbox/` or external |

---

## Bottom Line

The codebase is genuinely well-engineered — the abstractions are right, the patterns are consistent, the security thinking is present, and the documentation culture is strong. The golangci-lint mismatch needs to be fixed immediately as it blocks the quality gate. The silent partial diff and missing overlay validation are correctness issues that could bite users. Everything else is polish. For a beta product, this is above average. For GA enterprise deployment, resolve the high-priority items above first.

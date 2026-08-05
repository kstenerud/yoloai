> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since; the code it describes has moved. It is
> **not a specification** — do not build from it or cite it as the current answer. Good for
> archaeology only: see [`../README.md`](../README.md).

> **ABOUTME:** Add a file-based lock per sandbox directory so concurrent `new`/`start`/`destroy`
> calls on the same sandbox can't corrupt `environment.json` or double-create a container.

# Concurrency guard for sandbox operations

- **Status:** IMPLEMENTED — `store.AcquireLock` shipped the per-sandbox flock this asked for.
- **Depends on:** —

**Superseded — the claim below is no longer true.** `store.AcquireLock(layout, name)` is a
per-sandbox `flock`, acquired in create, start, stop, destroy and reset, which is the file-based
lock this plan proposed. What follows is the original text.

No concurrency controls exist. Multiple simultaneous `yoloai new` calls with the same sandbox name, or concurrent `yoloai start`/`destroy` on the same sandbox, are not guarded. Could result in corrupted `environment.json`, double container creation, or partial state.

Fix: file-based lock per sandbox directory (e.g., `meta.lock`), held during operations that mutate sandbox state. Low priority for single-user CLIs but worth doing before any CI/CD integration.

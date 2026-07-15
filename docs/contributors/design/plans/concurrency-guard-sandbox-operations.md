> **ABOUTME:** Add a file-based lock per sandbox directory so concurrent `new`/`start`/`destroy`
> calls on the same sandbox can't corrupt `meta.json` or double-create a container.

# Concurrency guard for sandbox operations

- **Status:** UNSPECIFIED — idea only; not started.
- **Depends on:** —

No concurrency controls exist. Multiple simultaneous `yoloai new` calls with the same sandbox name, or concurrent `yoloai start`/`destroy` on the same sandbox, are not guarded. Could result in corrupted `meta.json`, double container creation, or partial state.

Fix: file-based lock per sandbox directory (e.g., `meta.lock`), held during operations that mutate sandbox state. Low priority for single-user CLIs but worth doing before any CI/CD integration.

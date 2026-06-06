<!-- ABOUTME: Terminal sink for abandoned questions — decided "won't do", drained from questions-unresolved.md. -->
<!-- ABOUTME: Distinct from resolved- (answered) and deferred- (parked w/ trigger): these are permanently dropped. -->

# Abandoned questions

Questions permanently dropped — decided **"won't do."** Distinct from
[`questions-resolved.md`](questions-resolved.md) (the question got *answered*) and
[`questions-deferred.md`](questions-deferred.md) (parked with a revival trigger): items here
are terminal and not expected to come back. Each carries a short **`Why:`** line recording
the reason for abandonment. Newest first.

78. ~~**Multiple `:copy` sandboxes from same source — sequential apply conflicts**~~ — **Removed.** The "compare two approaches in parallel" scenario is contrived — in practice you'd use `reset` or `--replace` to iterate sequentially. Accidental overlap (forgot a sandbox exists) is already covered by `git apply` error wrapping (#59).

**Why:** the "compare two approaches in parallel" scenario is contrived — in practice you iterate sequentially via `reset`/`--replace`; accidental overlap is already covered by #59's `git apply` error wrapping.

92. ~~**Git worktrees as a copy strategy (instead of cp -rp)**~~ — **Resolved: not pursuing.** `git worktree add` would be near-instant and share the object store, but has fundamental problems for coding agents: (a) `.gitignore`d files (`node_modules/`, build artifacts, `.env`) are not included — agents can't build or test without them; (b) worktree branches/refs are visible in the original repo — agent git operations pollute the host; (c) only works for git repos, not arbitrary directories. The planned overlayfs strategy (post-MVP) solves the same performance problem without these limitations.

**Why:** git-worktree-as-copy has fundamental problems for coding agents — `.gitignore`d files (node_modules, build artifacts, .env) are excluded, worktree refs pollute the host repo, and it only works for git repos. The overlayfs strategy solves the same performance problem without these limitations.

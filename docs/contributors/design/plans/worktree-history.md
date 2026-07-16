> **ABOUTME:** Design space for giving a linked-worktree workdir real git history in its
> sandbox, instead of the fresh baseline it gets today because a worktree's objects live in
> the main repo's `.git` and never come along with the copy.

# Plan: preserve git history for linked-worktree workdirs

- **Status:** UNSPECIFIED — idea only; shape sketched below, not designed. The safety half is
  done and on `main` (DF116): a worktree workdir gets a fresh standalone baseline and a warning
  saying history was not preserved. This plan is only about making the history actually arrive.
- **Depends on:** copy-mode-history.md

This is the worktree case of the promise [copy-mode-history.md](copy-mode-history.md) makes for a
normal repo, and it should not diverge from how that plan clones a real `.git`. That plan is still
IN-PROGRESS, so its mechanism is what this one has to match once it settles.

## Why this exists

`:copy` preserves a normal repo's history by CoW-cloning its `.git` directory into the work copy
([copy-mode-history.md](copy-mode-history.md)). A linked worktree has no `.git` directory to
clone: its `.git` is a ~142-byte file holding `gitdir: /path/to/main/.git/worktrees/<name>`, and
every object, ref and even the worktree's own `HEAD` and `index` live over in the main repo,
shared with every other worktree. Copying the worktree copies a pointer, not a repository.

So the copy has two possible states and today gets the second:

- **Keep the pointer** — on the host it still resolves, and "git in the copy" *is* "git in the
  user's real repo". That was DF116: the baseline commit landed on the user's own branch. Not an
  option, at any price.
- **Sever the pointer** — the copy has no repo, gets a fresh `git init` baseline, and the agent
  works without `log`/`blame`/`bisect`. This is what ships now, and it is at least honest: the
  warning says so.

Preserving history means a third state that does not exist yet: bring enough of the shared object
store along that the copy is a standalone repo of its own.

**Worth checking before building any of this:** whether agents materially benefit from history in
a sandbox. The cost below is real, and "the agent can read `git log`" may not be worth it. Nobody
has asked for this; it is filed because DF116 made the gap visible, not because a user hit it.

## Shape (sketched, not designed)

`git rev-parse --git-common-dir` from the worktree names the shared store. The copy needs that
store plus this worktree's own `HEAD`/`index`, reassembled as a normal `.git` directory:

1. Resolve `--git-common-dir` and `--git-dir` (the latter is `<common>/worktrees/<name>`).
2. Copy the common dir into `<workcopy>/.git` — CoW where available, mirroring what `copyGitDir`
   already does for a normal repo, so the cost is the same order as `:copy` on the main repo.
3. Graft in the worktree's own state: `HEAD`, `index`, `logs/HEAD`, `ORIG_HEAD` from
   `<common>/worktrees/<name>/`, which are the things that are *not* in the common dir.
4. Remove `<workcopy>/.git/worktrees/` — the copy is not a worktree host, and leaving other
   worktrees' administrative state in it invites a second DF116.

## Open questions — none investigated

- **Detached HEAD.** The worktree's `HEAD` may be a raw SHA rather than a ref. Grafting it is
  probably fine; unverified.
- **Per-worktree config.** `extensions.worktreeConfig` puts real settings in
  `<common>/worktrees/<name>/config.worktree`. Ignoring it silently changes behaviour in the copy.
- **Sparse checkout.** `<common>/worktrees/<name>/info/sparse-checkout` governs what the tree is
  *supposed* to contain; a copy that ignores it and a copy that honours it disagree about what
  "the project" is.
- **Every branch comes too.** The common dir holds refs for the main checkout and every other
  worktree. Is a sandbox seeing all of the user's branches acceptable? For a normal repo `:copy`
  it already is — cloning `.git` brings every branch — so the honest answer is probably yes, and
  this question is really "is that still true when the user only pointed us at one worktree?"
- **Size.** The common dir of a big repo is the whole history. `:copy` on the main repo already
  pays that, but a user who points at a worktree may not expect it, and `--copy-strict` should
  remain the way out.
- **`worktree.useRelativePaths`** (git 2.48+) changes the link text, not the topology — so it
  should not matter here. Confirm rather than assume; the sever deliberately never reads that text.

## What must not regress

- The copy must never contain a `.git` that points outside itself, in any form: gitlink file,
  relative gitlink, or symlink. That invariant (`workspace.RemoveGitLink`, applied at
  `CopyProjectDir` and after `resetInPlace`'s rsync) is what closed DF116, and any history
  scheme has to replace the link with a **real directory**, never resolve or relocate it.
- `IsGitRepo` must keep its existence semantics. It is also asked about *host* paths, where a
  linked worktree genuinely is a repo — see DF116's fix note for the five callers that break if
  it starts rejecting gitlinks.

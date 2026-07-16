> **ABOUTME:** Design for making in-place reset reproduce the file set create produced, so it
> stops re-importing the `.gitignore`d secrets `:copy` excludes and the history `--copy-strict`
> strips (DF117).

# Plan: in-place reset must honor the copy's semantics

- **Status:** IMPLEMENTED 2026-07-16 — DF117 and DF118 both closed. **The mechanism below is not
  what shipped:** this planned to keep rsync and feed it an allowlist; implementation dropped
  rsync from reset entirely. See "What actually shipped".
- **Depends on:** —

## The defect

`resetInPlace` is the **default** reset (`reset.go:89-99` only takes the restart path on
`--restart` or a dead container). It syncs with `rsync -a --delete src/ dst/` and **no excludes
of any kind** (`reset.go:481`): not `.gitignore`, not `IncludeIgnored`, not `StripHistory`, not
`isBuildArtifact`. So it mirrors the host directory wholesale into a work copy that create built
by careful subtraction, and undoes the subtraction.

Reproduced (DF117): a repo gitignoring `.env` and `node_modules/`, a `:copy` work copy with
`.env` correctly absent, one `rsync -a --delete orig/ work/` — and `.env` (`AWS_SECRET_KEY=…`)
is in the agent's work copy, `node_modules/` with it, and `.git/` too, which is the
`--copy-strict` limb.

**The contract this violates**, stated by `copy_gitignore.go`'s own ABOUTME: files a user
excluded from their repo "never get copied into a sandbox where the agent could read them".
Create honors it. Reset does not. One reset is enough.

## Why the obvious fixes are wrong

**"Just use `CopyProjectDir` like the restart path does."** Refuted in place by `rsyncDir`'s own
comment (`reset.go:473-479`): in-place reset runs with `dst` bind-mounted into a **live**
container, and a differential sync never leaves a window where the tree has vanished under the
running agent. Wipe-then-copy would. rsync is deliberate, and a fix has to stay differential.

**"Feed `--exclude-from` the ignored files."** This is what DF117 originally suggested and it is
wrong twice over:

1. **It is a denylist, so it fails open.** Anything we fail to exclude leaks. That is the wrong
   direction for the one control standing between an agent and a `.env`.
2. **rsync excludes are patterns, and filenames are not.** Verified: a file named
   `weird[1].env` **survives** an `--exclude-from` line of `/weird[1].env`, because rsync reads
   `[1]` as a character class and matches `weird1.env` instead. The secret leaks *because* we
   tried to exclude it. Backslash-escaping does work in rsync 3.4.4 (`/weird\[1\].env` excludes
   correctly), but it is not clearly documented for filter rules, so it is version-dependent
   behaviour holding up a security boundary.
3. **It needs a second enumeration.** Create's source of truth is `git.ListProjectFiles` — the
   files to *include*. A denylist has to enumerate the complement (`git ls-files --others
   --ignored`), which is a different query that can drift from create's answer. Two sources of
   truth for "what belongs in the copy" is how they disagree.

## What actually shipped

Not this. The plan below kept rsync because `rsyncDir`'s comment said rsync was load-bearing, and
that premise went unexamined until someone asked whether rsync was worth the hassle. It was not.

**rsync is gone from reset.** `resyncWorkCopy` removes the work copy's `.git`, re-copies through
`CopyProjectDir` — create's own dispatch — and prunes the result to `ProjectFileSet`'s answer.
Reset runs create's copy code, so "reset honors the copy's semantics" is true by construction
rather than by a gate.

**The premise did not survive contact.** rsync was justified by "a differential sync never leaves
a window where the tree has vanished". But `CopyDir` and `copyFileList` overwrite **in place**:
they never replace the destination directory, so the inode the container's bind-mount resolves to
survives — verified with `os.SameFile`, and asserted by `TestResyncWorkCopy_PreservesWorkDirInode`.
The property rsync was there to protect holds without rsync. The comment named a window; the thing
that actually mattered was inode identity, and nothing in reset was ever going to wipe the
directory anyway.

**Dropping it retired the hazards rather than managing them.** No patterns means no `weird[1].env`
escape and no version-dependent escaping. It also disarmed a trap this plan would have walked into:
`rsync -a --files-from` does **not** recurse into a listed directory, but `rsync -a -r --files-from`
**does** — so a later hand adding an `-r` that looks redundant beside `-a` would have silently
re-imported `node_modules` and friends. That flag no longer exists to add. And rsync stopped being
a host runtime dependency: its required-tooling row and two `exec.LookPath("rsync")` test
preconditions went with it. Tart's in-VM rsync is the guest's and is untouched.

**What survived from the plan:** the allowlist *idea* (enumerate what belongs, never enumerate what
does not), the prune, `.git` replaced as a unit, and the acceptance property. `ProjectFileSet` is
still needed — the prune has to be driven by something — and `TestProjectFileSet_MatchesCopyProjectDir`
still gates it against what `CopyProjectDir` writes.

**The cost, accepted:** unchanged files are re-copied, so a reset costs what a create costs. A
size+mtime skip in `copyFile` would restore differential speed and would make create's re-copy
faster too, but it is the same quick check that caused DF118, so it was not added on speculation.
If reset latency ever bites on a large tree, that is the lever.

The rest of this file is the reasoning as it stood before implementation. It is kept because the
refuted options are still refuted — the denylist analysis below is why the allowlist idea survived
even though its rsync vehicle did not.

## The mechanism as planned: allowlist, then prune (superseded — rsync dropped)

**Allowlist, because it fails closed.** With `--files-from`, the copy contains exactly what we
enumerated. A file we forget to list is *missing from the sandbox* (a functionality bug, loud and
harmless) rather than *present in the sandbox* (a leak, silent and not). Verified: `--files-from`
treats paths **literally** — `weird[1].txt` and `star*.txt` both transfer correctly, and there is
no pattern for a filename to escape, because there are no patterns.

Verified gap: **`--files-from` does not prune.** `rsync -a --delete --files-from=list src/ dst/`
leaves `dst`'s extraneous files in place — agent-created files and a previously-leaked `.env` both
survived. Since discarding the agent's changes is what reset is *for*, the transfer needs a
companion pass.

So, per `:copy` dir:

1. **Enumerate** the desired set with the same helper create uses, so there is one source of
   truth. Extracting that from `CopyProjectDir` (which currently fuses enumeration and copying,
   which is why reset cannot reuse it) is most of the work here.
2. **Transfer** with `rsync -a --files-from=<list> src/ dst/`. Literal, differential, no window
   where the tree is gone.
3. **Prune** `dst` of anything not in the set. A walk and a delete, driven by our own enumeration
   rather than by rsync's pattern semantics. This is `--delete` reimplemented, deliberately: the
   point is that it deletes by the *same* answer the transfer used.

`:copy-all` may keep a denylist safely, and this is not a double standard: its exclusions are the
fixed artifact list (`.build`, `DerivedData`, `node_modules`, `__pycache__`, `*/xcuserdata`),
which **we** author. No user-controlled filename ever becomes a pattern, so the escaping hazard
cannot arise. Decide during implementation whether that is worth two mechanisms or whether
`:copy-all` should enumerate too, for one code path.

## Open: `.git`, which is where this meets DF118

The work copy's `.git` is not covered by the above and is genuinely harder:

- **`--copy-strict` / no-preserve:** `src/.git` must **not** transfer (that is the leak), and
  `dst/.git` is create's fresh baseline repo, which the prune must not eat. So `.git` is excluded
  from the desired set *and* exempted from the prune, and the existing re-baseline handles it.
- **Preserve (the `:copy` default):** `dst/.git` must come to match `src/.git`, which means
  losing the agent's commits — that is what reset means. A plain recursive sync **merges**
  instead of replacing (DF118): `--delete` removes the objects while the size+mtime quick check
  can skip the 41-byte ref that points at them, leaving `fatal: bad object HEAD` and a recorded
  baseline naming a missing object. So this sync needs the quick check defeated (`--ignore-times`
  or `--checksum`, both of which cost real time on a large `.git`), or a different approach
  entirely.
- **A `.git` link** is already handled and must stay handled: `RemoveGitLink` after the rsync
  (DF116). Any redesign here must keep that, or a worktree workdir starts reading the user's real
  repo again.

**Fix DF118 here or not?** They are the same call site and the `.git` sync cannot be made correct
without answering DF118. Probably one change; that is a decision for whoever picks this up.

## Acceptance

The property worth testing is stronger than "no `.env` in the copy", and it is what should be
asserted: **for a given DirSpec, in-place reset leaves the same file set that create would have
produced.** Compare a reset work copy against a fresh `CopyProjectDir` of the same source, tree
for tree, per mode (`:copy`, `:copy-all`, `:copy-strict`, non-repo). A test written that way also
catches the artifact and history limbs for free, and it cannot rot into "we remembered to exclude
the things we thought of".

Cases the tests must carry, because each has already fooled someone:

- a gitignored `.env` present on the host and absent from the copy, before **and after** reset;
- a filename containing `[`, to keep anyone from reintroducing a pattern-based denylist;
- a previously-leaked file already in `dst`, which reset must remove rather than preserve;
- `--copy-strict`, where `src/.git` exists and must not arrive;
- a linked worktree, which must not re-acquire its gitlink (DF116).

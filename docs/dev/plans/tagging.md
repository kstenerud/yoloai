# Plan: Tag Transfer on Apply

## Goal

When an agent creates git tags in a sandbox, transfer those tags to the host
during `yoloai apply`. Tags are opt-in via `--tags` flag. If tags exist but
`--tags` was not passed, print a hint in the pre-confirmation summary so the
user can cancel and re-run with `--tags` before the baseline advances.

## Design Decisions

- **Opt-in**: tags are NOT applied by default. Backward compatible, no silent side effects.
- **Hint before confirmation**: if tags exist beyond the baseline and `--tags` is not set,
  print "Note: N tag(s) not applied (use --tags to include them)" in the summary, before
  the `Apply to ...? [y/N]` prompt. After confirmation (baseline advances + SHAs change),
  it is too late to apply tags.
- **Tags follow applied commits**: tags whose target commit was not included in the apply
  are skipped with a per-tag warning (e.g. selective apply, path-filtered apply).
- **Squash + WIP-only incompatible**: `--tags` with `--squash` or WIP-only apply returns
  an error — individual commit identity is destroyed, so there is no SHA to map from.
- **Annotated tags**: preserve the tag message. Tagger identity and timestamp will be
  from the host context (acceptable).
- **`yoloai diff --log`**: show tags inline next to the commits they point to.

## SHA Mapping Problem

`git format-patch | git am` creates new commits on the host with different SHAs.
A tag `v1.0.0 → sandbox-sha-abc` cannot be directly transferred; it must be
re-pointed to the host SHA that corresponds to that sandbox commit.

Solution: parse `git am` output to extract the sandbox→host SHA mapping as patches
are applied, then use that map to re-create tags on the host.

`git am` prints lines like:
```
Applying: <subject>
```
After each patch, `git rev-parse HEAD` gives the new host SHA. Build the map
by walking commits in order alongside the am output.

## Implementation Plan

### 1. `sandbox/tags.go` — list tags beyond baseline

New file. Function:

```go
// TagInfo holds a tag name and the sandbox commit SHA it points to.
type TagInfo struct {
    Name    string
    SHA     string // commit SHA the tag points to (dereferenced)
    Message string // empty for lightweight tags
}

// ListTagsBeyondBaseline returns tags in the sandbox work copy that point to
// commits beyond the baseline SHA. Only :copy mode is supported.
func ListTagsBeyondBaseline(name string) ([]TagInfo, error)
```

Implementation: `git tag --list` + `git rev-list <baseline>..HEAD` to get
commit SHAs beyond baseline, then filter tags whose dereferenced commit SHA
is in that set.

Use `git for-each-ref --format=%(refname:short) %(objecttype) %(*objectname) %(objectname) %(contents:subject)`
to get tag name, type (tag=annotated, commit=lightweight), dereferenced SHA,
and message in one pass.

### 2. `workspace/tags.go` — create tags on the host

New file. Functions:

```go
// CreateTag creates a lightweight or annotated tag on the host repo.
// If message is empty, creates a lightweight tag. Otherwise annotated.
func CreateTag(dir, name, sha, message string) error
```

Uses `git tag <name> <sha>` (lightweight) or `git tag -a <name> <sha> -m <message>` (annotated).

### 3. `workspace/apply.go` — return SHA mapping from ApplyFormatPatch

Change signature:

```go
// ApplyFormatPatch applies patches and returns a map of sandbox SHA → host SHA.
func ApplyFormatPatch(patchDir string, files []string, targetDir string) (map[string]string, error)
```

After `git am` completes, walk the applied commits using `git log` on the
target to recover the new SHAs in order, paired with subjects from the patch
filenames (or the sandbox commit list passed in). The simplest approach:
before apply, record `git rev-parse HEAD` as the pre-apply tip; after apply,
use `git log <pre-tip>..HEAD --reverse --format=%H` to get the N new host SHAs
in order, paired positionally with the N sandbox SHAs from the input list.

### 4. `internal/cli/apply.go` — wire it together

**Pre-confirmation summary** (format-patch path only):
- Call `sandbox.ListTagsBeyondBaseline(name)`
- If `--tags` not set and tags exist: print hint before confirmation prompt
- If `--tags` set: proceed to apply tags after commits

**After commits applied**:
- If `--tags`: iterate `sandbox.ListTagsBeyondBaseline`, look up each tag's
  sandbox SHA in the mapping, call `workspace.CreateTag` if found, warn if not.
- Log counts: "N tag(s) applied", "M tag(s) skipped (target commit not applied)"

**Flag**:
```go
cmd.Flags().Bool("tags", false, "Transfer git tags created by the agent")
cmd.MarkFlagsMutuallyExclusive("tags", "squash")
```
WIP-only (no commits, only WIP) + `--tags`: return error before applying.

**JSON output**: add `TagsApplied int` and `TagsSkipped int` to `applyResult`.

### 5. `internal/cli/diff.go` — show tags in `--log` output

In `diffLog`, after fetching commits, call `sandbox.ListTagsBeyondBaseline`
and build a map of `SHA → []TagInfo`. When printing each commit line, append
tag markers:

```
  1  abc123def456  Bump version      [tag: v1.0.0]
  2  789abc123def  Update changelog
```

Also update `diffLogJSON` to include a `tags` field in the response.

## Constraints / Out of Scope

- Overlay sandboxes: not supported in this iteration (overlay apply does not
  use format-patch; no per-commit SHA mapping available). Document this.
- Selective apply: tags on unapplied commits are warned about, not errored.
- Squash apply: `--tags` is a hard error.
- Tag conflicts: if a tag already exists on the host, warn and skip (do not
  overwrite).

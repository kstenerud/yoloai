#!/usr/bin/env python3
# ABOUTME: Commit-message gate for a PR's own commits: subject shape, prose body
# ABOUTME: on code commits, and AI attribution trailers. Runs in CI, not in
# ABOUTME: `make check` — it needs a base ref, and fork PRs never run our hooks.
"""Lint the commit messages a pull request adds.

The rules are stated in AGENTS.md; this is the machine that enforces them. It
checks only `<base>..<head>`, never history — the conventions solidified around
June 2026 (18% -> 94% -> 99% conformance by month), so older commits use a
superseded style and are none of a PR's business.

Why this is CI-only rather than a `make check` prerequisite: it needs a base ref
to diff against, which `make check` has no notion of (on `main` the range is
empty and the check is vacuous). `make lint-commits` runs it locally against
origin/main for anyone who wants it before pushing.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass

# The type set. `.goreleaser.yaml` groups its changelog on some of these
# prefixes, but that config is currently inert (release.yml always passes
# --release-notes, so goreleaser never generates a changelog — DF91). So this
# set is a convention, enforced here because it is a convention, not because
# some downstream tool parses it. Two historical commits use `harden:`; they
# predate the settled set and are not a licence to add more.
TYPES = ("feat", "fix", "docs", "test", "refactor", "build", "ci", "chore", "perf")

# Types whose commits must explain themselves in a body. 435 of 437 code commits
# in all history carry one; a `docs:` typo fix legitimately does not.
CODE_TYPES = ("feat", "fix", "refactor", "perf")

SUBJECT_RE = re.compile(rf"^({'|'.join(TYPES)})(\([a-z0-9.\-/]+\))?(!)?: (.+)$")

# git's own default subject for `git revert`. b67dff88 uses it; rejecting it
# would mean hand-editing a message git wrote correctly.
REVERT_RE = re.compile(r'^Revert "')

TRAILER_RE = re.compile(r"^Co-Authored-By:\s*(.+?)\s*<", re.IGNORECASE | re.MULTILINE)

# Trailers and tool noise are not a body.
NOISE_RE = re.compile(
    r"^(Co-Authored-By:|Co-authored-by:|Signed-off-by:|🤖 Generated with)", re.IGNORECASE
)


@dataclass(frozen=True)
class Commit:
    sha: str
    subject: str
    body: str


@dataclass(frozen=True)
class Problem:
    sha: str
    subject: str
    rule: str
    detail: str


def check_subject(c: Commit) -> list[Problem]:
    """Subject must be `type(scope): summary` — imperative, no trailing period."""
    out: list[Problem] = []
    if REVERT_RE.match(c.subject):
        return out
    m = SUBJECT_RE.match(c.subject)
    if not m:
        out.append(
            Problem(
                c.sha,
                c.subject,
                "subject-shape",
                f"expected `type(scope): summary` with type in {', '.join(TYPES)}; "
                "breaking changes mark the type with `!` (e.g. `fix(config)!: ...`)",
            )
        )
        return out
    if m.group(4).endswith("."):
        out.append(
            Problem(
                c.sha, c.subject, "trailing-period",
                "drop the trailing period (0 of 2137 commits have one)",
            )
        )
    return out


def check_body(c: Commit) -> list[Problem]:
    """Code commits carry prose saying what was wrong, not a restatement of the diff."""
    m = SUBJECT_RE.match(c.subject)
    if not m or m.group(1) not in CODE_TYPES:
        return []
    real = [
        line for line in c.body.splitlines()
        if line.strip() and not NOISE_RE.match(line.strip())
    ]
    if real:
        return []
    return [
        Problem(
            c.sha, c.subject, "missing-body",
            f"a `{m.group(1)}` commit needs a body saying what was wrong or missing "
            "and why this is the fix (trailers alone don't count)",
        )
    ]


def check_trailers(c: Commit) -> list[Problem]:
    """AI attribution names the model; the tool's own footer is stripped."""
    out: list[Problem] = []
    full = f"{c.subject}\n{c.body}"

    if "Generated with [Claude Code]" in full or "🤖" in full:
        out.append(
            Problem(
                c.sha, c.subject, "robot-footer",
                "drop the `🤖 Generated with [Claude Code]` footer — 0 of 2176 "
                "commits carry it; attribution goes in the Co-Authored-By trailer",
            )
        )

    # Presence is NOT required: human and dependabot commits legitimately have no
    # trailer. Only its shape is checked. No model allowlist either — the set is
    # open and grows (Fable 5 and Sonnet 5 both appear in history).
    for who in TRAILER_RE.findall(c.body):
        if who.strip().lower() == "claude":
            out.append(
                Problem(
                    c.sha, c.subject, "bare-claude-trailer",
                    "name the exact model, e.g. `Co-Authored-By: Claude Opus 4.8 "
                    "<noreply@anthropic.com>` — a bare `Claude` says nothing about "
                    "what wrote it",
                )
            )
    return out


def read_commits(base: str, head: str) -> list[Commit]:
    sep, rec = "\x1f", "\x1e"
    out = subprocess.run(
        ["git", "log", "--no-merges", f"--format=%H{sep}%s{sep}%b{rec}", f"{base}..{head}"],
        capture_output=True, text=True, check=True,
    ).stdout
    commits: list[Commit] = []
    for chunk in out.split(rec):
        if not chunk.strip():
            continue
        parts = chunk.lstrip("\n").split(sep)
        if len(parts) < 3:
            continue
        commits.append(Commit(parts[0], parts[1], parts[2]))
    return commits


def lint(commits: list[Commit]) -> list[Problem]:
    problems: list[Problem] = []
    for c in commits:
        problems.extend(check_subject(c))
        problems.extend(check_body(c))
        problems.extend(check_trailers(c))
    return problems


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default="origin/main", help="base ref (default: origin/main)")
    ap.add_argument("--head", default="HEAD", help="head ref (default: HEAD)")
    args = ap.parse_args()

    commits = read_commits(args.base, args.head)
    if not commits:
        print(f"lint-commits: no commits in {args.base}..{args.head}")
        return 0

    problems = lint(commits)
    if not problems:
        print(f"lint-commits: {len(commits)} commit(s) OK")
        return 0

    print(f"lint-commits: {len(problems)} problem(s) in {len(commits)} commit(s)\n", file=sys.stderr)
    for p in problems:
        print(f"  {p.sha[:8]} {p.subject}", file=sys.stderr)
        print(f"      [{p.rule}] {p.detail}\n", file=sys.stderr)
    print("The rules are in AGENTS.md ('Preparing a PR').", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())

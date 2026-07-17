#!/usr/bin/env python3
# ABOUTME: CI gate. Fails a branch that removes a user-visible config key or CLI
# ABOUTME: flag without a docs/BREAKING-CHANGES.md entry in the same change.

"""Require a BREAKING-CHANGES entry when a user-visible name disappears.

Rule 1 of AGENTS.md is the most-missed rule in the repo, and the only thing
gating it ran at the wrong end. `release.yml` fires on a release tag and asserts
`## Unreleased` is EMPTY — a drain check. It proves entries that exist were
shipped under a version heading; it cannot see an entry that was never written.
So it passes most loudly in exactly the failure case: nobody files, the section
is empty, the release goes green. Rule 1's evidence is PR #36, which made
`yoloai config set backend docker` exit 1 across nine changed files, none of them
`docs/BREAKING-CHANGES.md`; a maintainer caught it in review, and `backend` had
by then shipped dead through 15 releases with `make check` green throughout.

This is the other end: it runs on the branch, where the entry is owed.

What it can and cannot see. It compares the SET of user-visible names at the base
ref against the set at HEAD and fires only on a name that DISAPPEARED — a removal
or a rename (which is a removal plus an addition). It cannot see a changed
default or newly-rejected input, which rule 1 also covers; those stay
review-caught, deliberately, exactly as D122 left D/DF citations. A partial gate
on the recurring case beats no gate on all of them.

Why a set difference rather than reading the diff: a name that merely MOVES
between files is removed and added in the same change, and a line-oriented check
would fire on it. Tree-wide sets make a move a non-event. That class — the one a
reader cannot do and a machine does in milliseconds — is the argument of
research/llm-shaped-repos.md Part 7.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys

BREAKING_CHANGES = "docs/BREAKING-CHANGES.md"

# The config-key registry: `{"container_backend", ""}` rows in the knownSettings
# tables. These literals ARE the user's surface — the key they typed into a YAML
# file a year ago — and config.IsKnownConfigPath reads the same tables.
CONFIG_KEYS_FILE = "internal/config/config.go"
CONFIG_KEY = re.compile(r'^\s*\{"([a-z][a-z0-9_.]*)",', re.M)

# Cobra flag DECLARATIONS, both forms: Flags().String("json", ...) and
# Flags().StringVar(&x, "name", ...), with or without the Persistent prefix and
# the P (shorthand) suffix.
#
# The verb list is an allowlist, not `\w+`, and that is load-bearing: `\w+` also
# matches `Flags().GetBool("debug")`, which READS a flag rather than declaring
# one. That bug made this gate silently blind — renaming --debug at its
# declaration left two GetBool("debug") readers behind, so the name never left the
# set and no removal was seen. Caught by probing the gate with a real rename
# instead of trusting that the pattern meant what it looked like.
FLAG_VERBS = (
    "String|StringSlice|StringArray|StringToString|Bool|BoolSlice|Count|Duration"
    "|Float32|Float64|Int|Int8|Int16|Int32|Int64|IntSlice|Uint|Uint8|Uint16|Uint32|Uint64|IP"
)
FLAG_DECL = re.compile(
    r"(?:Persistent)?Flags\(\)\.(?:" + FLAG_VERBS + r")(?:Var)?P?\("
    r'\s*(?:&[^,]+,\s*)?"([a-z][a-z0-9-]*)"'
)


def _git(*args: str) -> str:
    return subprocess.run(["git", *args], capture_output=True, text=True, check=False).stdout


def config_keys_at(ref: str) -> set[str]:
    """The config keys declared at ref."""
    return set(CONFIG_KEY.findall(_git("show", f"{ref}:{CONFIG_KEYS_FILE}")))


def flags_at(ref: str) -> set[str]:
    """Every CLI flag name declared under internal/cli at ref.

    Read from the tree at that ref rather than from the diff, so a flag moving
    between files is not a removal.
    """
    files = [
        f
        for f in _git("ls-tree", "-r", "--name-only", ref, "internal/cli/").splitlines()
        if f.endswith(".go") and not f.endswith("_test.go")
    ]
    names: set[str] = set()
    for f in files:
        names |= set(FLAG_DECL.findall(_git("show", f"{ref}:{f}")))
    return names


def touched(base: str, head: str, path: str) -> bool:
    return path in _git("diff", "--name-only", f"{base}...{head}").split()


def _vanished(base_set: set[str], head_set: set[str]) -> set[str]:
    """Names in base_set and not head_set — unless head_set is empty.

    An empty head set does not mean every name was deleted at once; it means the
    extractor lost its target, because the registry moved or changed shape. Firing
    then would report twenty simultaneous removals at whoever relocated a file,
    which is the false accusation that gets a gate deleted rather than fixed. The
    move is caught instead by test_the_registries_are_where_this_thinks — a test
    fails, loudly, at the person doing the moving, which is the same split D122
    uses for its research root.
    """
    if not head_set:
        return set()
    return base_set - head_set


def removed_names(base: str, head: str) -> dict[str, set[str]]:
    """User-visible names present at base and gone at head, by kind."""
    out = {
        "config key": _vanished(config_keys_at(base), config_keys_at(head)),
        "CLI flag": _vanished(flags_at(base), flags_at(head)),
    }
    return {kind: names for kind, names in out.items() if names}


def reason(removed: dict[str, set[str]]) -> str:
    lines = []
    for kind, names in sorted(removed.items()):
        for n in sorted(names):
            lines.append(f"  - {kind} {n!r} existed at the base ref and does not exist here")
    return (
        "ERROR: this branch removes or renames a user-visible name and adds no\n"
        f"       {BREAKING_CHANGES} entry.\n\n"
        + "\n".join(lines)
        + "\n\n"
        "A user typed that name into a script or a YAML file and will not read the\n"
        "diff that deleted it. That is rule 1, and it is the most-missed rule here:\n"
        "PR #36 made `yoloai config set backend docker` exit 1 across nine changed\n"
        "files, none of them the changelog — a human caught it in review, and the\n"
        "dead key had already shipped through 15 releases with `make check` green.\n\n"
        "Fix: add an entry under `## Unreleased` (never under a version heading —\n"
        "those are frozen) with previous behaviour, new behaviour, impact, and\n"
        "migration. Copy a neighbouring entry.\n\n"
        "If this is NOT user-visible — an unexported helper, a flag that never\n"
        "shipped — say so in review; this gate sees a name vanish, not who used it."
    )


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", required=True, help="ref the branch forked from")
    ap.add_argument("--head", default="HEAD")
    args = ap.parse_args()

    if touched(args.base, args.head, BREAKING_CHANGES):
        return 0  # an entry was written; whether it is a GOOD entry is review's job
    removed = removed_names(args.base, args.head)
    if not removed:
        return 0
    print(reason(removed), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())

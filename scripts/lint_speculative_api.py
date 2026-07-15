#!/usr/bin/env python3
# ABOUTME: Finds speculative API — code with no production caller, which the
# ABOUTME: normal lint cannot see because `unused` counts a test as a caller.
# ABOUTME: Cross-checks every platform so a GOOS-only caller still vouches (D125).
"""Report declarations whose only callers are tests, per D125.

`golangci-lint`'s `unused` counts a test caller as a caller, so a function whose
sole user is its own test is invisible to `make check` forever — the coverage is
the camouflage. Running `unused` with `--tests=false` surfaces that class, but a
single run cannot be trusted:

  * A declaration in a `_linux.go` file is not compiled under GOOS=darwin, so
    darwin has no opinion about it. Intersecting the platforms would drop it.
  * A declaration in a plain `.go` file whose only caller lives in a
    `_darwin.go` file looks unused under GOOS=linux, where that caller is
    excluded. Unioning the platforms would falsely accuse it.

Both cases are real in this tree, which is why neither union nor intersection is
the answer. The rule is: a declaration is speculative iff it is flagged on
*every platform where its file is actually compiled*, and there is at least one
such platform. That needs `go list` per platform to know which files each one
builds — the verdict is not derivable from the linter output alone.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

# e.g. "store/netfs_linux.go:34:6: func isNetworkFilesystemMagic is unused (unused)"
FINDING_RE = re.compile(
    r"^(?P<file>[^:]+):(?P<line>\d+):\d+: "
    r"(?P<kind>func|var|const|type|field) (?P<name>\S+) is unused \(unused\)$"
)

DEFAULT_PLATFORMS = ("linux/amd64", "darwin/arm64")


def parse_findings(stdout: str) -> set[tuple[str, str, str]]:
    """Extract (file, kind, name) triples from golangci-lint's output.

    Line numbers are deliberately dropped: the same declaration can sit on a
    different line under a different build, and identity here is the thing
    named, not where it sat.
    """
    out: set[tuple[str, str, str]] = set()
    for line in stdout.splitlines():
        m = FINDING_RE.match(line.strip())
        if m:
            out.add((m.group("file"), m.group("kind"), m.group("name")))
    return out


def compiled_files(go_list_json: str, root: str) -> set[str]:
    """Return every .go file `go list -json ./...` reports as built.

    Only GoFiles/CgoFiles count — TestGoFiles are excluded by construction,
    which is the whole point: a file that exists is not a file that compiles
    into the binary for this platform.
    """
    files: set[str] = set()
    dec = json.JSONDecoder()
    idx = 0
    blob = go_list_json.strip()
    while idx < len(blob):
        while idx < len(blob) and blob[idx].isspace():
            idx += 1
        if idx >= len(blob):
            break
        pkg, idx = dec.raw_decode(blob, idx)
        pkg_dir = pkg.get("Dir", "")
        if not pkg_dir:
            continue
        rel_dir = os.path.relpath(pkg_dir, root)
        for f in list(pkg.get("GoFiles", [])) + list(pkg.get("CgoFiles", [])):
            rel = f if rel_dir == "." else f"{rel_dir}/{f}"
            files.add(rel.replace(os.sep, "/"))
    return files


def speculative(
    flagged: dict[str, set[tuple[str, str, str]]],
    compiled: dict[str, set[str]],
) -> list[tuple[str, str, str, list[str]]]:
    """Return the declarations that are speculative on every platform that builds them.

    flagged/compiled are keyed by platform. Returns (file, kind, name, platforms)
    sorted for stable output, where `platforms` is where the file compiles.
    """
    verdicts: list[tuple[str, str, str, list[str]]] = []
    every = sorted({f for s in flagged.values() for f in s})
    for file, kind, name in every:
        builds_on = sorted(p for p in compiled if file in compiled[p])
        if not builds_on:
            # No platform we checked compiles it. Say nothing rather than guess:
            # the caller may live behind a GOOS we do not lint.
            continue
        if all((file, kind, name) in flagged[p] for p in builds_on):
            verdicts.append((file, kind, name, builds_on))
    return verdicts


def run(cmd: list[str], env: dict[str, str], cwd: str) -> str:
    proc = subprocess.run(cmd, env=env, cwd=cwd, capture_output=True, text=True, check=False)
    return proc.stdout


def collect(platform: str, linter: str, root: str) -> tuple[set[tuple[str, str, str]], set[str]]:
    goos, goarch = platform.split("/", 1)
    env = {**os.environ, "GOOS": goos, "GOARCH": goarch}
    listed = run(["go", "list", "-json", "./..."], env, root)
    if not listed.strip():
        raise SystemExit(f"go list produced nothing for {platform}; cannot judge what compiles there")
    found = run(
        [linter, "run", "--no-config", "--default=none", "-E", "unused", "--tests=false", "./..."],
        env,
        root,
    )
    return parse_findings(found), compiled_files(listed, root)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--linter", default="golangci-lint", help="path to a host-native golangci-lint")
    ap.add_argument("--platforms", default=",".join(DEFAULT_PLATFORMS))
    ap.add_argument("--root", default=".")
    args = ap.parse_args()

    root = str(Path(args.root).resolve())
    platforms = [p.strip() for p in args.platforms.split(",") if p.strip()]

    flagged: dict[str, set[tuple[str, str, str]]] = {}
    compiled: dict[str, set[str]] = {}
    for p in platforms:
        print(f">> unused --tests=false {p}", flush=True)
        flagged[p], compiled[p] = collect(p, args.linter, root)

    verdicts = speculative(flagged, compiled)
    if not verdicts:
        print(f"lint-speculative-api: no speculative API across {', '.join(platforms)}")
        return 0

    print()
    for file, kind, name, builds_on in verdicts:
        print(f"{file}: {kind} {name} has no caller outside tests (on {', '.join(builds_on)})")
    print()
    print(
        "Speculative API (D125): a declaration whose only callers are tests. Either give it a\n"
        "production caller, or delete it. Two things worth knowing before you reach for an\n"
        "exemption:\n"
        '  * "it is the pure, testable seam" is the defect, not the justification — make\n'
        "    production call the thing under test (DF108 is the worked example).\n"
        "  * if you delete it, retarget its tests onto the live path rather than deleting them\n"
        "    with it; they may be the only coverage that path has (DF105).",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())

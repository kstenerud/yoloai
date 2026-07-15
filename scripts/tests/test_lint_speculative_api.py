# ABOUTME: Tests for lint_speculative_api's verdict rule and output parsing.
# ABOUTME: The two fixtures below are the real cases that make union and
# ABOUTME: intersection both wrong; they are why the rule is compile-aware.

from lint_speculative_api import compiled_files, parse_findings, speculative

LINUX = "linux/amd64"
DARWIN = "darwin/arm64"


def test_parse_findings_extracts_file_kind_and_name() -> None:
    out = (
        "store/netfs_linux.go:34:6: func isNetworkFilesystemMagic is unused (unused)\n"
        "runtime/seatbelt/prune.go:130:6: func parseSandboxProcLine is unused (unused)\n"
        "3 issues:\n"
        "* unused: 3\n"
    )
    assert parse_findings(out) == {
        ("store/netfs_linux.go", "func", "isNetworkFilesystemMagic"),
        ("runtime/seatbelt/prune.go", "func", "parseSandboxProcLine"),
    }


def test_parse_findings_ignores_other_linters_and_noise() -> None:
    out = (
        "repo_hygiene_test.go:1962:1: cognitive complexity 21 is high (gocognit)\n"
        "store/x.go:1:1: some other diagnostic (staticcheck)\n"
        "level=warning msg=something\n"
    )
    assert parse_findings(out) == set()


def test_parse_findings_covers_non_func_kinds() -> None:
    out = (
        "a/b.go:3:2: var someVar is unused (unused)\n"
        "a/b.go:4:2: type someType is unused (unused)\n"
        "a/b.go:5:2: const someConst is unused (unused)\n"
        "a/b.go:6:2: field someField is unused (unused)\n"
    )
    assert {k for _, k, _ in parse_findings(out)} == {"var", "type", "const", "field"}


def test_parse_findings_drops_line_numbers_so_a_shifted_decl_still_matches() -> None:
    # The same declaration can sit on a different line under a different build.
    linux = parse_findings("a/b.go:34:6: func F is unused (unused)")
    darwin = parse_findings("a/b.go:99:6: func F is unused (unused)")
    assert linux == darwin


def test_speculative_flags_a_decl_dead_on_the_only_platform_that_builds_it() -> None:
    # The real DF108 shape: netfs_linux.go compiles only on linux, so darwin has
    # no opinion. Intersecting the platforms would wrongly clear it.
    file = "store/netfs_linux.go"
    flagged = {LINUX: {(file, "func", "isNetworkFilesystemMagic")}, DARWIN: set()}
    compiled = {LINUX: {file}, DARWIN: set()}
    assert speculative(flagged, compiled) == [
        (file, "func", "isNetworkFilesystemMagic", [LINUX])
    ]


def test_speculative_clears_a_decl_whose_only_caller_is_goos_specific() -> None:
    # The real seatbelt shape: prune.go compiles everywhere, but its caller lives
    # in prune_darwin.go. Unioning the platforms would wrongly accuse it.
    file = "runtime/seatbelt/prune.go"
    flagged = {LINUX: {(file, "func", "parseSandboxProcLine")}, DARWIN: set()}
    compiled = {LINUX: {file}, DARWIN: {file}}
    assert speculative(flagged, compiled) == []


def test_speculative_flags_a_decl_dead_on_every_platform_that_builds_it() -> None:
    file = "internal/envsetup/envsetup.go"
    entry = (file, "func", "CreateSecretsDir")
    flagged = {LINUX: {entry}, DARWIN: {entry}}
    compiled = {LINUX: {file}, DARWIN: {file}}
    assert speculative(flagged, compiled) == [(*entry, [DARWIN, LINUX])]


def test_speculative_says_nothing_when_no_checked_platform_builds_the_file() -> None:
    # A windows-only file when we lint linux+darwin: the caller may live behind a
    # GOOS we never compile, so silence beats a guess.
    file = "store/paths_windows.go"
    flagged = {LINUX: {(file, "func", "F")}, DARWIN: set()}
    compiled: dict[str, set[str]] = {LINUX: set(), DARWIN: set()}
    assert speculative(flagged, compiled) == []


def test_compiled_files_reads_go_list_and_excludes_tests() -> None:
    root = "/repo"
    blob = (
        '{"Dir": "/repo/store", "GoFiles": ["netfs.go", "netfs_linux.go"],'
        ' "TestGoFiles": ["netfs_linux_test.go"]}\n'
        '{"Dir": "/repo", "GoFiles": ["client.go"]}\n'
    )
    got = compiled_files(blob, root)
    assert got == {"store/netfs.go", "store/netfs_linux.go", "client.go"}
    assert "store/netfs_linux_test.go" not in got


def test_compiled_files_includes_cgo_files() -> None:
    blob = '{"Dir": "/repo/x", "GoFiles": ["a.go"], "CgoFiles": ["b.go"]}'
    assert compiled_files(blob, "/repo") == {"x/a.go", "x/b.go"}

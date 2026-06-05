# ABOUTME: Unit tests for the govulncheck allowlist policy: suppression while
# ABOUTME: unfixed, auto-fail once a fix is reported, and unexpected/stale paths.

from __future__ import annotations

import govulncheck
from govulncheck import Finding, classify, parse_findings

DOCKER = "github.com/docker/docker"
ALLOW = {"GO-2026-4887": DOCKER}


def _called(osv: str, *, module: str = DOCKER, fixed: str | None = None) -> Finding:
    return Finding(osv=osv, module=module, function="ContainerList", fixed=fixed)


def _imported(osv: str, *, module: str = DOCKER) -> Finding:
    return Finding(osv=osv, module=module, function=None, fixed=None)


def test_parse_findings_reduces_stream_to_called_and_fixed() -> None:
    stream = (
        '{"config":{"go_version":"go1.26.4"}}\n'
        '{"finding":{"osv":"GO-1","trace":[{"module":"m","function":"F"}],'
        '"fixed_version":"v1.2.3"}}\n'
        '{"finding":{"osv":"GO-2","trace":[{"module":"m2"}]}}\n'
        '{"progress":{"message":"done"}}\n'
    )
    findings = parse_findings(stream)
    assert findings == [
        Finding(osv="GO-1", module="m", function="F", fixed="v1.2.3"),
        Finding(osv="GO-2", module="m2", function=None, fixed=None),
    ]


def test_allowlisted_unfixed_is_suppressed() -> None:
    report = classify([_called("GO-2026-4887")], ALLOW)
    assert report.ok
    assert any("allowlisted -- no fix" in line for line in report.lines)


def test_allowlisted_fails_once_fix_is_reported() -> None:
    report = classify([_called("GO-2026-4887", fixed="v28.6.0")], ALLOW)
    assert not report.ok
    assert any("FIX AVAILABLE" in line and "v28.6.0" in line for line in report.lines)


def test_fix_on_a_different_module_does_not_trigger() -> None:
    # A second finding for the same OSV reports a fix, but for a module the
    # allowlist entry is not pinned to -- suppression must hold.
    findings = [
        _called("GO-2026-4887"),
        Finding(osv="GO-2026-4887", module="other/mod", function="G", fixed="v9.9.9"),
    ]
    report = classify(findings, ALLOW)
    assert report.ok


def test_unexpected_called_vuln_fails() -> None:
    report = classify([_called("GO-2026-9999")], ALLOW)
    assert not report.ok
    assert any("UNEXPECTED" in line for line in report.lines)


def test_imported_but_not_called_is_ignored() -> None:
    report = classify([_imported("GO-2026-9999")], ALLOW)
    assert report.ok
    assert any("no called vulnerabilities" in line for line in report.lines)


def test_stale_allowlist_entry_is_flagged_but_not_fatal() -> None:
    report = classify([], ALLOW)
    assert report.ok
    assert any("no longer reported" in line for line in report.lines)


def test_allow_constant_entries_are_single_module_strings() -> None:
    assert govulncheck.ALLOW
    assert all(isinstance(mod, str) and mod for mod in govulncheck.ALLOW.values())

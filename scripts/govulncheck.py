#!/usr/bin/env python3
# ABOUTME: govulncheck wrapper applying a self-policing allowlist: suppresses
# ABOUTME: vulns reachable by our code but unfixable today, and auto-fails an
# ABOUTME: entry the moment govulncheck reports a fixed version for its module.
"""Run govulncheck and apply a self-policing allowlist.

Each allowlist entry pairs an OSV id with the module whose lack of a fixed
release justifies suppressing it. While govulncheck reports no ``fixed_version``
for that module, the called finding is suppressed. As soon as a fixed version
appears the entry fails the check -- so a suppression cannot silently outlive
the fix and we never have to re-poll the advisory by hand. Anything called but
not allowlisted always fails.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from dataclasses import dataclass

# OSV id -> module path whose missing fix justifies suppression. Keep entries
# single-module: the policy checks whether *this* module has a fixed_version.
#
# GO-2026-4887 (CVE-2026-34040): Moby AuthZ-plugin bypass via oversized bodies.
# GO-2026-4883 (CVE-2026-33997): off-by-one in legacy-plugin privilege check.
#   Both are Docker *daemon*-side flaws in code yoloAI never runs (we use
#   github.com/docker/docker only as an API client); they surface because the
#   advisories declare no affected symbols, so every import is flagged. No fixed
#   docker/docker release exists -- the fix lives in github.com/moby/moby/v2
#   (>= v2.0.0-beta.8), a separate module. These entries auto-fail if and when
#   docker/docker publishes a fixed version.
ALLOW: dict[str, str] = {
    "GO-2026-4887": "github.com/docker/docker",
    "GO-2026-4883": "github.com/docker/docker",
}


@dataclass(frozen=True)
class Finding:
    """One govulncheck finding, reduced to the fields the policy needs."""

    osv: str
    module: str  # vulnerable module (trace[0].module)
    function: str | None  # trace[0].function; set => our code calls it
    fixed: str | None  # fixed_version for `module`; None => no fix yet


@dataclass(frozen=True)
class Report:
    ok: bool
    lines: list[str]


def parse_findings(stdout: str) -> list[Finding]:
    """Extract findings from govulncheck's concatenated-JSON stdout."""
    findings: list[Finding] = []
    decoder = json.JSONDecoder()
    idx, end = 0, len(stdout)
    while idx < end:
        while idx < end and stdout[idx].isspace():
            idx += 1
        if idx >= end:
            break
        obj, idx = decoder.raw_decode(stdout, idx)
        finding = obj.get("finding") if isinstance(obj, dict) else None
        if not isinstance(finding, dict):
            continue
        trace = finding.get("trace") or []
        top = trace[0] if trace else {}
        findings.append(
            Finding(
                osv=str(finding.get("osv", "")),
                module=str(top.get("module", "")),
                function=top.get("function"),
                fixed=finding.get("fixed_version"),
            )
        )
    return findings


def _fixed_version_for(findings: list[Finding], osv: str, module: str) -> str | None:
    for f in findings:
        if f.osv == osv and f.module == module and f.fixed:
            return f.fixed
    return None


def classify(findings: list[Finding], allow: dict[str, str]) -> Report:
    """Apply the allowlist policy and return a pass/fail report."""
    called = sorted({f.osv for f in findings if f.function is not None})
    lines: list[str] = []
    unexpected: list[str] = []
    fixed_now: list[str] = []

    lines.append(
        "govulncheck: called vulnerabilities:"
        if called
        else "govulncheck: no called vulnerabilities."
    )
    for osv in called:
        module = allow.get(osv)
        if module is None:
            lines.append(f"  {osv}  (UNEXPECTED -- no allowlist entry)")
            unexpected.append(osv)
            continue
        fixed = _fixed_version_for(findings, osv, module)
        if fixed is not None:
            lines.append(
                f"  {osv}  (FIX AVAILABLE in {module} {fixed} -- "
                "remove from ALLOW and upgrade)"
            )
            fixed_now.append(osv)
        else:
            lines.append(f"  {osv}  (allowlisted -- no fix for {module} yet)")

    for osv in sorted(allow):
        if osv not in called:
            lines.append(
                f"  note: allowlist entry {osv} no longer reported -- "
                "remove it from ALLOW."
            )

    ok = not unexpected and not fixed_now
    if ok:
        lines.append(f"OK: {len(called)} called vuln(s), all allowlisted.")
    else:
        if unexpected:
            lines.append(f"FAIL: {len(unexpected)} non-allowlisted vulnerability(ies).")
        if fixed_now:
            lines.append(
                f"FAIL: {len(fixed_now)} allowlisted vuln(s) now have a fix; "
                "upgrade and drop them."
            )
    return Report(ok=ok, lines=lines)


def run_govulncheck() -> str:
    """Run govulncheck in JSON mode and return its stdout.

    govulncheck exits non-zero whenever vulns are found; that is expected -- we
    decide pass/fail ourselves from the findings. Only a hard tooling failure
    (no JSON at all) aborts.
    """
    toolchain = subprocess.run(
        ["go", "env", "GOVERSION"], capture_output=True, text=True, check=True
    ).stdout.strip()
    proc = subprocess.run(
        ["go", "run", "golang.org/x/vuln/cmd/govulncheck@latest", "-format=json", "./..."],
        capture_output=True,
        text=True,
        env={**os.environ, "GOTOOLCHAIN": toolchain},
    )
    if not proc.stdout.strip():
        sys.stderr.write(proc.stderr)
        raise SystemExit("govulncheck produced no output")
    return proc.stdout


def main() -> int:
    report = classify(parse_findings(run_govulncheck()), ALLOW)
    print("\n".join(report.lines))
    return 0 if report.ok else 1


if __name__ == "__main__":
    raise SystemExit(main())

# ABOUTME: Pins that every yoloai invocation in the smoke harness detaches stdin
# ABOUTME: from the controlling terminal, not just the main Test.run path, so a
# ABOUTME: concurrent exec cannot leave the host terminal in raw mode.

from __future__ import annotations

import ast
import subprocess
import types
from pathlib import Path
from typing import Any, Optional

import pytest

import smoke_test
from smoke_test import _capture_terminal_snapshot, _destroy_named_sandboxes


@pytest.fixture
def ctx(tmp_path: Path) -> smoke_test.RunContext:
    """A real RunContext, for the reason test_smoke_cleanup_resilience gives.

    A local stub would drift from the dataclass without anything noticing.
    """
    return smoke_test.RunContext(
        yoloai_bin="./yoloai",
        tmpdir=tmp_path,
        log_dir=tmp_path,
        run_id="test",
        fixture_dir=tmp_path,
    )


class _Recorder:
    """subprocess.run stand-in that records the kwargs of every call.

    Returns a result plausible enough for each caller to finish its happy path:
    returncode 0, bytes on stdout/stderr (these call sites use capture_output
    without text=, except _sandbox_status which sets text= and parses JSON).
    """

    def __init__(self, stdout: Any = b"") -> None:
        self.calls: list[dict[str, Any]] = []
        self._stdout = stdout

    def __call__(self, argv: list[str], **kwargs: Any) -> Any:
        self.calls.append(kwargs)
        return types.SimpleNamespace(returncode=0, stdout=self._stdout, stderr=b"")


def test_sandbox_status_detaches_stdin(
    monkeypatch: pytest.MonkeyPatch, ctx: smoke_test.RunContext
) -> None:
    """`sandbox info --json` must not inherit the harness's terminal.

    _sandbox_status swallows every exception and returns "unknown", so asserting
    on its return value would pass whether or not the call was even made. Assert
    on the recorded kwargs instead.
    """
    rec = _Recorder(stdout='{"status": "running"}')
    monkeypatch.setattr(subprocess, "run", rec)

    smoke_test.Test(ctx, "status-probe")._sandbox_status("sbx-1")

    assert len(rec.calls) == 1
    assert rec.calls[0].get("stdin") is subprocess.DEVNULL


def test_terminal_snapshot_detaches_stdin(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Both terminal-snapshot captures (plain and --ansi) must detach stdin."""
    rec = _Recorder(stdout=b"snapshot")
    monkeypatch.setattr(subprocess, "run", rec)

    wrote = _capture_terminal_snapshot("./yoloai", "sbx-1", tmp_path)

    assert wrote is True
    assert len(rec.calls) == 2, "expected the plain and --ansi captures"
    for kwargs in rec.calls:
        assert kwargs.get("stdin") is subprocess.DEVNULL


def test_destroy_named_sandboxes_detaches_stdin(
    monkeypatch: pytest.MonkeyPatch, ctx: smoke_test.RunContext
) -> None:
    """The mid-run destroy path must detach stdin.

    This is the sibling of the end-of-run cleanup and runs from a worker thread
    while other backends are still executing, so it is the one most able to
    collide with a concurrent `yoloai exec`.
    """
    rec = _Recorder()
    monkeypatch.setattr(subprocess, "run", rec)

    _destroy_named_sandboxes(ctx, ["sbx-1", "sbx-2"])

    assert len(rec.calls) == 2
    for kwargs in rec.calls:
        assert kwargs.get("stdin") is subprocess.DEVNULL


def test_test_run_detaches_stdin(monkeypatch: pytest.MonkeyPatch, ctx: smoke_test.RunContext) -> None:
    """Regression guard on the path that was already fixed.

    The other three call sites drifted from this one for two releases; pinning it
    here means a future edit cannot quietly un-fix the original.
    """
    rec = _Recorder(stdout="")
    monkeypatch.setattr(subprocess, "run", rec)

    smoke_test.Test(ctx, "runner").run("ls")

    assert len(rec.calls) == 1
    assert rec.calls[0].get("stdin") is subprocess.DEVNULL


# --------------------------------------------------------------------------
# Completeness guard
# --------------------------------------------------------------------------
#
# The four tests above pin the call sites that exist today. They cannot catch
# the actual failure mode here, which is a *new* yoloai call site added later
# without stdin= — exactly how the original fix came to cover one site out of
# ten. This walks the harness's own AST instead, so a fresh call site has to
# opt in rather than be remembered.


def _argv_is_yoloai(call: ast.Call, src: str, local_lists: dict[str, ast.AST]) -> bool:
    """True if this subprocess.run invokes the yoloai binary.

    Handles both the inline form (`subprocess.run([ctx.yoloai_bin, ...])`) and
    the via-variable form (`cmd = [ctx.yoloai_bin, ...]; subprocess.run(cmd)`),
    which _run_system_check and the warm-up path both use. Missing the second
    form is not hypothetical: a scan that only understood the inline form
    reported these two as clean while they were still inheriting stdin.
    """
    if not call.args:
        return False
    argv = call.args[0]
    if isinstance(argv, ast.Name):
        resolved = local_lists.get(argv.id)
        if resolved is None:
            return False
        argv = resolved  # type: ignore[assignment]
    seg = ast.get_source_segment(src, argv) or ""
    return "yoloai_bin" in seg


def _enclosing_local_lists(fn: ast.AST, src: str) -> dict[str, ast.AST]:
    """Map local name -> list literal assigned to it, within one function."""
    out: dict[str, ast.AST] = {}
    for node in ast.walk(fn):
        if isinstance(node, (ast.Assign, ast.AugAssign)):
            value = node.value
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            for t in targets:
                if isinstance(t, ast.Name) and isinstance(value, ast.List):
                    out.setdefault(t.id, value)
    return out


def test_every_yoloai_invocation_passes_stdin() -> None:
    """No subprocess.run of the yoloai binary may inherit the harness's stdin."""
    path = Path(smoke_test.__file__)
    src = path.read_text(encoding="utf-8")
    tree = ast.parse(src)

    offenders: list[str] = []
    checked = 0
    for fn in ast.walk(tree):
        if not isinstance(fn, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        local_lists = _enclosing_local_lists(fn, src)
        for node in ast.walk(fn):
            if not isinstance(node, ast.Call):
                continue
            f: Optional[ast.expr] = node.func
            if not (
                isinstance(f, ast.Attribute)
                and f.attr == "run"
                and isinstance(f.value, ast.Name)
                and f.value.id == "subprocess"
            ):
                continue
            if not _argv_is_yoloai(node, src, local_lists):
                continue
            checked += 1
            if "stdin" not in {k.arg for k in node.keywords}:
                offenders.append(f"{path.name}:{node.lineno}")

    assert checked >= 12, f"guard stopped finding call sites (found {checked}) — has the shape changed?"
    assert not offenders, (
        "these yoloai invocations inherit the harness's controlling terminal; "
        "pass stdin=subprocess.DEVNULL:\n  " + "\n  ".join(offenders)
    )

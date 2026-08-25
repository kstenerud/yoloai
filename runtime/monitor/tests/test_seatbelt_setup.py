# ABOUTME: Unit tests for SeatbeltBackend.setup() — the guest must be given a `StateDir`
# ABOUTME: at any arbitrary `state_rel_path` within the `HomeDir`
"""Informed by a Seatbelt setup bug that affected OpenCode,
in which the basename alone had been appended to the home path
(thus producing a symlink at "~/opencode" rather than at "~/.local/share/opencode",
with OpenCode unaware of the "~/opencode/auth.json" therein).
"""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from conftest import load_sandbox_setup

sandbox_setup = load_sandbox_setup()


def run_seatbelt_setup(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, state_rel_path: str
) -> tuple[Path, Path]:

    host_home = tmp_path / "host-home"
    (host_home / ".local" / "bin").mkdir(parents=True)

    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])

    backend = sandbox_setup.SeatbeltBackend(
        {
            "state_rel_path": state_rel_path,
        },
        str(yoloai_dir),
    )
    backend.setup()

    return yoloai_dir, yoloai_dir / "home"


@pytest.mark.parametrize(
    "state_rel_path",
    [".claude", ".local/share/opencode"],
    ids=["shallow", "nested"],
)
def test_state_dir_symlink_lands_at_full_rel_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, state_rel_path: str
) -> None:

    (
        yoloai_dir,
        guest_home,
    ) = run_seatbelt_setup(tmp_path, monkeypatch, state_rel_path)

    state_link = guest_home / state_rel_path
    assert (
        state_link.is_symlink()
    ), f"{state_rel_path} must be a symlink within the sandbox's home directory"
    assert (
        state_link.resolve() == (yoloai_dir / "agent-runtime").resolve()
    ), f"{state_rel_path} must resolve to the persistent storage directory for this sandbox (got: {state_link.resolve()})"

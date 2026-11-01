# ABOUTME: Unit tests for SeatbeltBackend.setup()
"""Informed by a couple of Seatbelt symlinking bugs that affected OpenCode and XDG-style config paths."""

from __future__ import annotations

from itertools import chain, combinations
import os
from pathlib import Path

import pytest

from conftest import load_sandbox_setup

sandbox_setup = load_sandbox_setup()


@pytest.mark.parametrize(
    "state_rel_path",
    [".claude", ".local/share/opencode"],
    ids=["shallow", "nested"],
)
def test__state_dir_symlink__targets_full_rel_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, state_rel_path: str
) -> None:
    """A deep `state_rel_path` like ".local/share/opencode"
    should indeed produce a symlink/target at "~/.local/share/opencode"
    rather than at "~/opencode".
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    state_dir = yoloai_dir / "agent-runtime"
    state_dir.mkdir(parents=True)

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend(
        {"state_rel_path": state_rel_path}, str(yoloai_dir)
    ).setup()

    assert (yoloai_dir / "home" / state_rel_path).is_symlink()
    assert (yoloai_dir / "home" / state_rel_path).resolve() == state_dir.resolve()


def test__legacy_configs_symlink__preserved(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Home-seed directories containing "~/.config" directories, on hosts without "~/.config/git",
    _previously_ resulted in the creation of a guest's "~/.config"
    as a _wholesale_ symlink to the read-only home-seed configs directory.
    In theory, such sandboxes could be improved
    by replacing their "~/.config" symlinks with directories containing independent symlinks,
    but we're leaving those sandboxes untouched for now.
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    host_git_dir = host_home / ".config" / "git"
    host_git_dir.mkdir(parents=True)
    (host_git_dir / "A").write_text("git config from host")

    seeded_configs = yoloai_dir / "home-seed" / ".config"
    # We probably only care about preserving legacy sandboxes
    # if their "~/.config" symlink sources still exist.
    (seeded_configs / "agent").mkdir(parents=True)
    (seeded_configs / "agent" / "C").write_text("agent config from home-seed")

    guest_home = yoloai_dir / "home"
    guest_home.mkdir(parents=True)
    os.symlink(seeded_configs, guest_home / ".config")

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert (guest_home / ".config").is_symlink()
    assert (guest_home / ".config").resolve() == seeded_configs
    assert not (
        guest_home / ".config" / "git"
    ).exists(), "nothing may be created through the legacy symlink (it points into the read-only tier)"


@pytest.mark.parametrize(
    "defined_XDG_configs",
    # Test every _non-empty_ (size >= 1) subset of XDG config types,
    # inspired by the `powerset` recipe at https://docs.python.org/3/library/itertools.html#itertools-recipes:~:text=powerset
    # (a list, not a set, so the numbered ids map to the same subsets on every run)
    chain.from_iterable(
        combinations(
            ["host_git_config", "seeded_agent_config", "pre-existing guest configs"],
            size,
        )
        for size in range(1, 3 + 1)
    ),
)
def test__XDG_configs__produce_symlinks_in_real_configs_directory(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    defined_XDG_configs: tuple[str, ...],
) -> None:
    """
    If necessitated by the presence of an XDG-style "~/.config/git" directory
    and (or) XDG-style agent-specific directories like "~/.config/opencode",
    the guest should be given a "~/.config" directory (not a symlink)
    whose direct children (e.g. "~/.config/git" and "~/.config/opencode")
    are independent, wholesale symlinks to the respective sources (e.g. a host dir and a home-seed dir respectively).
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    host_git_dir = host_home / ".config" / "git"
    if "host_git_config" in defined_XDG_configs:
        host_git_dir.mkdir(parents=True)
        (host_git_dir / "A").write_text("git config from host")

    seeded_agent_config = yoloai_dir / "home-seed" / ".config" / "agent"
    if "seeded_agent_config" in defined_XDG_configs:
        seeded_agent_config.mkdir(parents=True)
        (seeded_agent_config / "C").write_text("agent config from home-seed")

    guest_configs_dir = yoloai_dir / "home" / ".config"
    guest_dotfile = guest_configs_dir / ".dotfile"
    guest_dotdir = guest_configs_dir / ".dotdir"
    if "pre-existing guest configs" in defined_XDG_configs:
        guest_configs_dir.mkdir(parents=True)
        guest_dotfile.touch()
        guest_dotdir.mkdir()

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert (
        guest_configs_dir.is_dir() and not guest_configs_dir.is_symlink()
    ), "~/.config must **always** be a real directory"

    if "host_git_config" in defined_XDG_configs:
        git_config_link = guest_configs_dir / "git"
        assert git_config_link.is_symlink()
        assert git_config_link.resolve() == host_git_dir.resolve()
    if "seeded_agent_config" in defined_XDG_configs:
        agent_config_link = guest_configs_dir / "agent"
        assert agent_config_link.is_symlink()
        assert agent_config_link.resolve() == seeded_agent_config.resolve()
    if "pre-existing guest configs" in defined_XDG_configs:
        assert guest_dotfile.is_file()
        assert guest_dotdir.is_dir()


def test__non_XDG_configs__symlinked(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Non-XDG configs (direct children of "~" and generally dot-prefixed),
    including "~/.gitconfig" as well as agent-specific configs like "~/.opencode",
    should be created as _wholesale_ symlinks to individual directories
    on the host or in home-seed respectively.
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    agent_config_A = yoloai_dir / "home-seed" / ".agent.dotdir"
    agent_config_A.mkdir(parents=True)
    agent_config_B = yoloai_dir / "home-seed" / ".agent.dotfile"
    agent_config_B.touch()

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert (yoloai_dir / "home" / ".agent.dotdir").is_symlink()
    assert (yoloai_dir / "home" / ".agent.dotdir").resolve() == agent_config_A
    assert (yoloai_dir / "home" / ".agent.dotfile").is_symlink()
    assert (yoloai_dir / "home" / ".agent.dotfile").resolve() == agent_config_B


def test__gitconfig_and_git_dir__supported_independently(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A host's "~/.gitconfig" and "~/.config/git/config" should be supported at the same time."""

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    host_home.mkdir(parents=True)
    (host_home / ".gitconfig").write_text("non-XDG git config")
    (host_home / ".config" / "git").mkdir(parents=True)
    (host_home / ".config" / "git" / "config").write_text("XDG git config")

    guest_home = yoloai_dir / "home"

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert (guest_home / ".gitconfig").read_text() == "non-XDG git config"
    assert (guest_home / ".config" / "git" / "config").read_text() == "XDG git config"


@pytest.mark.parametrize(
    "git_config_rel_path",
    [".gitconfig", ".config/git"],
    ids=["non-XDG", "XDG"],
)
@pytest.mark.parametrize(
    "agent_config_rel_path",
    [".agent", ".config/agent"],
    ids=["non-XDG", "XDG"],
)
def test__host_git_config_and_seeded_agent_config__supported_independently(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    agent_config_rel_path: str,
    git_config_rel_path: str,
) -> None:
    """A host's git config and a home-seed's agent configs should be supported at the same time."""

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    # The non-XDG shape of git config is a single FILE (~/.gitconfig), not a
    # directory; the XDG shape is the ~/.config/git directory.
    host_git_config = host_home / git_config_rel_path
    if git_config_rel_path == ".gitconfig":
        host_git_config.parent.mkdir(parents=True)
        host_git_config.write_text("git config from host")
    elif git_config_rel_path == ".config/git":
        host_git_config.mkdir(parents=True)
        (host_git_config / "A").write_text("git config A, from host")
        (host_git_config / "B").write_text("git config B, from host")

    guest_git_config = yoloai_dir / "home" / git_config_rel_path

    seeded_agent_config = yoloai_dir / "home-seed" / agent_config_rel_path
    seeded_agent_config.mkdir(parents=True)
    (seeded_agent_config / "C").write_text("agent config C, seeded")
    (seeded_agent_config / "D").write_text("agent config D, seeded")

    guest_agent_config = yoloai_dir / "home" / agent_config_rel_path

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert guest_git_config.is_symlink()
    if git_config_rel_path == ".gitconfig":
        assert guest_git_config.read_text() == "git config from host"
    else:
        assert (guest_git_config / "A").read_text() == "git config A, from host"
        assert (guest_git_config / "B").read_text() == "git config B, from host"

    assert (guest_agent_config / "C").read_text() == "agent config C, seeded"
    assert (guest_agent_config / "D").read_text() == "agent config D, seeded"


@pytest.mark.parametrize(
    "agent_config_rel_path",
    [".agent", ".config/agent"],
    ids=["non-XDG", "XDG"],
)
def test__host_git_config__ignored_if_already_configured_in_guest_home(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    agent_config_rel_path: str,
) -> None:
    """A git config should not produce a symlink
    if it's shadowed by a pre-existing file or directory in the guest's home,
    and a git config directory's individual config files should not be merged into a pre-existing directory.

    Agent config directories should still be symlinked (assuming they're not shadowed).
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    host_gitconfig = host_home / ".gitconfig"
    host_gitconfig.parent.mkdir(parents=True)
    host_gitconfig.write_text("git config from host")

    guest_gitconfig = yoloai_dir / "home" / ".gitconfig"
    guest_gitconfig.parent.mkdir(parents=True)
    guest_gitconfig.write_text("git config, pre-existing in guest")

    seeded_agent_config = yoloai_dir / "home-seed" / agent_config_rel_path
    seeded_agent_config.mkdir(parents=True)
    (seeded_agent_config / "C").write_text("agent config C, seeded")
    (seeded_agent_config / "D").write_text("agent config D, seeded")

    guest_agent_config = yoloai_dir / "home" / agent_config_rel_path

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert guest_gitconfig.read_text() == "git config, pre-existing in guest"

    assert (guest_agent_config / "C").read_text() == "agent config C, seeded"
    assert (guest_agent_config / "D").read_text() == "agent config D, seeded"


@pytest.mark.parametrize(
    "agent_config_rel_path",
    [".agent", ".config/agent"],
    ids=["non-XDG", "XDG"],
)
def test__host_git_dir__ignored_if_already_configured_in_guest_home(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    agent_config_rel_path: str,
) -> None:
    """A git config directory should not produce a symlink
    if it's shadowed by a pre-existing directory in the guest's home,
    and individual git config files should not be merged into a pre-existing directory.

    Agent config directories should still be symlinked (assuming they're not shadowed).
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    host_git_dir = host_home / ".config" / "git"
    host_git_dir.mkdir(parents=True)
    (host_git_dir / "A").write_text("git config A, from host")
    (host_git_dir / "B").write_text("git config B, from host")

    guest_git_dir = yoloai_dir / "home" / ".config" / "git"
    guest_git_dir.mkdir(parents=True)
    (guest_git_dir / "A").write_text("git config A, pre-existing in guest")

    seeded_agent_config = yoloai_dir / "home-seed" / agent_config_rel_path
    seeded_agent_config.mkdir(parents=True)
    (seeded_agent_config / "C").write_text("agent config C, seeded")
    (seeded_agent_config / "D").write_text("agent config D, seeded")

    guest_agent_config = yoloai_dir / "home" / agent_config_rel_path

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert (guest_git_dir / "A").read_text() == "git config A, pre-existing in guest"
    assert not (
        guest_git_dir / "B"
    ).exists(), "New files should not be added to a pre-existing config directory"

    assert (guest_agent_config / "C").read_text() == "agent config C, seeded"
    assert (guest_agent_config / "D").read_text() == "agent config D, seeded"


@pytest.mark.parametrize(
    "git_config_rel_path",
    [".gitconfig", ".config/git"],
    ids=["non-XDG", "XDG"],
)
@pytest.mark.parametrize(
    "agent_config_rel_path",
    [".agent", ".config/agent"],
    ids=["non-XDG", "XDG"],
)
def test__seeded_agent_config__ignored_if_already_configured_in_guest_home(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    agent_config_rel_path: str,
    git_config_rel_path: str,
) -> None:
    """An agent-specific config directory (e.g. for OpenCode) should not be symlinked
    if it's shadowed by a pre-existing directory in the guest's home,
    and its individual config files should not be merged into the pre-existing directory.

    The git config should still be symlinked (assuming it's not shadowed).
    """

    host_home = tmp_path / "host-home"
    yoloai_dir = tmp_path / "yoloai"
    (yoloai_dir / "agent-runtime").mkdir(parents=True)

    host_git_config = host_home / git_config_rel_path
    if git_config_rel_path == ".gitconfig":
        host_home.mkdir(parents=True)
        host_git_config.write_text("git config from host")
    elif git_config_rel_path == ".config/git":
        host_git_config.mkdir(parents=True)
        (host_git_config / "A").write_text("git config A, from host")
        (host_git_config / "B").write_text("git config B, from host")

    guest_git_config = yoloai_dir / "home" / git_config_rel_path

    seeded_agent_config = yoloai_dir / "home-seed" / agent_config_rel_path
    seeded_agent_config.mkdir(parents=True)
    (seeded_agent_config / "C").write_text("agent config C, seeded")
    (seeded_agent_config / "D").write_text("agent config D, seeded")

    guest_agent_config = yoloai_dir / "home" / agent_config_rel_path
    guest_agent_config.mkdir(parents=True)
    (guest_agent_config / "C").write_text("agent config C, pre-existing in guest")

    monkeypatch.setenv("HOME", str(host_home))
    monkeypatch.setenv("PATH", os.environ["PATH"])
    sandbox_setup.SeatbeltBackend({"state_rel_path": ""}, str(yoloai_dir)).setup()

    assert guest_git_config.is_symlink()
    assert guest_git_config.resolve() == host_git_config

    assert (
        guest_agent_config / "C"
    ).read_text() == "agent config C, pre-existing in guest"
    assert not (
        guest_agent_config / "D"
    ).exists(), "New files should not be added to a pre-existing config directory"

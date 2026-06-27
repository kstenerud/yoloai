# ABOUTME: Unit tests for runtime/monitor/setup_helpers.py — the pure-function
# ABOUTME: surface extracted from sandbox-setup.py during W3 (architecture remediation).
"""Tests for setup_helpers.

These exercise the pure helpers extracted from sandbox-setup.py: schema
versioning, lifecycle preamble composition, prompt composition, and the
secret-file filtering logic. Anything that touches tmux/subprocess lives in
sandbox-setup.py and is out of scope for W3 (it's the W4 work).
"""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

import setup_helpers


# --- read_runtime_config ---


def test_read_runtime_config_returns_dict_when_version_matches(tmp_path: Path) -> None:
    cfg_path = tmp_path / "runtime-config.json"
    cfg_path.write_text(json.dumps({"schema_version": 1, "agent": "claude"}))
    cfg = setup_helpers.read_runtime_config(str(cfg_path), expected_schema_version=1)
    assert cfg["agent"] == "claude"


def test_read_runtime_config_tolerates_missing_version(tmp_path: Path) -> None:
    # Legacy files (written before W2) had no schema_version. They must still
    # parse so old sandboxes can be opened by a newer yoloai.
    cfg_path = tmp_path / "runtime-config.json"
    cfg_path.write_text(json.dumps({"agent": "claude"}))
    cfg = setup_helpers.read_runtime_config(str(cfg_path), expected_schema_version=1)
    assert cfg == {"agent": "claude"}


def test_read_runtime_config_rejects_version_mismatch(tmp_path: Path) -> None:
    cfg_path = tmp_path / "runtime-config.json"
    cfg_path.write_text(json.dumps({"schema_version": 99, "agent": "claude"}))
    with pytest.raises(RuntimeError, match="schema_version mismatch.*got 99.*expected 1"):
        setup_helpers.read_runtime_config(str(cfg_path), expected_schema_version=1)


# --- cmd_str ---


def test_cmd_str_string_form() -> None:
    assert setup_helpers.cmd_str({"type": "string", "cmd": "echo hi"}) == "echo hi"


def test_cmd_str_array_form_joins_with_spaces() -> None:
    assert setup_helpers.cmd_str({"type": "array", "cmd": ["echo", "hi", "there"]}) == "echo hi there"


def test_cmd_str_object_form_shows_keys_and_values() -> None:
    result = setup_helpers.cmd_str({"type": "object", "cmd": {"build": "make", "lint": "ruff"}})
    # Dict iteration order is insertion order on 3.7+, so the output is
    # deterministic. Assert both substrings rather than the full string to
    # stay robust if the formatting tweaks later.
    assert result.startswith("{") and result.endswith("}")
    assert "build: make" in result
    assert "lint: ruff" in result


def test_cmd_str_unknown_type_falls_back_to_str_cmd() -> None:
    # Defensive fallback: an entry with an unrecognized "type" still produces
    # *some* string rather than crashing the preamble builder.
    assert setup_helpers.cmd_str({"type": "weird", "cmd": "payload"}) == "payload"


# --- lifecycle_preamble ---


def test_lifecycle_preamble_empty_when_no_lifecycle(tmp_path: Path) -> None:
    assert setup_helpers.lifecycle_preamble({}, str(tmp_path)) == ""


def test_lifecycle_preamble_lists_pending_on_create_and_on_start(tmp_path: Path) -> None:
    cfg = {
        "lifecycle": {
            "on_create": [{"type": "string", "cmd": "pip install -e ."}],
            "on_start": [{"type": "array", "cmd": ["redis-server", "--daemonize", "yes"]}],
        }
    }
    out = setup_helpers.lifecycle_preamble(cfg, str(tmp_path))
    assert "onCreateCommand: pip install -e ." in out
    assert "postStartCommand: redis-server --daemonize yes" in out
    assert "running in the background" in out


def test_lifecycle_preamble_skips_on_create_when_marker_exists(tmp_path: Path) -> None:
    # The on-create marker is what tells us setup commands have already
    # executed in a previous container start. If it's there, the preamble
    # should only mention on-start commands.
    (tmp_path / "lifecycle-on-create-done").write_text("")
    cfg = {
        "lifecycle": {
            "on_create": [{"type": "string", "cmd": "pip install -e ."}],
            "on_start": [{"type": "string", "cmd": "echo ready"}],
        }
    }
    out = setup_helpers.lifecycle_preamble(cfg, str(tmp_path))
    assert "onCreateCommand" not in out
    assert "postStartCommand: echo ready" in out


def test_lifecycle_preamble_skips_on_create_when_cfg_flag_set(tmp_path: Path) -> None:
    cfg = {
        "lifecycle": {
            "on_create_done": True,
            "on_create": [{"type": "string", "cmd": "pip install -e ."}],
            "on_start": [],
        }
    }
    assert setup_helpers.lifecycle_preamble(cfg, str(tmp_path)) == ""


# --- lifecycle_on_create_marker / should_run_on_create ---


def test_lifecycle_on_create_marker_path(tmp_path: Path) -> None:
    # The runner writes this file and both the runner and the preamble read it;
    # the literal must stay identical, hence the single helper.
    assert setup_helpers.lifecycle_on_create_marker(str(tmp_path)) == str(
        tmp_path / "lifecycle-on-create-done"
    )


def test_should_run_on_create_first_start() -> None:
    # No prior marker, flag unset → on-create runs.
    assert setup_helpers.should_run_on_create({}, marker_exists=False) is True


def test_should_run_on_create_skips_when_marker_exists() -> None:
    assert setup_helpers.should_run_on_create({}, marker_exists=True) is False


def test_should_run_on_create_skips_when_cfg_flag_set() -> None:
    # on_create_done is stamped at create time (e.g. a clone that already ran
    # on-create on the source); honor it even without a marker file.
    assert setup_helpers.should_run_on_create({"on_create_done": True}, marker_exists=False) is False


# --- compose_prompt_content ---


def test_compose_prompt_content_preamble_only() -> None:
    assert setup_helpers.compose_prompt_content("[yoloai] setup running", None) == "[yoloai] setup running"


def test_compose_prompt_content_prompt_only() -> None:
    assert setup_helpers.compose_prompt_content(None, "user task") == "user task"


def test_compose_prompt_content_both_joined_with_blank_line() -> None:
    # Two newlines so the agent sees them as separate paragraphs — the
    # preamble shouldn't blur into the user's prompt.
    assert (
        setup_helpers.compose_prompt_content("preamble", "user task")
        == "preamble\n\nuser task"
    )


def test_compose_prompt_content_none_when_nothing() -> None:
    # Letting callers short-circuit the tmux paste-buffer roundtrip when
    # there's nothing to deliver.
    assert setup_helpers.compose_prompt_content(None, None) is None
    assert setup_helpers.compose_prompt_content("", "") is None


# --- load_secret_files ---


def test_load_secret_files_returns_empty_when_dir_missing(tmp_path: Path) -> None:
    missing = tmp_path / "does-not-exist"
    assert setup_helpers.load_secret_files(str(missing)) == {}


def test_load_secret_files_reads_each_file_into_dict(tmp_path: Path) -> None:
    (tmp_path / "ANTHROPIC_API_KEY").write_text("sk-ant-test")
    (tmp_path / "GITHUB_TOKEN").write_text("ghp_test")
    assert setup_helpers.load_secret_files(str(tmp_path)) == {
        "ANTHROPIC_API_KEY": "sk-ant-test",
        "GITHUB_TOKEN": "ghp_test",
    }


def test_load_secret_files_skips_subdirectories(tmp_path: Path) -> None:
    # The Docker mount sometimes contains directories (e.g. when Docker
    # bind-mounts a directory rather than a file). Those must be skipped,
    # not opened as if they were secret files.
    (tmp_path / "TOKEN").write_text("real-secret")
    (tmp_path / "subdir").mkdir()
    (tmp_path / "subdir" / "nested").write_text("nested")
    secrets = setup_helpers.load_secret_files(str(tmp_path))
    assert secrets == {"TOKEN": "real-secret"}


def test_load_secret_files_preserves_exact_contents(tmp_path: Path) -> None:
    # No trailing-newline stripping: the agent process receives the bytes
    # verbatim so any whitespace the user put in the file is preserved.
    (tmp_path / "MULTILINE").write_text("line1\nline2\n")
    assert setup_helpers.load_secret_files(str(tmp_path)) == {"MULTILINE": "line1\nline2\n"}


# --- build_secret_exports ---


def test_build_secret_exports_empty_for_no_secrets() -> None:
    assert setup_helpers.build_secret_exports(None) == ""
    assert setup_helpers.build_secret_exports({}) == ""


def test_build_secret_exports_emits_export_per_secret() -> None:
    out = setup_helpers.build_secret_exports({"A": "1", "B": "2"})
    assert out == "export A='1'; export B='2'; "


def test_build_secret_exports_escapes_single_quotes() -> None:
    # A value containing a single quote must not break out of the surrounding
    # single-quoted shell literal — this is the injection-class bug the carve
    # exists to pin down.
    out = setup_helpers.build_secret_exports({"K": "a'b"})
    assert out == "export K='a'\\''b'; "


# --- build_agent_launch_command ---


def test_build_agent_launch_command_no_workdir_no_secrets() -> None:
    assert (
        setup_helpers.build_agent_launch_command("claude --foo", None, None)
        == "exec claude --foo"
    )


def test_build_agent_launch_command_cds_into_quoted_workdir() -> None:
    # working_dir is single-quoted so paths with spaces (e.g. Tart VirtioFS)
    # survive the shell.
    assert (
        setup_helpers.build_agent_launch_command("claude", "/work space", None)
        == "cd '/work space' && exec claude"
    )


def test_build_agent_launch_command_prepends_secret_exports() -> None:
    out = setup_helpers.build_agent_launch_command("claude", "/w", {"T": "x"})
    assert out == "export T='x'; cd '/w' && exec claude"


def test_build_agent_launch_command_wraps_for_fall_to_shell() -> None:
    # With a wrapper set (D96 fall-to-shell), the agent runs as an argument to the
    # wrapper instead of being exec'd directly — the wrapper becomes the pane's
    # process and the agent its child.
    out = setup_helpers.build_agent_launch_command(
        "claude --foo", "/w", None, wrapper="/yoloai/bin/agent-run.sh")
    assert out == "cd '/w' && exec /yoloai/bin/agent-run.sh claude --foo"


def test_build_agent_launch_command_wrapper_no_workdir() -> None:
    out = setup_helpers.build_agent_launch_command(
        "claude", None, None, wrapper="/yoloai/bin/agent-run.sh")
    assert out == "exec /yoloai/bin/agent-run.sh claude"


def test_build_agent_launch_command_applies_launch_prefix() -> None:
    # The W1a launch prefix (e.g. Tart's PATH=...) is prepended verbatim, ahead
    # of the secret exports and cd.
    out = setup_helpers.build_agent_launch_command(
        "claude", "/w", {"T": "x"}, launch_prefix='PATH="/opt/bin:$PATH" ')
    assert out == "PATH=\"/opt/bin:$PATH\" export T='x'; cd '/w' && exec claude"


def test_dockerd_storage_args_overlay_backing_uses_fuse() -> None:
    # No real-fs volume (backing is the overlay rootfs): overlay2 can't nest, so
    # force fuse-overlayfs as the fallback driver.
    assert setup_helpers.dockerd_storage_args("overlay") == ["--storage-driver=fuse-overlayfs"]
    assert setup_helpers.dockerd_storage_args("  overlay  ") == ["--storage-driver=fuse-overlayfs"]


def test_dockerd_storage_args_realfs_backing_auto_selects() -> None:
    # Real-fs volume present: return no flag and let dockerd auto-select the
    # native overlay driver (fast, exec-safe on every provider).
    for fs in ("ext4", "xfs", "btrfs", ""):
        assert setup_helpers.dockerd_storage_args(fs) == []

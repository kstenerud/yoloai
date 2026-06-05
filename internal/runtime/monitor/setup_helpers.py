# ABOUTME: Pure-function helpers extracted from sandbox-setup.py for unit testing.
# ABOUTME: Type-annotated for mypy --strict; no logging, tmux, or subprocess calls.
"""Pure helpers used by sandbox-setup.py.

Functions here have no implicit I/O coupling: they read files only when given
a path, never touch tmux/subprocess, and never mutate process environment.
That makes them straightforward to unit-test without fakes.

W3 of the architecture remediation plan extracted these from the dirtier
sandbox-setup.py wrappers so they can be type-checked under mypy --strict
and covered by pytest.
"""

from __future__ import annotations

import json
import os
from typing import Any


# RUNTIME_CONFIG_SCHEMA_VERSION must equal the runtimeConfigSchemaVersion
# constant in sandbox/create.go. Bumped together by W2 (architecture
# remediation plan) when the runtime-config.json contract changes in a
# non-additive way.
RUNTIME_CONFIG_SCHEMA_VERSION: int = 1


def read_runtime_config(path: str, expected_schema_version: int = RUNTIME_CONFIG_SCHEMA_VERSION) -> dict[str, Any]:
    """Read runtime-config.json and validate schema_version.

    A missing schema_version is tolerated (legacy file written before W2).
    A present-but-mismatching value raises RuntimeError with a specific
    message — silently parsing a newer/older shape risks misinterpreting
    fields.
    """
    with open(path) as f:
        cfg: dict[str, Any] = json.load(f)
    got = cfg.get("schema_version")
    if got is not None and got != expected_schema_version:
        raise RuntimeError(
            f"schema_version mismatch in {path}: got {got}, "
            f"expected {expected_schema_version} "
            f"(runtime-config.json was written by an incompatible yoloai version; "
            f"re-create the sandbox)"
        )
    return cfg


def cmd_str(entry: dict[str, Any]) -> str:
    """Return a human-readable string for a lifecycle command entry.

    Entry kinds: "string" (cmd is a shell string), "array" (cmd is argv list),
    "object" (cmd is a dict of name → shell-string for parallel execution).
    """
    kind = entry.get("type", "")
    cmd = entry.get("cmd")
    if kind == "string":
        return str(cmd)
    if kind == "array" and isinstance(cmd, list):
        return " ".join(str(c) for c in cmd)
    if kind == "object" and isinstance(cmd, dict):
        return "{" + ", ".join(f"{k}: {v}" for k, v in cmd.items()) + "}"
    return str(cmd)


def lifecycle_on_create_marker(yoloai_dir: str) -> str:
    """Path of the marker recording that on-create lifecycle commands have run.

    Single source of truth for the filename, shared by the preamble (which
    decides whether to advertise on-create commands as pending) and the runner
    (which writes it after on-create succeeds). Keeping the literal here prevents
    the two readers from drifting apart.
    """
    return os.path.join(yoloai_dir, "lifecycle-on-create-done")


def should_run_on_create(lifecycle: dict[str, Any], marker_exists: bool) -> bool:
    """Whether on-create lifecycle commands should run on this start.

    They run once per sandbox: skipped when the cfg flag ``on_create_done`` is
    set (recorded at create time) or the marker file already exists (a prior
    start ran them). Centralizes the gating that both the preamble and the runner
    depend on, so they can't disagree — the on-create-runs-twice / never bug
    class.
    """
    return not lifecycle.get("on_create_done", False) and not marker_exists


def lifecycle_preamble(cfg: dict[str, Any], yoloai_dir: str) -> str:
    """Build the preamble describing lifecycle commands running in the background.

    Returns the empty string if no lifecycle commands are pending; otherwise
    returns the multi-line notice that gets prepended to the user prompt.

    The "on-create" commands are advertised only when :func:`should_run_on_create`
    says they'll run this start.
    """
    lifecycle = cfg.get("lifecycle")
    if not lifecycle:
        return ""

    marker_exists = os.path.exists(lifecycle_on_create_marker(yoloai_dir))

    pending: list[str] = []
    if should_run_on_create(lifecycle, marker_exists):
        for entry in lifecycle.get("on_create", []):
            pending.append(f"  onCreateCommand: {cmd_str(entry)}")
    for entry in lifecycle.get("on_start", []):
        pending.append(f"  postStartCommand: {cmd_str(entry)}")

    if not pending:
        return ""

    return (
        "[yoloai] The following setup commands are running in the background:\n"
        + "\n".join(pending)
        + "\nSome services may not be ready yet. You can start working now — "
        "a notification will appear in this session when setup is complete."
    )


def compose_prompt_content(preamble: str | None, prompt_text: str | None) -> str | None:
    """Compose the content delivered to the agent's tmux pane.

    Returns the joined `preamble\\n\\nprompt_text` when both are present, or
    just whichever is present. Returns None when both are absent so callers
    can short-circuit without doing a tmux paste.
    """
    parts: list[str] = []
    if preamble:
        parts.append(preamble)
    if prompt_text:
        parts.append(prompt_text)
    if not parts:
        return None
    return "\n\n".join(parts)


def build_secret_exports(secrets: dict[str, str] | None) -> str:
    """Build a POSIX-sh ``export NAME='value'; `` prefix for the given secrets.

    Single quotes in values are escaped as ``'\\''`` so the export is safe to
    embed in a shell command. Returns ``""`` when there are no secrets.

    tmux ``set-environment`` doesn't reach shells that are already running, so
    the agent launch command must export secrets inline — this builds that
    prefix. Pure: it composes a string and does not touch ``os.environ``.
    """
    if not secrets:
        return ""
    parts: list[str] = []
    for name, value in secrets.items():
        escaped = value.replace("'", "'\\''")
        parts.append(f"export {name}='{escaped}'; ")
    return "".join(parts)


def build_agent_launch_command(
    agent_command: str,
    working_dir: str | None,
    secrets: dict[str, str] | None,
    launch_prefix: str = "",
) -> str:
    """Compose the shell command sent to the agent's tmux pane.

    Prepends the secret exports (see :func:`build_secret_exports`), ``cd``s into
    ``working_dir`` (single-quoted, to tolerate spaces) when set, ``exec``s
    ``agent_command`` (so the agent replaces the shell and its exit code becomes
    the pane's), and prepends ``launch_prefix`` (e.g. a ``PATH=... `` prefix a
    backend needs). Pure string composition — no tmux, no subprocess.
    """
    exports = build_secret_exports(secrets)
    if working_dir:
        base = f"{exports}cd '{working_dir}' && exec {agent_command}"
    else:
        base = f"{exports}exec {agent_command}"
    return launch_prefix + base


def load_secret_files(secrets_dir: str) -> dict[str, str]:
    """Read secret files from a directory into a {name: value} mapping.

    Subdirectories and unreadable files are skipped silently. A missing or
    non-directory `secrets_dir` returns an empty dict. The caller is
    responsible for mutating `os.environ` and propagating to tmux — this
    helper does neither, so it stays pure and testable.
    """
    secrets: dict[str, str] = {}
    if not os.path.isdir(secrets_dir):
        return secrets
    try:
        names = os.listdir(secrets_dir)
    except OSError:
        return secrets
    for name in names:
        path = os.path.join(secrets_dir, name)
        if not os.path.isfile(path):
            continue
        try:
            with open(path) as f:
                secrets[name] = f.read()
        except OSError:
            continue
    return secrets


def dockerd_storage_args(var_lib_docker_fstype: str) -> list[str]:
    """Pick the nested dockerd `--storage-driver` args from the /var/lib/docker
    backing filesystem type.

    yoloAI mounts a real-filesystem volume at /var/lib/docker for privileged
    sandboxes, so the backing is normally ext4/xfs/btrfs and the native overlay
    driver auto-selects — return no flag and let dockerd choose it (fast,
    low-disk, exec-safe on every provider).

    Only when the backing is still `overlay` (no volume — overlay2 can't nest on
    overlay) do we force fuse-overlayfs. That works on Linux/OrbStack/Podman
    Machine; Docker Desktop's LinuxKit kernel can't exec off fuse-overlayfs, but
    that path shouldn't be hit because yoloAI always provides the volume. See
    docs/contributors/design/research/dind-storage-drivers.md.
    """
    if var_lib_docker_fstype.strip() == "overlay":
        return ["--storage-driver=fuse-overlayfs"]
    return []

#!/usr/bin/env python3
# ABOUTME: Consolidated sandbox setup for every backend, run inside the sandbox.
# ABOUTME: Replaces the old per-backend shell entrypoints; dispatches on a CLI arg.
"""Consolidated sandbox setup script for all yoloAI backends.

Replaces the per-backend shell entrypoint scripts (entrypoint-user.sh for
Docker, entrypoint.sh for seatbelt, setup.sh for Tart) with a single Python
implementation. Backend-specific setup is dispatched by a CLI argument.

Usage:
    sandbox-setup.py docker                  # Docker (YOLOAI_DIR from env)
    sandbox-setup.py seatbelt <sandbox-dir>  # Seatbelt
    sandbox-setup.py tart <shared-dir>       # Tart
"""

from __future__ import annotations

import datetime
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path
from typing import Any, Callable, TextIO, cast

from setup_helpers import (
    build_agent_launch_command,
    compose_prompt_content,
    dockerd_storage_args,
    lifecycle_on_create_marker,
    lifecycle_preamble,
    load_secret_files,
    read_runtime_config,
    should_run_on_create,
)
import tmux_io
from tmux_io import set_title, tmux, tmux_output


# --- JSONL logger ---

_sandbox_log: TextIO | None = None


def _init_sandbox_log(yoloai_dir: str) -> None:
    global _sandbox_log
    log_path = os.path.join(yoloai_dir, "logs", "sandbox.jsonl")
    try:
        _sandbox_log = open(log_path, "a", buffering=1)  # noqa: WPS515 # line-buffered
    except OSError as e:
        print(f"[sandbox-setup] warning: cannot open log: {e}", file=sys.stderr)


def _log(level: str, event: str, msg: str, **fields: Any) -> None:
    now = datetime.datetime.now(datetime.timezone.utc)
    ts = now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
    entry: dict[str, Any] = {"ts": ts, "level": level, "event": event, "msg": msg}
    entry.update(fields)
    if _sandbox_log:
        try:
            _sandbox_log.write(json.dumps(entry) + "\n")
            _sandbox_log.flush()
        except OSError:
            pass


def log_info(event: str, msg: str, **fields: Any) -> None:
    _log("info", event, msg, **fields)


def log_debug(event: str, msg: str, **fields: Any) -> None:
    if DEBUG:
        _log("debug", event, msg, **fields)


def log_error(event: str, msg: str, **fields: Any) -> None:
    _log("error", event, msg, **fields)


# --- Utility functions ---


def read_config(path: str) -> dict[str, Any]:
    """Read and return the runtime-config.json as a dict."""
    return read_runtime_config(path)


def read_secrets(secrets_dir: str, socket: str | None = None) -> dict[str, str]:
    """Read secret files from a directory into os.environ and tmux environment.

    For Docker, entrypoint.py reads secrets and execs into sandbox-setup.py,
    so the environment is inherited. For Tart/Seatbelt, sandbox-setup.py runs
    directly, so secrets must be explicitly passed to tmux via set-environment
    and also exported in the agent launch command (since tmux set-environment
    doesn't propagate to shells that are already running).

    Returns a dict of {name: value} for all loaded secrets.
    """
    log_info("read_secrets.check", f"checking secrets_dir={secrets_dir}")
    if not os.path.isdir(secrets_dir):
        log_info("read_secrets.not_dir", f"secrets_dir is not a directory: {secrets_dir}")
        return {}
    secrets = load_secret_files(secrets_dir)
    log_info("read_secrets.done", f"loaded {len(secrets)} secrets from {secrets_dir}")
    for name, value in secrets.items():
        os.environ[name] = value
        # Also set in tmux so the agent shell session inherits it.
        if socket:
            tmux("set-environment", "-t", "main", name, value, socket=socket)
    return secrets


def read_secrets_from_env(socket: str | None = None) -> dict[str, str]:
    """Build the secrets dict from the env vars named in YOLOAI_SECRET_KEYS.

    The values are already in os.environ (delivered via the launched process's
    environment); this collects the named subset and mirrors them into the tmux
    session environment so the agent pane inherits them, exactly as the
    file-based read_secrets does.
    """
    names = [n for n in os.environ.get("YOLOAI_SECRET_KEYS", "").split(",") if n]
    secrets = {n: os.environ[n] for n in names if n in os.environ}
    # Drop the yoloai-internal sentinel so it does not leak into the tmux server's
    # inherited environment (the tmux session is created after this runs) and thus
    # the agent's panes. The secret values themselves stay in os.environ — the
    # agent needs them — but this plumbing var is not the agent's business.
    os.environ.pop("YOLOAI_SECRET_KEYS", None)
    log_info("read_secrets_from_env.done", f"loaded {len(secrets)} secrets from env")
    if socket:
        for name, value in secrets.items():
            tmux("set-environment", "-t", "main", name, value, socket=socket)
    return secrets


def signal_secrets_consumed(yoloai_dir: str) -> None:
    """Touch a host-visible marker after secrets have been read.

    The host (buildAndStart) waits for this marker before removing the
    ephemeral secrets temp dir, so a slow-booting VM backend can't have
    the dir removed before it reads the credentials. For Docker/containerd
    entrypoint.py already writes this earlier; this covers the Tart/Seatbelt
    path where sandbox-setup.py is the secrets reader.

    Written under logs/ because only /yoloai subdirs are bind-mounted to
    the host (the /yoloai root is not). Must match store.SecretsConsumedMarker.
    """
    marker = os.path.join(yoloai_dir, "logs", ".secrets-consumed")
    try:
        with open(marker, "w") as f:
            f.write("1")
    except OSError as e:
        log_info("secrets.consumed_error", f"cannot write secrets-consumed marker: {e}")


# AGENT_STATUS_SCHEMA_VERSION must equal agentStatusSchemaVersion in
# internal/orchestrator/status/status.go. The cross-language fence in
# internal/orchestrator/status/schema_version_test.go asserts the agreement.
AGENT_STATUS_SCHEMA_VERSION = 1


def write_status(status_file: str, status: str, exit_code: int | None = None) -> None:
    """Write agent-status.json."""
    data: dict[str, Any] = {
        "schema_version": AGENT_STATUS_SCHEMA_VERSION,
        "status": status,
        "exit_code": exit_code,
        "timestamp": int(time.time()),
    }
    with open(status_file, "w") as f:
        json.dump(data, f)
        f.write("\n")


# --- Backend abstraction ---
#
# Similar to Go's runtime.Register() pattern, each backend handles platform-specific
# setup, secrets, paths, etc. Backends are selected by name at runtime.

from abc import ABC, abstractmethod


def _prepend_macos_toolchain_path(leading_dirs: list[str] | None = None) -> None:
    """Prepend the macOS host toolchain dirs to PATH so host-run agents find node
    and Homebrew-installed CLIs. Used by the backends whose agent runs on the host
    (Tart in-VM, Seatbelt on the host). node@22 is keg-only, so /opt/homebrew/bin
    alone is NOT enough — the keg's own bin dir must be added, which is exactly the
    dir a non-interactive shell (e.g. `make smoketest`) usually lacks. leading_dirs
    (e.g. mounted Xcode tool dirs) go first. Idempotent: only dirs not already on
    PATH are added, order preserved."""
    toolchain_bins = list(leading_dirs or []) + [
        os.path.expanduser("~/.local/bin"),
        "/opt/homebrew/opt/node@22/bin",  # keg-only node@22 (not linked into /opt/homebrew/bin)
        "/opt/homebrew/bin",
        "/opt/homebrew/sbin",
        "/usr/local/bin",
    ]
    current_path = os.environ.get("PATH", "")
    extras = [p for p in toolchain_bins if p not in current_path.split(":")]
    if extras:
        os.environ["PATH"] = ":".join(extras) + ":" + current_path
        log_debug("path_augment", "prepended macOS toolchain dirs", added=":".join(extras))


def _discover_node_bin_dir() -> str | None:
    """Best-effort discovery of the host's node bin dir for host-run agents.

    node may live where a non-interactive `make smoketest` shell can't see it —
    nvm (~/.nvm/versions/node/...), a version manager, or any node exported only
    from a login shell. The fixed-dir list in _prepend_macos_toolchain_path()
    covers the Homebrew cases; this covers the rest by asking a login shell to
    resolve node, mirroring how the seatbelt git path resolves the real git via
    `xcrun -f git` (see runtime/seatbelt resolveGitBinary). Returns the bin dir
    to prepend, or None when node is already on PATH or can't be found — a missing
    node is NOT fatal, since the native Claude Code install bundles its own
    runtime and needs no system node."""
    if shutil.which("node"):
        return None  # already resolvable on PATH; nothing to prepend
    shell = os.environ.get("SHELL") or "/bin/zsh"
    try:
        proc = subprocess.run(
            [shell, "-lc", "command -v node"],
            capture_output=True, text=True, timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    lines = [ln.strip() for ln in proc.stdout.splitlines() if ln.strip()]
    node_path = lines[-1] if lines else ""
    if node_path and os.path.isfile(node_path) and os.access(node_path, os.X_OK):
        node_dir = os.path.dirname(node_path)
        log_debug("node_discover", "resolved node via login shell", node_dir=node_dir)
        return node_dir
    return None


class Backend(ABC):
    """Abstract base class for sandbox backends."""

    # Whether this backend stages secrets in a host-side dir whose deletion the
    # host gates on the .secrets-consumed marker. True for the file-staging
    # backends (Tart/Seatbelt); DockerBackend overrides to False because it now
    # receives secrets via the launch env, so there is no staged dir to release.
    writes_consumed_marker = True

    def __init__(self, cfg: dict[str, Any], yoloai_dir: str) -> None:
        self.cfg = cfg
        self.yoloai_dir = yoloai_dir

    @abstractmethod
    def setup(self) -> None:
        """Run backend-specific setup (mount symlinks, overlays, etc.)."""
        pass

    @abstractmethod
    def get_tmux_socket(self) -> str | None:
        """Return the tmux socket path, or None for default."""
        pass

    @abstractmethod
    def get_working_dir(self) -> str | None:
        """Return the working directory path, or None if not needed."""
        pass

    @abstractmethod
    def prepare_environment(self) -> None:
        """Set up environment variables before launching the agent."""
        pass

    @abstractmethod
    def read_secrets(self, socket: str | None) -> dict[str, str]:
        """Read secrets and make them available to the agent.

        Returns a dict of {name: value} for all loaded secrets.
        """
        pass


class DockerBackend(Backend):
    """Backend for Docker and Podman containers."""

    # Secrets arrive via the launch env (ProcSpec.Env), not a host-staged dir,
    # so there is nothing for the host to release — the consumed-marker is moot.
    writes_consumed_marker = False

    def setup(self) -> None:
        """Docker-specific setup: the auto-commit loop for :copy directories."""
        log_info("sandbox.backend_setup", "Docker backend setup", backend="docker")

        # Auto-commit loop for :copy directories
        auto_commit_interval = int(self.cfg.get("auto_commit_interval", 0))
        copy_dirs = self.cfg.get("copy_dirs", [])

        if auto_commit_interval > 0 and copy_dirs:
            log_debug("auto_commit.start", f"starting auto-commit loop (interval={auto_commit_interval}s, dirs={len(copy_dirs)})")

            def _auto_commit() -> None:
                while True:
                    time.sleep(auto_commit_interval)
                    timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                    for d in copy_dirs:
                        tmux_io.run(["git", "-C", d, "add", "-A"], capture_output=True)
                        tmux_io.run(
                            ["git", "-C", d, "commit", "-m", f"yoloai auto-commit {timestamp}", "--no-gpg-sign"],
                            capture_output=True,
                        )

            import threading
            t = threading.Thread(target=_auto_commit, daemon=True)
            t.start()

    def get_tmux_socket(self) -> str | None:
        """Docker uses a fixed socket path from config (for gVisor compatibility)."""
        return self.cfg.get("tmux_socket") or None

    def get_working_dir(self) -> str | None:
        """Docker doesn't need explicit cd - containers start at the right path."""
        return None

    def prepare_environment(self) -> None:
        """Docker environment is already prepared by entrypoint.py."""
        pass

    def read_secrets(self, socket: str | None) -> dict[str, str]:
        """Read secrets from env vars named in YOLOAI_SECRET_KEYS.

        On the Launch path secrets arrive in the process environment (ProcSpec.Env);
        YOLOAI_SECRET_KEYS names which env vars are secrets. Values are already in
        os.environ — this collects them and mirrors into the tmux session environment
        so the agent pane inherits them.
        """
        return read_secrets_from_env(socket=socket)


class TartBackend(Backend):
    """Backend for Tart macOS VMs with VirtioFS mounts."""

    def setup(self) -> None:
        """Tart-specific setup: create VirtioFS mount symlinks via sudo."""
        log_info("sandbox.backend_setup", "Tart backend setup", backend="tart")

        # Create symlinks for user-specified mounts
        mount_map = self.cfg.get("mount_map", {})
        if mount_map:
            log_debug("tart.symlinks", "creating VirtioFS mount symlinks")
            for target, source in mount_map.items():
                parent = os.path.dirname(target)
                log_debug("tart.symlink.mkdir", "creating parent dir", target=target)
                tmux_io.run(["sudo", "mkdir", "-p", parent], capture_output=True)

                # Remove existing symlink or empty directory
                if os.path.islink(target):
                    log_debug("tart.symlink.rm", "removing existing symlink", target=target)
                    tmux_io.run(["sudo", "rm", "-f", target], capture_output=True)
                elif os.path.isdir(target):
                    # Check if empty
                    try:
                        if not os.listdir(target):
                            log_debug("tart.symlink.rmdir", "removing empty dir", target=target)
                            tmux_io.run(["sudo", "rmdir", target], capture_output=True)
                    except OSError:
                        pass

                log_debug("tart.symlink.ln", "creating symlink", target=target, source=source)
                tmux_io.run(["sudo", "ln", "-sf", source, target], capture_output=True)
                log_debug("tart.symlink.done", "symlink ready", target=target)

        # Auto-configure iOS testing if Xcode is mounted from host
        # Supports any Xcode name (m-Xcode.app, m-Xcode-Beta.app, etc.)
        import glob
        log_debug("tart.xcode.scan", "scanning for mounted Xcode")
        xcode_mounts = glob.glob("/Volumes/My Shared Files/m-Xcode*.app")

        if xcode_mounts:
            xcode_mount = xcode_mounts[0]  # Use first found (should only be one)
            xcode_developer = os.path.join(xcode_mount, "Contents/Developer")
        else:
            xcode_mount = None
            xcode_developer = None

        if xcode_developer and os.path.isdir(xcode_developer):
            log_debug("tart.xcode.select", "switching xcode-select", developer=xcode_developer)
            # Point xcode-select to the mounted Xcode so xcrun can find simctl and other tools
            result = tmux_io.run(
                ["sudo", "xcode-select", "--switch", xcode_developer],
                capture_output=True,
                text=True
            )
            if result.returncode != 0:
                import syslog
                syslog.syslog(syslog.LOG_ERR, f"Failed to configure xcode-select: {result.stderr}")

            # Add DEVELOPER_DIR to shell profiles for persistence
            developer_dir_export = f'export DEVELOPER_DIR="{xcode_developer}"\n'
            path_export = f'export PATH="{xcode_developer}/usr/bin:$PATH"\n'

            for profile in ["~/.zprofile", "~/.bash_profile"]:
                profile_path = os.path.expanduser(profile)
                try:
                    # Append to existing file or create new one
                    with open(profile_path, "a") as f:
                        f.write(developer_dir_export)
                        f.write(path_export)
                except OSError:
                    pass  # Non-fatal if we can't update profile

            log_debug("tart.xcode.license", "accepting xcode license")
            # Accept Xcode license (stored in VM's /Library/Preferences, not in Xcode.app)
            result = tmux_io.run(
                ["sudo", "xcodebuild", "-license", "accept"],
                capture_output=True,
                text=True
            )
            log_debug("tart.xcode.license.done", "xcode license accepted")

            # Run first launch in background — this initializes device types and can
            # take 60-120+ seconds on first run. State persists in the Xcode.app bundle
            # via VirtioFS, so subsequent VMs find it already done. Running it in the
            # background lets the agent start immediately instead of blocking setup.
            log_debug("tart.xcode.firstlaunch", "starting xcodebuild -runFirstLaunch in background")
            xcodebuild_log = os.path.join(self.yoloai_dir, "xcodebuild-firstlaunch.log")
            # A marker signals the firstlaunch context to the tmux resolver: the
            # security-scan storm transiently hides tmux, so while this marker
            # exists resolution probes to a long ceiling instead of burning a
            # fixed budget. We do not track completion — the storm outlasts the
            # xcodebuild process, so its exit is not a useful "tmux is back"
            # signal.
            started_marker = os.path.join(self.yoloai_dir, "xcodebuild-firstlaunch.started")
            try:
                os.remove(started_marker)
            except OSError:
                pass
            try:
                with open(started_marker, "w"):
                    pass
            except OSError:
                pass
            try:
                with open(xcodebuild_log, "w") as _xcodebuild_logf:
                    subprocess.Popen(
                        ["sudo", "xcodebuild", "-runFirstLaunch"],
                        stdout=_xcodebuild_logf,
                        stderr=subprocess.STDOUT,
                        start_new_session=True,
                    )
                tmux_io.set_firstlaunch_marker(started_marker)
            except OSError:
                pass  # Non-fatal
            log_debug("tart.xcode.firstlaunch.started", "xcodebuild -runFirstLaunch launched")

        # Symlink mounted PrivateFrameworks to system location (required for CoreSimulator.framework)
        privateframeworks_mount = "/Volumes/My Shared Files/m-PrivateFrameworks"
        privateframeworks_target = "/Library/Developer/PrivateFrameworks"

        if os.path.isdir(privateframeworks_mount):
            # Create parent directory if needed
            tmux_io.run(["sudo", "mkdir", "-p", "/Library/Developer"],
                         capture_output=True, text=True)

            # Only remove if it's already a symlink (safe)
            if os.path.islink(privateframeworks_target):
                result = tmux_io.run(["sudo", "rm", privateframeworks_target],
                                      capture_output=True, text=True)
                if result.returncode != 0:
                    import syslog
                    syslog.syslog(syslog.LOG_ERR, f"Failed to remove PrivateFrameworks symlink: {result.stderr}")

            # Create symlink (ln -sfn handles overwriting existing symlinks)
            result = tmux_io.run(["sudo", "ln", "-sfn", privateframeworks_mount, privateframeworks_target],
                                  capture_output=True, text=True)
            if result.returncode != 0:
                import syslog
                syslog.syslog(syslog.LOG_ERR, f"Failed to create PrivateFrameworks symlink: {result.stderr}")

        # NOTE: Mounting CoreSimulator Volumes from host does NOT work.
        # CoreSimulator cannot discover runtimes from VirtioFS mounts, even with symlinks.
        # Runtimes must be copied locally to /Library/Developer/CoreSimulator/Profiles/Runtimes/
        # Users can copy from /Volumes/My Shared Files/m-Volumes/ if available on host.

        # Add iOS testing note to CLAUDE.md if Xcode is mounted (agent context).
        # The `xcode_mount and` guard is load-bearing, not defensive: xcode_mount is
        # None whenever the glob above finds no mounted Xcode, and os.path.isdir(None)
        # raises TypeError — which main() does not catch, so setup() died and the
        # sandbox never initialized. Every Tart VM without a hand-mounted Xcode hit it.
        # It survived because the lifecycle tier that covers this path was gated on an
        # env var nothing set (see integration_tart_test.go). Found by mypy --strict
        # the day the gate was turned on (D117).
        if xcode_mount and os.path.isdir(xcode_mount):
            claude_md = os.path.expanduser("~/.claude/CLAUDE.md")
            os.makedirs(os.path.dirname(claude_md), exist_ok=True)

            # Check if host has runtimes available to copy
            runtime_mount = "/Volumes/My Shared Files/m-Volumes"
            has_host_runtimes = os.path.isdir(runtime_mount)

            try:
                with open(claude_md, "a") as f:
                    f.write("\n# iOS Simulator Testing\n\n")
                    f.write("Xcode is mounted from the host. To enable iOS/tvOS/watchOS/visionOS testing:\n\n")

                    if has_host_runtimes:
                        f.write("## Copy runtime from host (fastest):\n")
                        f.write("```bash\n")
                        f.write("# See available runtimes on host\n")
                        f.write("ls /Volumes/My\\ Shared\\ Files/m-Volumes/\n\n")
                        f.write("# Copy iOS runtime (example)\n")
                        f.write("sudo mkdir -p /Library/Developer/CoreSimulator/Profiles/Runtimes\n")
                        f.write("sudo ditto \"/Volumes/My Shared Files/m-Volumes/iOS_*/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS\"*.simruntime \\\n")
                        f.write("  /Library/Developer/CoreSimulator/Profiles/Runtimes/\n")
                        f.write("sudo cp \"/Volumes/My Shared Files/m-Volumes/iOS_*/Library/Developer/CoreSimulator/Profiles/Runtimes/\"*/Contents/Info.plist \\\n")
                        f.write("  /Library/Developer/CoreSimulator/Profiles/Runtimes/*/Contents/\n")
                        f.write("```\n\n")

                    f.write("## Or download runtime locally:\n")
                    f.write("```bash\n")
                    f.write("xcodebuild -downloadPlatform iOS\n")
                    f.write("```\n\n")
                    f.write("## Verify and use:\n")
                    f.write("```bash\n")
                    f.write("xcrun simctl list runtimes\n")
                    f.write("xcodebuild test -scheme YourScheme \\\n")
                    f.write("  -destination 'platform=iOS Simulator,name=iPhone 17 Pro'\n")
                    f.write("```\n")
            except OSError:
                pass  # Non-fatal if we can't write CLAUDE.md

    def get_tmux_socket(self) -> str | None:
        """Tart uses the uid-based default socket (/tmp/tmux-<uid>/default)."""
        return None

    def get_working_dir(self) -> str | None:
        """Tart needs explicit cd to the VirtioFS-mounted working directory."""
        working_dir: str = self.cfg.get("working_dir", "")
        if not working_dir:
            return working_dir

        # Go's executeVMWorkDirSetup (rsync + git baseline) runs after launching
        # the Python script, so the directory may not exist yet. Wait for it.
        deadline = time.time() + 120
        while not os.path.isdir(working_dir):
            if time.time() >= deadline:
                log_info("tart.workdir.timeout", "working dir not ready after 120s, continuing anyway",
                         path=working_dir)
                return working_dir
            time.sleep(1)

        # For :copy workdirs the host commits the diff baseline (git init/add/
        # commit, the last step of ExecuteVMWorkDirSetup) AFTER the directory
        # itself appears. The agent must not write into the work tree before
        # that commit lands: git add -A would otherwise bake the agent's output
        # into the baseline and `yoloai diff` would report "No changes". The
        # directory-exists check above is too weak a gate — wait for a committed
        # HEAD, which is the exact "baseline ready" signal. Non-copy workdirs
        # have no git repo, so only wait when this is a copy workdir (copy_dirs
        # is non-empty iff the workdir is :copy).
        if self.cfg.get("copy_dirs"):
            while not self._baseline_committed(working_dir):
                if time.time() >= deadline:
                    log_info("tart.baseline.timeout", "git baseline not ready after 120s, continuing anyway",
                             path=working_dir)
                    break
                time.sleep(1)

        os.chdir(working_dir)
        return working_dir

    @staticmethod
    def _baseline_committed(working_dir: str) -> bool:
        """True once a git baseline commit exists in working_dir (HEAD resolves)."""
        result = tmux_io.run(
            ["git", "-C", working_dir, "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
        )
        return result.returncode == 0

    def prepare_environment(self) -> None:
        """Tart needs the provisioned tool dirs prepended: native Claude Code in
        ~/.local/bin, keg-only node@22, and Homebrew, plus Xcode tools if mounted."""
        leading: list[str] = []

        # Add host-mounted Xcode tools to PATH and set DEVELOPER_DIR if available
        xcode_base = "/Users/admin/host-xcode/Contents"
        xcode_developer = os.path.join(xcode_base, "Developer")
        if os.path.isdir(xcode_developer):
            # Set DEVELOPER_DIR so xcodebuild and other tools can find SDKs
            os.environ["DEVELOPER_DIR"] = xcode_developer
            log_debug("tart.xcode_setup", "configured Xcode environment", developer_dir=xcode_developer)

            # Prepend Xcode binaries (order preserved: usr/bin then the toolchain dir)
            for xcode_path in [
                os.path.join(xcode_developer, "usr/bin"),
                os.path.join(xcode_developer, "Toolchains/XcodeDefault.xctoolchain/usr/bin"),
            ]:
                if os.path.isdir(xcode_path):
                    leading.insert(0, xcode_path)

            # Also add to shell profile for interactive shells
            shell_profile = os.path.expanduser("~/.zprofile")
            xcode_path_export = f'export PATH="{os.path.join(xcode_developer, "usr/bin")}:$PATH"\n'
            try:
                if os.path.exists(shell_profile):
                    with open(shell_profile, "r") as f:
                        content = f.read()
                    if "host-xcode" not in content:
                        with open(shell_profile, "a") as f:
                            f.write(f"# Xcode tools from host mount\n{xcode_path_export}")
                else:
                    with open(shell_profile, "w") as f:
                        f.write(f"# Xcode tools from host mount\n{xcode_path_export}")
            except OSError:
                pass  # Non-fatal if we can't update profile

        _prepend_macos_toolchain_path(leading)

    def read_secrets(self, socket: str | None) -> dict[str, str]:
        """Read secrets from VirtioFS-mounted secrets directory and pass to tmux."""
        return read_secrets(os.path.join(self.yoloai_dir, "secrets"), socket=socket)


class SeatbeltBackend(Backend):
    """Backend for macOS Seatbelt sandboxing (lightweight, no VM)."""

    def setup(self) -> None:
        """Seatbelt-specific setup: HOME redirection, CLI tool symlinks, git config."""
        log_info("sandbox.backend_setup", "Seatbelt backend setup", backend="seatbelt")

        original_home = os.environ.get("HOME", "")
        new_home = os.path.join(self.yoloai_dir, "home")

        os.environ["HOME"] = new_home
        os.makedirs(os.path.join(new_home, ".local", "bin"), exist_ok=True)
        os.environ["PATH"] = os.path.join(new_home, ".local", "bin") + ":" + os.environ.get("PATH", "")

        # Symlink CLI tools from original HOME
        original_bin = os.path.join(original_home, ".local", "bin")
        new_bin = os.path.join(new_home, ".local", "bin")
        if os.path.isdir(original_bin):
            for name in os.listdir(original_bin):
                src = os.path.join(original_bin, name)
                dst = os.path.join(new_bin, name)
                if os.access(src, os.X_OK) and not os.path.lexists(dst):
                    os.symlink(src, dst)

        # Symlink git config
        original_gitconfig = os.path.join(original_home, ".gitconfig")
        new_gitconfig = os.path.join(new_home, ".gitconfig")
        if os.path.isfile(original_gitconfig) and not os.path.lexists(new_gitconfig):
            os.symlink(original_gitconfig, new_gitconfig)

        original_git_dir = os.path.join(original_home, ".config", "git")
        new_config_dir = os.path.join(new_home, ".config")
        new_git_dir = os.path.join(new_config_dir, "git")
        if os.path.isdir(original_git_dir):
            os.makedirs(new_config_dir, exist_ok=True)
            if not os.path.lexists(new_git_dir):
                os.symlink(original_git_dir, new_git_dir)

        # Symlink agent state dir
        state_dir_name = self.cfg.get("state_dir_name", "")
        if state_dir_name:
            agent_dir = os.path.join(self.yoloai_dir, "agent-runtime")
            state_link = os.path.join(new_home, state_dir_name)
            if not os.path.islink(state_link):
                os.symlink(agent_dir, state_link)

        # Symlink home-seed files
        home_seed = os.path.join(self.yoloai_dir, "home-seed")
        if os.path.isdir(home_seed):
            for name in os.listdir(home_seed):
                if name in (".", ".."):
                    continue
                src = os.path.join(home_seed, name)
                dst = os.path.join(new_home, name)
                if not os.path.lexists(dst):
                    os.symlink(src, dst)

        # Create Swift wrapper to auto-add --disable-sandbox when running in Seatbelt
        # (macOS sandboxes don't nest, so Swift PM's sandbox-exec calls fail)
        log_info("seatbelt.swift_wrapper", "about to create Swift wrapper file")
        swift_wrapper = os.path.join(new_home, ".swift-wrapper.sh")
        with open(swift_wrapper, "w") as f:
            f.write("""# Swift PM wrapper for yoloAI Seatbelt sandboxes
# Automatically adds --disable-sandbox to swift build/test commands
# because macOS sandboxes don't support nesting.

swift() {
    if [[ "$1" == "build" || "$1" == "test" ]]; then
        command swift "$1" --disable-sandbox "${@:2}"
    else
        command swift "$@"
    fi
}
""")
        log_info("seatbelt.swift_wrapper", "Swift wrapper file created successfully")

        # Source the wrapper in shell startup files so it's available in interactive shells
        log_info("seatbelt.swift_wrapper", "creating shell startup files with Swift wrapper")
        for rcfile in [".bashrc", ".bash_profile", ".zshrc"]:
            rc_path = os.path.join(new_home, rcfile)
            source_line = "source ~/.swift-wrapper.sh\n"

            # Append to existing file or create new one
            try:
                if os.path.isfile(rc_path):
                    with open(rc_path, "r") as f:
                        content = f.read()
                    if source_line.strip() not in content:
                        with open(rc_path, "a") as f:
                            f.write("\n" + source_line)
                        log_info("seatbelt.swift_wrapper", f"appended to {rcfile}", path=rc_path)
                else:
                    with open(rc_path, "w") as f:
                        f.write(source_line)
                    log_info("seatbelt.swift_wrapper", f"created {rcfile}", path=rc_path)
            except OSError as e:
                log_info("seatbelt.swift_wrapper_error", f"failed to update {rcfile}: {e}", path=rc_path, error=str(e))

    def get_tmux_socket(self) -> str | None:
        """Seatbelt uses a per-sandbox socket in the sandbox directory."""
        return os.path.join(self.yoloai_dir, "tmux", "tmux.sock")

    def get_working_dir(self) -> str | None:
        """Seatbelt needs explicit cd to the working directory."""
        working_dir: str = self.cfg.get("working_dir", "")
        if working_dir:
            os.chdir(working_dir)
        return working_dir

    def prepare_environment(self) -> None:
        """Seatbelt environment preparation: host toolchain PATH + Swift PM cache."""
        # The agent runs on the host, so — like Tart — it needs the Homebrew /
        # keg-only-node@22 dirs on PATH; a non-interactive `make smoketest` shell
        # usually lacks them, which is why `node --version` fails at launch. node
        # may also live outside the fixed Homebrew dirs (nvm, a version manager
        # exported only from a login shell); discover it the way the git path
        # resolves the real git, and prepend its dir first. Tart's call site is
        # left untouched so its behavior is unchanged.
        node_dir = _discover_node_bin_dir()
        _prepend_macos_toolchain_path([node_dir] if node_dir else None)

        # Redirect Swift PM cache to sandbox-local directories to avoid
        # "not accessible or not writable" errors and maintain isolation.
        swiftpm_cache_dir = os.path.join(self.yoloai_dir, "cache", "swiftpm")
        swiftpm_config_dir = os.path.join(self.yoloai_dir, "cache", "swiftpm-config")

        os.makedirs(swiftpm_cache_dir, exist_ok=True)
        os.makedirs(swiftpm_config_dir, exist_ok=True)

        os.environ["SWIFTPM_CACHE_DIR"] = swiftpm_cache_dir
        os.environ["SWIFTPM_CONFIG_DIR"] = swiftpm_config_dir
        os.environ["SWIFTPM_DISABLE_TELEMETRY"] = "1"

        log_debug("seatbelt.swiftpm_cache", "redirected Swift PM cache to sandbox",
                  cache_dir=swiftpm_cache_dir, config_dir=swiftpm_config_dir)

    def read_secrets(self, socket: str | None) -> dict[str, str]:
        """Read secrets from sandbox secrets directory and pass to tmux."""
        return read_secrets(os.path.join(self.yoloai_dir, "secrets"), socket=socket)


# Backend registry (similar to Go's runtime.Register pattern)
# Typed as a constructor Callable (not type[Backend]) so mypy doesn't treat
# calling backend_class(...) as instantiating the abstract Backend base.
_backend_registry: dict[str, Callable[[dict[str, Any], str], Backend]] = {
    "docker": DockerBackend,
    "podman": DockerBackend,  # Podman uses Docker backend
    "seatbelt": SeatbeltBackend,
    "tart": TartBackend,
}


def get_backend(name: str, cfg: dict[str, Any], yoloai_dir: str) -> Backend:
    """Create a backend instance by name."""
    if name not in _backend_registry:
        raise ValueError(f"Unknown backend: {name} (available: {list(_backend_registry.keys())})")
    backend_class = _backend_registry[name]
    return backend_class(cfg, yoloai_dir)


# --- Shared setup functions ---

def setup_tmux_session(cfg: dict[str, Any], yoloai_dir: str, socket: str | None = None) -> None:
    """Start a tmux session with config based on tmux_conf setting."""
    tmux_conf = cfg.get("tmux_conf", "")
    tmux_conf_file = os.path.join(yoloai_dir, "tmux", "tmux.conf")
    home = os.environ.get("HOME", "")
    host_tmux_conf = os.path.join(home, ".tmux.conf") if home else ""

    # Build new-session arguments
    base_args: list[str] = []
    if socket:
        base_args.extend(["-S", socket])

    session_args = ["new-session", "-d", "-s", "main", "-x", "200", "-y", "50"]

    tmux_bin = tmux_io.tmux_bin()
    if tmux_conf in ("default", "default+host"):
        cmd = [tmux_bin] + base_args + ["-f", tmux_conf_file] + session_args
    elif tmux_conf == "host" and host_tmux_conf and os.path.isfile(host_tmux_conf):
        cmd = [tmux_bin] + base_args + ["-f", host_tmux_conf] + session_args
    else:
        cmd = [tmux_bin] + base_args + session_args

    log_debug("tmux.start", f"starting tmux session (tmux_conf={tmux_conf})")
    result = tmux_io.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        log_info("tmux.error", "tmux new-session failed",
                 cmd=" ".join(cmd),
                 exit_code=result.returncode,
                 stdout=result.stdout.strip(),
                 stderr=result.stderr.strip())
        print(f"[sandbox-setup] tmux new-session failed (exit {result.returncode}): {result.stderr.strip()}", file=sys.stderr)
    else:
        log_info("sandbox.tmux_new_session", "new-session succeeded")

    # Verify server is alive after new-session before proceeding.
    _sessions_after_new = tmux_output("list-sessions", socket=socket)
    log_info("sandbox.tmux_server_check", "server alive after new-session",
             alive=bool(_sessions_after_new.strip()),
             sessions=_sessions_after_new.strip())

    # Source host tmux.conf on top of default if default+host
    if tmux_conf == "default+host" and host_tmux_conf and os.path.isfile(host_tmux_conf):
        tmux("source-file", host_tmux_conf, socket=socket)

    # remain-on-exit is also set in tmux.conf, but belt-and-suspenders here.
    # Use set-window-option (not set-option) — remain-on-exit is a window option.
    r = tmux("set-window-option", "-t", "main", "remain-on-exit", "on", socket=socket)
    if r.returncode != 0:
        log_info("tmux.error", "set-window-option remain-on-exit failed",
                 exit_code=r.returncode, stderr=r.stderr.strip())

    # Verify server is alive after set-window-option.
    _sessions_after_swo = tmux_output("list-sessions", socket=socket)
    log_info("sandbox.tmux_server_check", "server alive after set-window-option",
             alive=bool(_sessions_after_swo.strip()),
             sessions=_sessions_after_swo.strip())

    # Pipe raw terminal stream to logs/agent.log for later inspection.
    r = tmux("pipe-pane", "-t", "main", f"cat >> {yoloai_dir}/logs/agent.log", socket=socket)
    if r.returncode != 0:
        log_info("tmux.error", "pipe-pane failed",
                 exit_code=r.returncode, stderr=r.stderr.strip())

    # Verify server is alive after pipe-pane.
    _sessions_after_pp = tmux_output("list-sessions", socket=socket)
    log_info("sandbox.tmux_start", "tmux session created",
             alive_after_pipe_pane=bool(_sessions_after_pp.strip()))


def launch_agent(
    cfg: dict[str, Any],
    socket: str | None = None,
    working_dir: str | None = None,
    backend_inst: Backend | None = None,
    secrets: dict[str, str] | None = None,
    yoloai_dir: str | None = None,
) -> None:
    """Launch the agent command inside the tmux session."""
    agent_command = cfg.get("agent_command", "")
    agent = cfg.get("agent", "")
    model = cfg.get("model", "")
    log_debug("agent.launch", f"launching agent: {agent_command}")

    # PATH augmentation for Tart is handled by backend.prepare_environment()
    # before this function is called.

    # Diagnostic only: verify Node.js works before launching the agent. Node.js 22
    # has known syscall incompatibilities with gVisor ARM64 that cause silent
    # immediate crashes with no output. A missing node must NOT abort the launch:
    # the native Claude Code install bundles its own runtime and needs no system
    # node, so a seatbelt host without node still runs the agent fine. Without the
    # guard, subprocess.run raises FileNotFoundError here and kills the whole
    # launch ("sandbox-exec exited: exit status 1").
    try:
        node_check = tmux_io.run(["node", "--version"], capture_output=True, text=True)
        log_info("sandbox.node_check", "node version check",
                 version=node_check.stdout.strip(),
                 returncode=node_check.returncode,
                 stderr=node_check.stderr.strip())
    except FileNotFoundError:
        log_info("sandbox.node_check", "node not found on PATH — skipping diagnostic",
                 version="", returncode=-1, stderr="node not found on PATH")

    # Check that the agent binary exists before launching, so we can emit a
    # clear error instead of "command not found". Use shutil.which() in the
    # Python process rather than a pane-based check: the pane runs zsh -l
    # which can take >0.4s to source ~/.zprofile and set Homebrew PATH, making
    # a timed pane check unreliable. Python's PATH includes /opt/homebrew/bin
    # on macOS (set by the host environment before sandbox-setup.py runs).
    agent_bin = agent_command.split()[0] if agent_command else ""
    if agent_bin:
        found_at = shutil.which(agent_bin)
        if not found_at:
            log_info("sandbox.agent_not_found", "agent binary not found",
                     agent_bin=agent_bin,
                     python_path=os.environ.get("PATH", ""))
            backend_name = backend_inst.__class__.__name__.replace("Backend", "").lower() if backend_inst else ""
            rebuild_cmd = f"yoloai system build --backend {backend_name}" if backend_name else "yoloai system build"
            tmux("send-keys", "-t", "main",
                 f"echo 'yoloai: {agent_bin} not found — run: {rebuild_cmd}'",
                 "Enter", socket=socket)
            return
        log_debug("sandbox.agent_found", f"agent binary found: {found_at}")

    # Compose the launch command (secret exports + cd + exec) in a pure helper
    # so the shell-escaping and quoting is unit-tested (setup_helpers). tmux
    # set-environment doesn't reach already-running shells, so secrets are
    # exported inline before exec'ing the agent.
    #
    # agent_launch_prefix is the single source of truth for the backend launch
    # wrap (W1a/W1b). Every sandbox carries it: create writes it, and the v1->v2
    # schema migration backfills it for older sandboxes (empty for container
    # backends, a no-op prepend).
    # Fall-to-shell (D96): hook-authoritative agents launch under agent-run.sh,
    # which records `done` on agent exit and keeps the pane alive as a shell.
    # Gated by fall_to_shell in runtime-config.json (off for older sandboxes and
    # heuristic agents → unchanged exec-the-agent behavior). The wrapper lives in
    # the sandbox bin dir, which differs per backend (/yoloai/bin for containers,
    # the VirtioFS mount for Tart, the sandbox dir for seatbelt), so resolve it
    # from yoloai_dir rather than hardcoding the container path; it derives its
    # own YOLOAI_DIR paths once running.
    bin_dir = yoloai_dir or os.environ.get("YOLOAI_DIR", "/yoloai")
    wrapper = os.path.join(bin_dir, "bin", "agent-run.sh") if cfg.get("fall_to_shell") else ""
    send_cmd = build_agent_launch_command(
        agent_command, working_dir, secrets, cfg.get("agent_launch_prefix", ""),
        wrapper=wrapper)

    tmux("send-keys", "-t", "main", send_cmd, "Enter", socket=socket)
    log_info("sandbox.agent_launch", "agent process started", agent=agent, model=model)

    # Check session health shortly after launch to surface immediate crashes
    # (e.g. missing binary, auth error, gVisor syscall rejection) in
    # sandbox.jsonl before the host-side attach attempt.
    time.sleep(0.5)
    sessions = tmux_output("list-sessions", socket=socket)
    pane = tmux_output("capture-pane", "-t", "main", "-p", socket=socket)
    pane_dead = tmux_output("list-panes", "-t", "main", "-F", "#{pane_dead}", socket=socket)
    log_info("sandbox.post_launch", "post-launch check",
             sessions_alive=bool(sessions.strip()),
             pane_dead=(pane_dead.strip() == "1"),
             pane_sample=pane.strip()[:400] if pane else "")


def launch_vscode_tunnel(cfg: dict[str, Any], socket: str | None = None) -> None:
    """Launch VS Code Remote Tunnel in a background tmux window.

    The window is created with -d so focus stays on the agent window.
    On first run the tunnel prompts for auth; the user switches to the
    'vscode-tunnel' window (Ctrl-b n) to complete it. Subsequent runs
    reuse cached credentials from ~/.vscode/cli/ and start immediately.
    """
    tunnel_name = cfg.get("vscode_tunnel_name", cfg.get("sandbox_name", "sandbox"))

    # -d creates the window in the background so focus stays on the agent window.
    r = tmux("new-window", "-d", "-t", "main", "-n", "vscode-tunnel", socket=socket)
    if r.returncode != 0:
        log_info("vscode_tunnel.window_error", "failed to create vscode-tunnel window",
                 exit_code=r.returncode, stderr=r.stderr.strip())
        return

    # Capture tunnel output via tmux pipe-pane (does not affect the process TTY,
    # so interactive prompts — provider selection, device-auth codes — work correctly).
    tmux("pipe-pane", "-t", "main:vscode-tunnel",
         f"cat >> /yoloai/logs/vscode-tunnel.log", socket=socket)

    # Do NOT pipe stdout — VS Code CLI needs a real TTY for the provider-selection
    # menu and device-auth flow on first run.
    #
    # VSCODE_CLI_USE_FILE_KEYCHAIN=1  — bypass D-Bus entirely; use file-based
    # storage in ~/.vscode/cli/ (covered by the ~/.yoloai/vscode-cli/ bind mount).
    #
    # VSCODE_CLI_DISABLE_KEYCHAIN_ENCRYPT=1  — disable AES encryption of the
    # stored token. VS Code CLI derives the encryption key from the container
    # hostname, which changes on every restart (Docker assigns the container ID
    # as hostname). Disabling encryption makes the token portable across restarts.
    tunnel_cmd = (
        f"VSCODE_CLI_USE_FILE_KEYCHAIN=1 "
        f"VSCODE_CLI_DISABLE_KEYCHAIN_ENCRYPT=1 "
        f"exec code tunnel --accept-server-license-terms --name {tunnel_name}"
    )
    tmux("send-keys", "-t", "main:vscode-tunnel", tunnel_cmd, "Enter", socket=socket)
    log_info("vscode_tunnel.launch", "VS Code tunnel started", tunnel_name=tunnel_name)


def monitor_exit(socket: str | None = None) -> None:
    """Daemon thread: poll pane_dead and detach clients when agent exits."""
    def _monitor() -> None:
        while True:
            output = tmux_output("list-panes", "-t", "main", "-F", "#{pane_dead}:#{pane_dead_status}", socket=socket)
            if ":" in (output or ""):
                dead, status = output.strip().split(":", 1)
                if dead == "1":
                    pane = tmux_output("capture-pane", "-t", "main", "-p", socket=socket)
                    log_info("sandbox.agent_exit_detected", "agent pane exited",
                             exit_code=status.strip(),
                             pane_content=pane.strip()[:400] if pane else "")
                    clients = tmux_output("list-clients", "-t", "main", "-F", "#{client_name}", socket=socket)
                    for client in clients.strip().splitlines():
                        client = client.strip()
                        if client:
                            tmux("detach-client", "-t", client, socket=socket)
                    break
            elif not (output or "").strip():
                # list-panes returned nothing: tmux session is completely gone
                # (server crash or session destroyed). remain-on-exit couldn't help.
                sessions = tmux_output("list-sessions", socket=socket)
                if not (sessions or "").strip():
                    log_info("sandbox.session_died", "tmux session gone unexpectedly - agent likely crashed")
                    break
            time.sleep(1)

    t = threading.Thread(target=_monitor, daemon=True)
    t.start()


def wait_for_ready(cfg: dict[str, Any], socket: str | None = None) -> None:
    """Wait for agent ready pattern, auto-accept trust/confirmation prompts."""
    ready_pattern = cfg.get("ready_pattern", "")
    startup_delay = cfg.get("startup_delay", 5)

    log_debug("agent.wait_ready", f"waiting for agent ready (pattern={ready_pattern})")

    if not ready_pattern or ready_pattern == "null":
        time.sleep(float(startup_delay))
        return

    max_wait = 60
    waited = 0

    found = False
    while waited < max_wait:
        pane = tmux_output("capture-pane", "-t", "main", "-p", socket=socket)

        # Auto-accept confirmation prompts
        if "Enter to confirm" in pane:
            if "Yes, I accept" in pane:
                tmux("send-keys", "-t", "main", "Down", socket=socket)
                time.sleep(0.5)
            tmux("send-keys", "-t", "main", "Enter", socket=socket)
            time.sleep(2)
            waited += 2
            continue

        if ready_pattern in pane:
            found = True
            break

        time.sleep(1)
        waited += 1

    if not found:
        log_info("agent.wait_ready_timeout",
                 f"ready pattern not found after {max_wait}s, proceeding anyway",
                 pattern=ready_pattern)

    # Wait for screen to stabilize (no changes for 1 consecutive check)
    prev = ""
    stable = 0
    while stable < 1 and waited < max_wait:
        time.sleep(0.5)
        waited += 1
        curr = tmux_output("capture-pane", "-t", "main", "-p", socket=socket)
        if curr == prev:
            stable += 1
        else:
            stable = 0
        prev = curr


def deliver_prompt(
    cfg: dict[str, Any],
    yoloai_dir: str,
    socket: str | None = None,
    preamble: str | None = None,
) -> bool:
    """Deliver preamble and/or prompt file to the agent via tmux paste-buffer.

    preamble is prepended to the user prompt when provided (e.g. lifecycle
    status notice). If preamble is set but no prompt.txt exists, the preamble
    alone is delivered so the agent has context from the start.
    """
    prompt_file = os.path.join(yoloai_dir, "prompt.txt")

    # Retry prompt.txt existence check — on Kata VMs (VirtioFS), the bind
    # mount may not be visible immediately after container start.
    has_prompt = False
    for attempt in range(5):
        if os.path.isfile(prompt_file):
            has_prompt = True
            break
        if attempt == 0 and not preamble:
            log_info("sandbox.prompt_wait", "prompt.txt not yet visible, retrying",
                     path=prompt_file)
        time.sleep(1)

    if not has_prompt and not preamble:
        log_info("sandbox.prompt_skip",
                 "no prompt.txt after retries; agent started without prompt",
                 path=prompt_file)
        return False

    # Build content: preamble (if any) followed by user prompt (if any).
    prompt_text = None
    if has_prompt:
        with open(prompt_file) as f:
            prompt_text = f.read()
    content = compose_prompt_content(preamble, prompt_text) or ""

    submit_sequence = cfg.get("submit_sequence", "")
    log_debug("prompt.deliver", "delivering prompt", has_preamble=bool(preamble),
              has_user_prompt=has_prompt)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False) as tmp:
        tmp.write(content)
        tmpname = tmp.name
    try:
        r = tmux("load-buffer", tmpname, socket=socket)
        if r.returncode != 0:
            log_error("prompt.load_buffer_failed", "tmux load-buffer failed",
                      exit_code=r.returncode, stderr=r.stderr.strip())
            return False

        r = tmux("paste-buffer", "-t", "main", socket=socket)
        if r.returncode != 0:
            log_error("prompt.paste_buffer_failed", "tmux paste-buffer failed",
                      exit_code=r.returncode, stderr=r.stderr.strip())
            return False

        time.sleep(0.5)
        for key in submit_sequence.split():
            tmux("send-keys", "-t", "main", key, socket=socket)
            time.sleep(0.2)
    finally:
        os.unlink(tmpname)

    log_info("sandbox.prompt_deliver", "prompt delivered", method="paste-buffer")
    return has_prompt  # True only when a real user task was submitted


def _var_lib_docker_fstype() -> str:
    """Backing filesystem type of /var/lib/docker ('overlay', 'ext4', 'xfs', …),
    or '' if it can't be determined. `-T` resolves the containing mount so a
    non-mountpoint path reports the rootfs (overlay) rather than nothing."""
    try:
        r = subprocess.run(
            ["findmnt", "-T", "/var/lib/docker", "-no", "FSTYPE"],
            capture_output=True, text=True, timeout=5,
        )
        if r.returncode == 0:
            return r.stdout.strip()
    except (OSError, subprocess.SubprocessError):
        pass
    return ""


def start_dockerd(log: Callable[[str], None]) -> None:
    """Start the Docker daemon and wait for it to be ready."""
    import shutil as _shutil
    import time as _time
    if _shutil.which("docker") is None:
        log("dockerd: docker not found, skipping")
        return
    # Check if already running
    r = tmux_io.run(["docker", "info"], capture_output=True)
    if r.returncode == 0:
        log("dockerd: already running")
        return
    log("dockerd: starting...")
    fstype = _var_lib_docker_fstype()
    storage_args = dockerd_storage_args(fstype)
    log(f"dockerd: /var/lib/docker backing={fstype or 'unknown'} args={storage_args or 'auto (overlay)'}")
    subprocess.Popen(
        ["sudo", "dockerd", *storage_args],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    # Poll socket until ready (30s timeout)
    deadline = _time.time() + 30
    while _time.time() < deadline:
        r = tmux_io.run(["docker", "info"], capture_output=True)
        if r.returncode == 0:
            log("dockerd: ready")
            return
        _time.sleep(0.5)
    log("dockerd: timed out waiting for daemon")
    # Don't hard-fail — lifecycle commands will fail with a clear error


def run_lifecycle_command(cmd_entry: dict[str, Any], log: Callable[[str], None]) -> bool:
    """Run one lifecycle command entry (string, array, or object form).

    Object form runs all values in parallel; fails if any exit non-zero.
    Returns True on success, False on failure.
    """
    from concurrent.futures import ThreadPoolExecutor, as_completed

    kind = cmd_entry.get("type")
    cmd  = cmd_entry.get("cmd")

    if kind == "string":
        cmd_str = cast(str, cmd)
        r = tmux_io.run(["sh", "-c", cmd_str])
        if r.returncode != 0:
            log(f"lifecycle command failed (exit {r.returncode}): {cmd}")
            return False
    elif kind == "array":
        cmd_list = cast("list[str]", cmd)
        r = tmux_io.run(cmd_list)
        if r.returncode != 0:
            log(f"lifecycle command failed (exit {r.returncode}): {cmd}")
            return False
    elif kind == "object":
        cmd_obj = cast("dict[str, str | list[str]]", cmd)
        failures: list[str] = []
        def run_one(name: str, subcmd: str | list[str]) -> tuple[str, int]:
            r = tmux_io.run(["sh", "-c", subcmd] if isinstance(subcmd, str) else subcmd)
            return name, r.returncode
        with ThreadPoolExecutor() as pool:
            futures = {pool.submit(run_one, n, c): n for n, c in cmd_obj.items()}
            for fut in as_completed(futures):
                name, rc = fut.result()
                if rc != 0:
                    failures.append(f"{name} (exit {rc})")
        if failures:
            log(f"lifecycle commands failed: {', '.join(failures)}")
            return False
    return True


def run_lifecycle_commands(cfg: dict[str, Any], yoloai_dir: str, log: Callable[[str], None]) -> bool:
    """Run lifecycle commands from the runtime config.

    On-create commands run once (guarded by marker file).
    On-start commands run on every start.
    dockerd is started first if required.
    """
    lifecycle = cfg.get("lifecycle")
    if not lifecycle:
        return False

    if lifecycle.get("dockerd_required"):
        start_dockerd(log)

    marker = lifecycle_on_create_marker(yoloai_dir)

    if should_run_on_create(lifecycle, os.path.exists(marker)):
        log("lifecycle: running on-create commands")
        for entry in lifecycle.get("on_create", []):
            if not run_lifecycle_command(entry, log):
                log("lifecycle: on-create command failed; skipping remaining on-create commands")
                break
        else:
            # All on-create commands succeeded — write marker
            try:
                open(marker, "w").close()
            except OSError as e:
                log(f"lifecycle: could not write marker: {e}")

    log("lifecycle: running on-start commands")
    for entry in lifecycle.get("on_start", []):
        if not run_lifecycle_command(entry, log):
            log("lifecycle: on-start command failed")
            # Continue with remaining on-start commands (partial start is better than none)
    return True


def run_lifecycle_background(
    cfg: dict[str, Any],
    yoloai_dir: str,
    socket: str | None,
    log: Callable[[str], None],
    pane_ready_event: threading.Event,
) -> None:
    """Run lifecycle commands in a background thread, then notify the agent.

    Called from a daemon thread so it never blocks the main setup flow.
    pane_ready_event is set by the main thread once it has finished its own
    tmux pane writes (launch_agent + deliver_prompt). The banner waits on
    this event so it cannot interleave into the agent's startup command or
    the initial prompt — both of those write the same pane via send-keys /
    paste-buffer and would race otherwise.
    """
    ran = run_lifecycle_commands(cfg, yoloai_dir, log)
    if ran:
        log_info("lifecycle.background_done", "background lifecycle commands complete")

    # Wait for the main thread to finish writing to the pane before we add
    # the completion banner. Without this, a fast lifecycle (e.g. none
    # configured) lands the banner in the middle of the agent's exec line.
    pane_ready_event.wait()

    if not ran:
        return

    # Deliver a completion notification to the agent pane.
    msg = "[yoloai] Background setup complete — all services are now available."
    with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False) as tmp:
        tmp.write(msg)
        tmpname = tmp.name
    try:
        tmux("load-buffer", tmpname, socket=socket)
        tmux("paste-buffer", "-t", "main", socket=socket)
        time.sleep(0.3)
        submit_sequence = cfg.get("submit_sequence", "")
        for key in submit_sequence.split():
            tmux("send-keys", "-t", "main", key, socket=socket)
            time.sleep(0.2)
    finally:
        os.unlink(tmpname)


def launch_monitor(cfg_path: str, status_file: str, yoloai_dir: str, socket: str | None = None) -> None:
    """Launch the Python status monitor as a background process."""
    monitor_script = os.path.join(yoloai_dir, "bin", "status-monitor.py")
    cmd = ["python3", monitor_script, cfg_path, status_file]
    if socket:
        cmd.append(socket)
    subprocess.Popen(cmd)
    log_info("sandbox.monitor_launch", "status-monitor.py spawned")


# --- Backend-specific setup ---

# --- Main ---

DEBUG: bool = False


def main() -> None:
    global DEBUG

    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} docker|seatbelt|tart [<dir>]", file=sys.stderr)
        sys.exit(1)

    backend_name = sys.argv[1]

    # Determine yoloai_dir based on backend
    if backend_name == "docker":
        yoloai_dir = os.environ.get("YOLOAI_DIR", "/yoloai")
    elif backend_name in ("seatbelt", "tart"):
        if len(sys.argv) < 3:
            print(f"Usage: {sys.argv[0]} {backend_name} <dir>", file=sys.stderr)
            sys.exit(1)
        yoloai_dir = sys.argv[2]
        os.environ["YOLOAI_DIR"] = yoloai_dir
    else:
        print(f"Unknown backend: {backend_name}", file=sys.stderr)
        sys.exit(1)

    # Open structured log (append mode — entrypoint.py may have written entries already).
    _init_sandbox_log(yoloai_dir)

    # Read config
    cfg_path = os.path.join(yoloai_dir, "runtime-config.json")
    cfg = read_config(cfg_path)

    DEBUG = cfg.get("debug", False)

    # Suppress browser-open attempts inside the sandbox
    if "BROWSER" not in os.environ:
        os.environ["BROWSER"] = "true"

    # Tell agents they're inside a sandbox
    os.environ["IS_SANDBOX"] = "1"

    # Create backend instance and run setup
    backend = get_backend(backend_name, cfg, yoloai_dir)
    backend.prepare_environment()
    backend.setup()

    # Get backend-specific paths
    socket = backend.get_tmux_socket()

    # Read secrets and signal the host BEFORE waiting for the working dir.
    # For Tart (and Seatbelt), the host's buildAndStart() blocks in
    # waitForSecretsConsumed() until this marker appears. That call is inside
    # launchContainer(), which must return before executeVMWorkDirSetup() runs
    # (the rsync that creates the VM-local working dir). get_working_dir() below
    # polls for that dir for up to 120 s, and signal_secrets_consumed() used to
    # come after it — creating a deadlock: host waiting for the VM to signal,
    # VM waiting for the host to create the dir, host waiting for the VM to
    # signal … Neither side could proceed. Signalling here, while still in the
    # setup phase, breaks the cycle: the host unblocks, runs the rsync, the dir
    # appears, and get_working_dir() returns promptly.
    # Secrets are safe to read now: copySecretsToSandbox() ran during Create(),
    # before Start(), so yoloai_dir/secrets/ is already populated via VirtioFS.
    # The tmux session does not exist yet, so set-environment is skipped for
    # backends that use a real socket; secrets reach the agent via the explicit
    # env_exports= prefix in the launch_agent() send-keys command instead.
    secrets = backend.read_secrets(socket)
    if backend.writes_consumed_marker:
        signal_secrets_consumed(yoloai_dir)

    working_dir = backend.get_working_dir()

    setup_tmux_session(cfg, yoloai_dir, socket=socket)

    # Launch lifecycle commands in a background thread so the agent starts
    # immediately. The preamble tells the agent what is still setting up;
    # run_lifecycle_background sends a notification when it completes.
    # pane_ready gates the notification on completion of the main thread's
    # own pane writes (launch_agent + deliver_prompt), so the banner cannot
    # race into the agent's startup line or its initial prompt.
    def _log_lifecycle(msg: str) -> None:
        log_info("lifecycle.event", msg)
    preamble = lifecycle_preamble(cfg, yoloai_dir) or None
    pane_ready = threading.Event()
    threading.Thread(
        target=run_lifecycle_background,
        args=(cfg, yoloai_dir, socket, _log_lifecycle, pane_ready),
        daemon=True,
    ).start()

    # Under gVisor on ARM64 the docker exec'd process may see different
    # effective credentials than the container entrypoint, causing EACCES
    # when connecting to the tmux socket. chmod 0777 lets any user in the
    # container connect; the socket is already isolated inside the container.
    if socket and os.path.exists(socket):
        stat_info = os.stat(socket)
        log_info("sandbox.tmux_socket_info", "tmux socket created",
                 path=socket, mode=oct(stat_info.st_mode),
                 uid=stat_info.st_uid, gid=stat_info.st_gid)
        os.chmod(socket, 0o777)

    launch_agent(cfg, socket=socket, working_dir=working_dir, backend_inst=backend, secrets=secrets, yoloai_dir=yoloai_dir)

    if cfg.get("vscode_tunnel"):
        launch_vscode_tunnel(cfg, socket=socket)

    monitor_exit(socket=socket)
    if cfg.get("headless"):
        # Headless run (D100): the prompt is baked into the launch command, so
        # there is nothing to wait for or inject — the agent works from the first
        # line and the monitor records done+exit-code when its pane dies. Treat it
        # as a delivered prompt so the initial status is "active".
        prompt_delivered = True
    else:
        wait_for_ready(cfg, socket=socket)
        prompt_delivered = deliver_prompt(cfg, yoloai_dir, socket=socket, preamble=preamble)
    # Main thread is done writing to the tmux pane; the lifecycle background
    # banner is now safe to deliver.
    pane_ready.set()

    # Write initial status and set title
    status_file = os.path.join(yoloai_dir, "agent-status.json")
    sandbox_name = cfg.get("sandbox_name", "sandbox")

    if prompt_delivered:
        write_status(status_file, "active")
        set_title(sandbox_name, socket=socket)
    else:
        write_status(status_file, "idle")
        set_title(f"> {sandbox_name}", socket=socket)

    # Launch status monitor
    launch_monitor(cfg_path, status_file, yoloai_dir, socket=socket)

    log_info("sandbox.ready", "sandbox fully initialized")

    # Block — process stops only on explicit stop/kill.
    # Use tmux_io.run (not os.execvp) so the Python process stays alive
    # and the monitor_exit daemon thread can detach clients when the agent exits.
    cmd = [tmux_io.tmux_bin()]
    if socket:
        cmd.extend(["-S", socket])
    cmd.extend(["wait-for", "yoloai-exit"])
    result = tmux_io.run(cmd)

    log_info("sandbox.agent_exit", "agent process exited", exit_code=result.returncode)
    sys.exit(result.returncode)


if __name__ == "__main__":
    main()

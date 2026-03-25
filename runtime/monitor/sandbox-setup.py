#!/usr/bin/env python3
"""Consolidated sandbox setup script for all yoloAI backends.

Replaces the per-backend shell entrypoint scripts (entrypoint-user.sh for
Docker, entrypoint.sh for seatbelt, setup.sh for Tart) with a single Python
implementation. Backend-specific setup is dispatched by a CLI argument.

Usage:
    sandbox-setup.py docker                  # Docker (YOLOAI_DIR from env)
    sandbox-setup.py seatbelt <sandbox-dir>  # Seatbelt
    sandbox-setup.py tart <shared-dir>       # Tart
"""

import datetime
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from pathlib import Path


# --- JSONL logger ---

_sandbox_log = None


def _init_sandbox_log(yoloai_dir):
    global _sandbox_log
    log_path = os.path.join(yoloai_dir, "logs", "sandbox.jsonl")
    try:
        _sandbox_log = open(log_path, "a", buffering=1)  # noqa: WPS515 # line-buffered
    except OSError as e:
        print(f"[sandbox-setup] warning: cannot open log: {e}", file=sys.stderr)


def _log(level, event, msg, **fields):
    now = datetime.datetime.utcnow()
    ts = now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
    entry = {"ts": ts, "level": level, "event": event, "msg": msg}
    entry.update(fields)
    if _sandbox_log:
        try:
            _sandbox_log.write(json.dumps(entry) + "\n")
            _sandbox_log.flush()
        except OSError:
            pass


def log_info(event, msg, **fields):
    _log("info", event, msg, **fields)


def log_debug(event, msg, **fields):
    if DEBUG:
        _log("debug", event, msg, **fields)


# --- Utility functions ---

def read_config(path):
    """Read and return the runtime-config.json as a dict."""
    with open(path) as f:
        return json.load(f)


def tmux(*args, socket=None):
    """Run a tmux command, optionally with a per-sandbox socket."""
    cmd = ["tmux"]
    if socket:
        cmd.extend(["-S", socket])
    cmd.extend(args)
    return subprocess.run(cmd, capture_output=True, text=True)


def tmux_output(*args, socket=None):
    """Run a tmux command and return stdout, or empty string on failure."""
    result = tmux(*args, socket=socket)
    if result.returncode == 0:
        return result.stdout
    return ""


def set_title(title, socket=None):
    """Set tmux window title."""
    tmux("rename-window", "-t", "main", title, socket=socket)


def read_secrets(secrets_dir, socket=None):
    """Read secret files from a directory into os.environ and tmux environment.

    For Docker, entrypoint.py reads secrets and execs into sandbox-setup.py,
    so the environment is inherited. For Tart/Seatbelt, sandbox-setup.py runs
    directly, so secrets must be explicitly passed to tmux via set-environment
    and also exported in the agent launch command (since tmux set-environment
    doesn't propagate to shells that are already running).

    Returns a dict of {name: value} for all loaded secrets.
    """
    log_info("read_secrets.check", f"checking secrets_dir={secrets_dir}")
    secrets = {}
    if not os.path.isdir(secrets_dir):
        log_info("read_secrets.not_dir", f"secrets_dir is not a directory: {secrets_dir}")
        return secrets
    try:
        names = os.listdir(secrets_dir)
        log_info("read_secrets.found", f"found {len(names)} files in {secrets_dir}: {names}")
    except OSError as e:
        log_info("read_secrets.list_error", f"failed to list {secrets_dir}: {e}")
        return secrets  # Not accessible (e.g. root-owned dir, container running as non-root)
    loaded_count = 0
    for name in names:
        path = os.path.join(secrets_dir, name)
        if os.path.isfile(path):
            try:
                with open(path) as f:
                    value = f.read()
                    os.environ[name] = value
                    secrets[name] = value
                    # Also set in tmux so the agent shell session inherits it
                    if socket:
                        tmux("set-environment", "-t", "main", name, value, socket=socket)
                    loaded_count += 1
            except OSError as e:
                log_info("read_secrets.read_error", f"failed to read {path}: {e}")
                pass  # Already set by entrypoint.py (running as root), or not accessible
    log_info("read_secrets.done", f"loaded {loaded_count} secrets from {secrets_dir}")
    return secrets


def write_status(status_file, status, exit_code=None):
    """Write agent-status.json."""
    data = {
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


class Backend(ABC):
    """Abstract base class for sandbox backends."""

    def __init__(self, cfg, yoloai_dir):
        self.cfg = cfg
        self.yoloai_dir = yoloai_dir

    @abstractmethod
    def setup(self):
        """Run backend-specific setup (mount symlinks, overlays, etc.)."""
        pass

    @abstractmethod
    def get_tmux_socket(self):
        """Return the tmux socket path, or None for default."""
        pass

    @abstractmethod
    def get_working_dir(self):
        """Return the working directory path, or None if not needed."""
        pass

    @abstractmethod
    def prepare_environment(self):
        """Set up environment variables before launching the agent."""
        pass

    @abstractmethod
    def read_secrets(self, socket):
        """Read secrets and make them available to the agent.

        Returns a dict of {name: value} for all loaded secrets.
        """
        pass

    @abstractmethod
    def prepare_launch_command(self, base_cmd):
        """Prepare the final shell command to launch the agent.

        Allows each backend to customize the launch command (e.g., set PATH,
        source wrapper scripts, etc.) before exec'ing the agent.

        Args:
            base_cmd: The base command string to execute

        Returns:
            The modified command string ready for tmux send-keys
        """
        pass


class DockerBackend(Backend):
    """Backend for Docker and Podman containers."""

    def setup(self):
        """Docker-specific setup: git baseline for overlays, auto-commit loop."""
        log_info("sandbox.backend_setup", "Docker backend setup", backend="docker")

        # Git baseline for overlay mounts
        overlay_mounts = self.cfg.get("overlay_mounts", [])
        for overlay in overlay_mounts:
            merged = overlay.get("merged", "")
            if not merged:
                continue
            log_debug("overlay.git_baseline", f"creating git baseline for overlay: {merged}")
            # Remove .git dirs (creates whiteouts in upper layer)
            for root, dirs, _files in os.walk(merged):
                if ".git" in dirs:
                    import shutil
                    shutil.rmtree(os.path.join(root, ".git"), ignore_errors=True)
                    dirs.remove(".git")
            subprocess.run(["git", "-C", merged, "init"], capture_output=True)
            subprocess.run(["git", "-C", merged, "add", "-A"], capture_output=True)
            subprocess.run(
                ["git", "-C", merged, "commit", "-m", "yoloai overlay baseline", "--no-gpg-sign"],
                capture_output=True,
            )
            log_info("overlay.git_baseline", "git baseline commit created", path=merged)

        # Auto-commit loop for :copy directories
        auto_commit_interval = int(self.cfg.get("auto_commit_interval", 0))
        copy_dirs = self.cfg.get("copy_dirs", [])

        if auto_commit_interval > 0 and copy_dirs:
            log_debug("auto_commit.start", f"starting auto-commit loop (interval={auto_commit_interval}s, dirs={len(copy_dirs)})")

            def _auto_commit():
                while True:
                    time.sleep(auto_commit_interval)
                    timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                    for d in copy_dirs:
                        subprocess.run(["git", "-C", d, "add", "-A"], capture_output=True)
                        subprocess.run(
                            ["git", "-C", d, "commit", "-m", f"yoloai auto-commit {timestamp}", "--no-gpg-sign"],
                            capture_output=True,
                        )

            import threading
            t = threading.Thread(target=_auto_commit, daemon=True)
            t.start()

    def get_tmux_socket(self):
        """Docker uses a fixed socket path from config (for gVisor compatibility)."""
        return self.cfg.get("tmux_socket") or None

    def get_working_dir(self):
        """Docker doesn't need explicit cd - containers start at the right path."""
        return None

    def prepare_environment(self):
        """Docker environment is already prepared by entrypoint.py."""
        pass

    def read_secrets(self, socket):
        """Read secrets from /run/secrets (inherited from entrypoint.py)."""
        return read_secrets("/run/secrets", socket=socket)

    def prepare_launch_command(self, base_cmd):
        """Docker doesn't need command customization."""
        return base_cmd


class TartBackend(Backend):
    """Backend for Tart macOS VMs with VirtioFS mounts."""

    def setup(self):
        """Tart-specific setup: create VirtioFS mount symlinks via sudo."""
        log_info("sandbox.backend_setup", "Tart backend setup", backend="tart")

        mount_map = self.cfg.get("mount_map", {})
        if not mount_map:
            return

        log_debug("tart.symlinks", "creating VirtioFS mount symlinks")
        for target, source in mount_map.items():
            parent = os.path.dirname(target)
            subprocess.run(["sudo", "mkdir", "-p", parent], capture_output=True)

            # Remove existing symlink or empty directory
            if os.path.islink(target):
                subprocess.run(["sudo", "rm", "-f", target], capture_output=True)
            elif os.path.isdir(target):
                # Check if empty
                try:
                    if not os.listdir(target):
                        subprocess.run(["sudo", "rmdir", target], capture_output=True)
                except OSError:
                    pass

            subprocess.run(["sudo", "ln", "-sf", source, target], capture_output=True)

        # Configure xcode-select and accept license if Xcode is mounted from host
        xcode_developer = "/Users/admin/host-xcode/Contents/Developer"
        if os.path.isdir(xcode_developer):
            # Point xcode-select to the mounted Xcode so xcrun can find simctl and other tools
            log_debug("tart.xcode_select", "configuring xcode-select", developer_dir=xcode_developer)
            result = subprocess.run(
                ["sudo", "xcode-select", "--switch", xcode_developer],
                capture_output=True,
                text=True
            )
            if result.returncode != 0:
                log_debug("tart.xcode_select", "xcode-select failed", stderr=result.stderr)

            # Add DEVELOPER_DIR to shell profile so it's set in interactive shells
            shell_profile = os.path.expanduser("~/.zprofile")
            developer_dir_export = f'export DEVELOPER_DIR="{xcode_developer}"\n'
            try:
                # Check if already present
                if os.path.exists(shell_profile):
                    with open(shell_profile, "r") as f:
                        content = f.read()
                    if "DEVELOPER_DIR" not in content:
                        with open(shell_profile, "a") as f:
                            f.write(developer_dir_export)
                else:
                    with open(shell_profile, "w") as f:
                        f.write(developer_dir_export)
                log_debug("tart.xcode_profile", "added DEVELOPER_DIR to shell profile")
            except OSError as e:
                log_debug("tart.xcode_profile", "failed to update shell profile", error=str(e))

            # Accept Xcode license (stored in VM's /Library/Preferences, not in Xcode.app)
            log_debug("tart.xcode_license", "accepting Xcode license")
            result = subprocess.run(
                ["sudo", "xcodebuild", "-license", "accept"],
                capture_output=True,
                text=True
            )
            if result.returncode == 0:
                log_info("tart.xcode_setup", "Xcode configured and license accepted")
            else:
                log_debug("tart.xcode_license", "license acceptance failed or already accepted",
                         stderr=result.stderr)

    def get_tmux_socket(self):
        """Tart uses the uid-based default socket (/tmp/tmux-<uid>/default)."""
        return None

    def get_working_dir(self):
        """Tart needs explicit cd to the VirtioFS-mounted working directory."""
        working_dir = self.cfg.get("working_dir", "")
        if working_dir:
            os.chdir(working_dir)
        return working_dir

    def prepare_environment(self):
        """Tart needs Homebrew paths prepended for node/npm binaries, plus Xcode tools if mounted."""
        homebrew_bins = ["/opt/homebrew/opt/node/bin", "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin"]

        # Add host-mounted Xcode tools to PATH and set DEVELOPER_DIR if available
        xcode_base = "/Users/admin/host-xcode/Contents"
        xcode_developer = os.path.join(xcode_base, "Developer")
        if os.path.isdir(xcode_developer):
            # Set DEVELOPER_DIR so xcodebuild and other tools can find SDKs
            os.environ["DEVELOPER_DIR"] = xcode_developer
            log_debug("tart.xcode_setup", "configured Xcode environment", developer_dir=xcode_developer)

            # Add Xcode binaries to PATH (for this process and child processes)
            xcode_paths = [
                os.path.join(xcode_developer, "usr/bin"),
                os.path.join(xcode_developer, "Toolchains/XcodeDefault.xctoolchain/usr/bin"),
            ]
            for xcode_path in xcode_paths:
                if os.path.isdir(xcode_path):
                    homebrew_bins.insert(0, xcode_path)

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

        current_path = os.environ.get("PATH", "")
        extras = [p for p in homebrew_bins if p not in current_path.split(":")]
        if extras:
            os.environ["PATH"] = ":".join(extras) + ":" + current_path
            log_debug("tart.path_augment", "prepended paths", added=":".join(extras))

    def read_secrets(self, socket):
        """Read secrets from VirtioFS-mounted secrets directory and pass to tmux."""
        return read_secrets(os.path.join(self.yoloai_dir, "secrets"), socket=socket)

    def prepare_launch_command(self, base_cmd):
        """Prepend node 25 to PATH to avoid broken node@24 from Cirrus base image."""
        # The login shell's ~/.zprofile puts node@24 before node 25 in PATH.
        # Prepend node 25's bin dir so the claude shebang (#!/usr/bin/env node)
        # resolves to node 25, not the broken node@24.
        return f'PATH="/opt/homebrew/opt/node/bin:$PATH" {base_cmd}'


class SeatbeltBackend(Backend):
    """Backend for macOS Seatbelt sandboxing (lightweight, no VM)."""

    def setup(self):
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
                if os.access(src, os.X_OK) and not os.path.exists(dst):
                    os.symlink(src, dst)

        # Symlink git config
        original_gitconfig = os.path.join(original_home, ".gitconfig")
        new_gitconfig = os.path.join(new_home, ".gitconfig")
        if os.path.isfile(original_gitconfig) and not os.path.exists(new_gitconfig):
            os.symlink(original_gitconfig, new_gitconfig)

        original_git_dir = os.path.join(original_home, ".config", "git")
        new_config_dir = os.path.join(new_home, ".config")
        new_git_dir = os.path.join(new_config_dir, "git")
        if os.path.isdir(original_git_dir):
            os.makedirs(new_config_dir, exist_ok=True)
            if not os.path.exists(new_git_dir):
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
                if not os.path.exists(dst):
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

    def get_tmux_socket(self):
        """Seatbelt uses a per-sandbox socket in the sandbox directory."""
        return os.path.join(self.yoloai_dir, "tmux", "tmux.sock")

    def get_working_dir(self):
        """Seatbelt needs explicit cd to the working directory."""
        working_dir = self.cfg.get("working_dir", "")
        if working_dir:
            os.chdir(working_dir)
        return working_dir

    def prepare_environment(self):
        """Seatbelt environment preparation: Swift PM cache redirection."""
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

    def read_secrets(self, socket):
        """Read secrets from sandbox secrets directory and pass to tmux."""
        return read_secrets(os.path.join(self.yoloai_dir, "secrets"), socket=socket)

    def prepare_launch_command(self, base_cmd):
        """Source Swift wrapper to auto-add --disable-sandbox for Swift PM commands."""
        # macOS sandboxes don't support nesting, so Swift PM's sandbox-exec calls
        # fail. The wrapper automatically adds --disable-sandbox to swift build/test.
        return f'source ~/.swift-wrapper.sh && {base_cmd}'


# Backend registry (similar to Go's runtime.Register pattern)
_backend_registry = {
    "docker": DockerBackend,
    "podman": DockerBackend,  # Podman uses Docker backend
    "seatbelt": SeatbeltBackend,
    "tart": TartBackend,
}


def get_backend(name, cfg, yoloai_dir):
    """Create a backend instance by name."""
    if name not in _backend_registry:
        raise ValueError(f"Unknown backend: {name} (available: {list(_backend_registry.keys())})")
    backend_class = _backend_registry[name]
    return backend_class(cfg, yoloai_dir)


# --- Shared setup functions ---

def setup_tmux_session(cfg, yoloai_dir, socket=None):
    """Start a tmux session with config based on tmux_conf setting."""
    tmux_conf = cfg.get("tmux_conf", "")
    tmux_conf_file = os.path.join(yoloai_dir, "tmux", "tmux.conf")
    home = os.environ.get("HOME", "")
    host_tmux_conf = os.path.join(home, ".tmux.conf") if home else ""

    # Build new-session arguments
    base_args = []
    if socket:
        base_args.extend(["-S", socket])

    session_args = ["new-session", "-d", "-s", "main", "-x", "200", "-y", "50"]

    if tmux_conf in ("default", "default+host"):
        cmd = ["tmux"] + base_args + ["-f", tmux_conf_file] + session_args
    elif tmux_conf == "host" and host_tmux_conf and os.path.isfile(host_tmux_conf):
        cmd = ["tmux"] + base_args + ["-f", host_tmux_conf] + session_args
    else:
        cmd = ["tmux"] + base_args + session_args

    log_debug("tmux.start", f"starting tmux session (tmux_conf={tmux_conf})")
    result = subprocess.run(cmd, capture_output=True, text=True)
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


def launch_agent(cfg, socket=None, working_dir=None, backend_inst=None, secrets=None):
    """Launch the agent command inside the tmux session."""
    agent_command = cfg.get("agent_command", "")
    agent = cfg.get("agent", "")
    model = cfg.get("model", "")
    log_debug("agent.launch", f"launching agent: {agent_command}")

    # PATH augmentation for Tart is handled by backend.prepare_environment()
    # before this function is called.

    # Diagnostic: verify Node.js works before launching the agent.
    # Node.js 22 has known syscall incompatibilities with gVisor ARM64 that
    # cause silent immediate crashes with no output.
    node_check = subprocess.run(["node", "--version"], capture_output=True, text=True)
    log_info("sandbox.node_check", "node version check",
             version=node_check.stdout.strip(),
             returncode=node_check.returncode,
             stderr=node_check.stderr.strip())

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

    # Build environment variable exports for secrets
    # tmux set-environment doesn't propagate to shells that are already running,
    # so we need to explicitly export them in the shell command before exec'ing the agent.
    env_exports = ""
    if secrets:
        for name, value in secrets.items():
            # Escape single quotes in the value by replacing ' with '\''
            escaped_value = value.replace("'", "'\\''")
            env_exports += f"export {name}='{escaped_value}'; "

    if working_dir:
        # Quote working_dir to handle paths with spaces (e.g. Tart VirtioFS paths)
        base_cmd = f"{env_exports}cd '{working_dir}' && exec {agent_command}"
    else:
        base_cmd = f"{env_exports}exec {agent_command}"

    # Let the backend customize the launch command (PATH, wrapper scripts, etc.)
    send_cmd = backend_inst.prepare_launch_command(base_cmd) if backend_inst else base_cmd

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


def monitor_exit(socket=None):
    """Daemon thread: poll pane_dead and detach clients when agent exits."""
    def _monitor():
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


def wait_for_ready(cfg, socket=None):
    """Wait for agent ready pattern, auto-accept trust/confirmation prompts."""
    ready_pattern = cfg.get("ready_pattern", "")
    startup_delay = cfg.get("startup_delay", 5)

    log_debug("agent.wait_ready", f"waiting for agent ready (pattern={ready_pattern})")

    if not ready_pattern or ready_pattern == "null":
        time.sleep(float(startup_delay))
        return

    max_wait = 60
    waited = 0

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
            break

        time.sleep(1)
        waited += 1

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


def deliver_prompt(cfg, yoloai_dir, socket=None):
    """Deliver prompt file to the agent via tmux paste-buffer."""
    prompt_file = os.path.join(yoloai_dir, "prompt.txt")
    if not os.path.isfile(prompt_file):
        log_info("sandbox.prompt_skip", "no prompt.txt; agent started without prompt")
        return False

    submit_sequence = cfg.get("submit_sequence", "")
    log_debug("prompt.deliver", "delivering prompt")

    tmux("load-buffer", prompt_file, socket=socket)
    tmux("paste-buffer", "-t", "main", socket=socket)

    time.sleep(0.5)
    for key in submit_sequence.split():
        tmux("send-keys", "-t", "main", key, socket=socket)
        time.sleep(0.2)

    log_info("sandbox.prompt_deliver", "prompt delivered", method="paste-buffer")
    return True


def launch_monitor(cfg_path, status_file, yoloai_dir, socket=None):
    """Launch the Python status monitor as a background process."""
    monitor_script = os.path.join(yoloai_dir, "bin", "status-monitor.py")
    cmd = ["python3", monitor_script, cfg_path, status_file]
    if socket:
        cmd.append(socket)
    subprocess.Popen(cmd)
    log_info("sandbox.monitor_launch", "status-monitor.py spawned")


# --- Backend-specific setup ---

# --- Main ---

DEBUG = False


def main():
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
    working_dir = backend.get_working_dir()

    setup_tmux_session(cfg, yoloai_dir, socket=socket)

    # Read secrets and pass to tmux session
    secrets = backend.read_secrets(socket)

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

    launch_agent(cfg, socket=socket, working_dir=working_dir, backend_inst=backend, secrets=secrets)
    monitor_exit(socket=socket)
    wait_for_ready(cfg, socket=socket)
    prompt_delivered = deliver_prompt(cfg, yoloai_dir, socket=socket)

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
    # Use subprocess.run (not os.execvp) so the Python process stays alive
    # and the monitor_exit daemon thread can detach clients when the agent exits.
    cmd = ["tmux"]
    if socket:
        cmd.extend(["-S", socket])
    cmd.extend(["wait-for", "yoloai-exit"])
    result = subprocess.run(cmd)

    log_info("sandbox.agent_exit", "agent process exited", exit_code=result.returncode)
    sys.exit(result.returncode)


if __name__ == "__main__":
    main()

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

import json
import os
import subprocess
import sys
import threading
import time
from pathlib import Path


# --- Utility functions ---

def debug_log(msg):
    """Print a debug message if DEBUG is enabled."""
    if DEBUG:
        print(f"[debug] {msg}", flush=True)


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


def read_secrets(secrets_dir):
    """Read secret files from a directory into os.environ."""
    if not os.path.isdir(secrets_dir):
        return
    for name in os.listdir(secrets_dir):
        path = os.path.join(secrets_dir, name)
        if os.path.isfile(path):
            with open(path) as f:
                os.environ[name] = f.read()


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

    debug_log(f"starting tmux session (tmux_conf={tmux_conf})")
    subprocess.run(cmd, capture_output=True)

    # Source host tmux.conf on top of default if default+host
    if tmux_conf == "default+host" and host_tmux_conf and os.path.isfile(host_tmux_conf):
        tmux("source-file", host_tmux_conf, socket=socket)

    tmux("set-option", "-t", "main", "remain-on-exit", "on", socket=socket)
    tmux("pipe-pane", "-t", "main", f"cat >> {yoloai_dir}/log.txt", socket=socket)


def launch_agent(cfg, socket=None, working_dir=None):
    """Launch the agent command inside the tmux session."""
    agent_command = cfg.get("agent_command", "")
    debug_log(f"launching agent: {agent_command}")

    if working_dir:
        send_cmd = f"cd {working_dir} && exec {agent_command}"
    else:
        send_cmd = f"exec {agent_command}"

    tmux("send-keys", "-t", "main", send_cmd, "Enter", socket=socket)


def monitor_exit(socket=None):
    """Daemon thread: poll pane_dead and detach clients when agent exits."""
    def _monitor():
        while True:
            output = tmux_output("list-panes", "-t", "main", "-F", "#{pane_dead}", socket=socket)
            if output.strip() == "1":
                clients = tmux_output("list-clients", "-t", "main", "-F", "#{client_name}", socket=socket)
                for client in clients.strip().splitlines():
                    client = client.strip()
                    if client:
                        tmux("detach-client", "-t", client, socket=socket)
                break
            time.sleep(1)

    t = threading.Thread(target=_monitor, daemon=True)
    t.start()


def wait_for_ready(cfg, socket=None):
    """Wait for agent ready pattern, auto-accept trust/confirmation prompts."""
    ready_pattern = cfg.get("ready_pattern", "")
    startup_delay = cfg.get("startup_delay", 5)

    debug_log(f"waiting for agent ready (pattern={ready_pattern})")

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
        debug_log("no prompt file found")
        return False

    submit_sequence = cfg.get("submit_sequence", "")
    debug_log("delivering prompt")

    tmux("load-buffer", prompt_file, socket=socket)
    tmux("paste-buffer", "-t", "main", socket=socket)

    time.sleep(0.5)
    for key in submit_sequence.split():
        tmux("send-keys", "-t", "main", key, socket=socket)
        time.sleep(0.2)

    return True


def launch_monitor(cfg_path, status_file, yoloai_dir, socket=None):
    """Launch the Python status monitor as a background process."""
    monitor_script = os.path.join(yoloai_dir, "bin", "status-monitor.py")
    cmd = ["python3", monitor_script, cfg_path, status_file]
    if socket:
        cmd.append(socket)
    subprocess.Popen(cmd)


# --- Backend-specific setup ---

def setup_docker(cfg, yoloai_dir):
    """Docker-specific setup: overlay git baseline and auto-commit loop."""
    # Git baseline for overlay mounts
    overlay_mounts = cfg.get("overlay_mounts", [])
    for overlay in overlay_mounts:
        merged = overlay.get("merged", "")
        if not merged:
            continue
        debug_log(f"creating git baseline for overlay: {merged}")
        # Remove .git dirs (creates whiteouts in upper layer)
        for root, dirs, _files in os.walk(merged):
            if ".git" in dirs:
                import shutil
                shutil.rmtree(os.path.join(root, ".git"), ignore_errors=True)
                dirs.remove(".git")
        subprocess.run(["git", "-C", merged, "init"], capture_output=True)
        subprocess.run(["git", "-C", merged, "add", "-A"], capture_output=True)
        subprocess.run(
            ["git", "-C", merged, "-c", "user.name=yoloai", "-c", "user.email=yoloai@local",
             "commit", "-m", "baseline", "--allow-empty"],
            capture_output=True,
        )

    # Auto-commit loop for :copy directories
    auto_commit_interval = int(cfg.get("auto_commit_interval", 0))
    copy_dirs = cfg.get("copy_dirs", [])

    if auto_commit_interval > 0 and copy_dirs:
        debug_log(f"starting auto-commit loop (interval={auto_commit_interval}s, dirs={len(copy_dirs)})")

        def _auto_commit():
            while True:
                time.sleep(auto_commit_interval)
                timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                for d in copy_dirs:
                    subprocess.run(["git", "-C", d, "add", "-A"], capture_output=True)
                    subprocess.run(
                        ["git", "-C", d, "-c", "user.name=yoloai", "-c", "user.email=yoloai@localhost",
                         "commit", "-m", f"auto-commit at {timestamp}"],
                        capture_output=True,
                    )

        t = threading.Thread(target=_auto_commit, daemon=True)
        t.start()


def setup_seatbelt(cfg, sandbox_dir):
    """Seatbelt-specific setup: HOME redirection, CLI tool symlinks, git config."""
    original_home = os.environ.get("HOME", "")
    new_home = os.path.join(sandbox_dir, "home")

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
    state_dir_name = cfg.get("state_dir_name", "")
    if state_dir_name:
        agent_dir = os.path.join(sandbox_dir, "agent-runtime")
        state_link = os.path.join(new_home, state_dir_name)
        if not os.path.islink(state_link):
            os.symlink(agent_dir, state_link)

    # Symlink home-seed files
    home_seed = os.path.join(sandbox_dir, "home-seed")
    if os.path.isdir(home_seed):
        for name in os.listdir(home_seed):
            if name in (".", ".."):
                continue
            src = os.path.join(home_seed, name)
            dst = os.path.join(new_home, name)
            if not os.path.exists(dst):
                os.symlink(src, dst)
        # Also handle hidden files (listdir already includes them on Python)
        # but glob for dotfiles that listdir might show
        # Note: os.listdir already returns dotfiles, so no extra step needed


def setup_tart(cfg, shared_dir):
    """Tart-specific setup: VirtioFS mount symlinks via sudo."""
    mount_map = cfg.get("mount_map", {})
    if not mount_map:
        return

    debug_log("creating VirtioFS mount symlinks")
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


# --- Main ---

DEBUG = False


def main():
    global DEBUG

    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} docker|seatbelt|tart [<dir>]", file=sys.stderr)
        sys.exit(1)

    backend = sys.argv[1]

    # Determine yoloai_dir based on backend
    if backend == "docker":
        yoloai_dir = os.environ.get("YOLOAI_DIR", "/yoloai")
    elif backend in ("seatbelt", "tart"):
        if len(sys.argv) < 3:
            print(f"Usage: {sys.argv[0]} {backend} <dir>", file=sys.stderr)
            sys.exit(1)
        yoloai_dir = sys.argv[2]
        os.environ["YOLOAI_DIR"] = yoloai_dir
    else:
        print(f"Unknown backend: {backend}", file=sys.stderr)
        sys.exit(1)

    # Read config
    cfg_path = os.path.join(yoloai_dir, "runtime-config.json")
    cfg = read_config(cfg_path)

    DEBUG = cfg.get("debug", False)
    debug_log(f"sandbox-setup starting (backend={backend}, dir={yoloai_dir})")

    # Setup log capture per backend
    if backend == "seatbelt":
        # Redirect stdout/stderr to log.txt (no tee — sandbox-exec blocks /dev/fd)
        log_path = os.path.join(yoloai_dir, "log.txt")
        log_file = open(log_path, "a")
        sys.stdout = log_file
        sys.stderr = log_file
    elif backend in ("docker", "tart"):
        # Append to log (docker logs captures stdout; tart tees externally)
        log_path = os.path.join(yoloai_dir, "log.txt")
        log_file = open(log_path, "a")
        sys.stdout = log_file
        sys.stderr = log_file

    # Read secrets and set env vars
    if backend == "docker":
        read_secrets("/run/secrets")
    else:
        read_secrets(os.path.join(yoloai_dir, "secrets"))

    # Suppress browser-open attempts inside the sandbox
    if "BROWSER" not in os.environ:
        os.environ["BROWSER"] = "true"

    # Tell agents they're inside a sandbox
    os.environ["IS_SANDBOX"] = "1"

    # Run backend-specific setup
    if backend == "docker":
        setup_docker(cfg, yoloai_dir)
    elif backend == "seatbelt":
        setup_seatbelt(cfg, yoloai_dir)
    elif backend == "tart":
        setup_tart(cfg, yoloai_dir)

    # Determine tmux socket (seatbelt uses per-sandbox socket)
    socket = None
    if backend == "seatbelt":
        socket = os.path.join(yoloai_dir, "tmux", "tmux.sock")

    # Determine working directory (seatbelt and tart need explicit cd)
    working_dir = None
    if backend in ("seatbelt", "tart"):
        working_dir = cfg.get("working_dir", "")
        if working_dir:
            os.chdir(working_dir)

    # Shared setup
    setup_tmux_session(cfg, yoloai_dir, socket=socket)
    launch_agent(cfg, socket=socket, working_dir=working_dir)
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

    debug_log("sandbox-setup complete, blocking on tmux wait")

    # Block — process stops only on explicit stop/kill
    cmd = ["tmux"]
    if socket:
        cmd.extend(["-S", socket])
    cmd.extend(["wait-for", "yoloai-exit"])
    os.execvp("tmux", cmd)


if __name__ == "__main__":
    main()

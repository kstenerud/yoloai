#!/usr/bin/env python3
"""Root-level container entrypoint: UID remap, secrets, network isolation,
overlay mounts, setup commands. Execs into sandbox-setup.py when done.

Writes structured JSONL to logs/sandbox.jsonl. Runs as root (or as the host
user under Podman rootless with --userns=keep-id).
"""

import datetime
import json
import os
import socket
import subprocess
import sys


# --- JSONL logger ---

_log_file = None


def _init_log(yoloai_dir):
    global _log_file
    log_path = os.path.join(yoloai_dir, "logs", "sandbox.jsonl")
    try:
        _log_file = open(log_path, "a", buffering=1)  # noqa: WPS515 # line-buffered
    except OSError as e:
        print(f"[entrypoint.py] warning: cannot open log: {e}", file=sys.stderr)


def _log(level, event, msg, **fields):
    now = datetime.datetime.utcnow()
    ts = now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
    entry = {"ts": ts, "level": level, "event": event, "msg": msg}
    entry.update(fields)
    line = json.dumps(entry) + "\n"
    if _log_file:
        try:
            _log_file.write(line)
            _log_file.flush()
        except OSError:
            pass


def log_info(event, msg, **fields):
    _log("info", event, msg, **fields)


def log_debug(event, msg, **fields):
    if DEBUG:
        _log("debug", event, msg, **fields)


def log_error(event, msg, **fields):
    _log("error", event, msg, **fields)


# --- Utility ---

DEBUG = False


def read_config(path):
    with open(path) as f:
        return json.load(f)


# --- Setup stages ---

def remap_uid(cfg, running_as_root):
    """Remap UID/GID and fix ownership to match host user."""
    if not running_as_root:
        log_debug("uid.remap_skip", "skipping UID remap (not running as root)")
        return

    host_uid = str(cfg.get("host_uid", ""))
    host_gid = str(cfg.get("host_gid", ""))

    current_uid = subprocess.check_output(["id", "-u", "yoloai"], text=True).strip()
    current_gid = subprocess.check_output(["id", "-g", "yoloai"], text=True).strip()

    if current_gid != host_gid:
        subprocess.run(["groupmod", "-g", host_gid, "yoloai"], capture_output=True)
    if current_uid != host_uid:
        subprocess.run(["usermod", "-u", host_uid, "yoloai"], capture_output=True)

    # Fix ownership on container-managed directories.
    # Some files may be bind-mounted read-only; chown failures are expected.
    subprocess.run(["chown", "-R", "yoloai:yoloai", "/home/yoloai"], capture_output=True)

    yoloai_dir = cfg.get("yoloai_dir", os.environ.get("YOLOAI_DIR", "/yoloai"))
    subprocess.run(["chown", "yoloai:yoloai", yoloai_dir], capture_output=True)
    for name in os.listdir(yoloai_dir):
        path = os.path.join(yoloai_dir, name)
        subprocess.run(["chown", "yoloai:yoloai", path], capture_output=True)
    for subdir in ("bin", "tmux", "logs"):
        d = os.path.join(yoloai_dir, subdir)
        if os.path.isdir(d):
            subprocess.run(["chown", "-R", "yoloai:yoloai", d], capture_output=True)

    log_info("uid.remap", "UID/GID remapped",
             host_uid=int(host_uid) if host_uid else 0,
             host_gid=int(host_gid) if host_gid else 0)


def read_secrets():
    """Read secret files from /run/secrets into os.environ."""
    secrets_dir = "/run/secrets"
    if not os.path.isdir(secrets_dir):
        log_debug("secrets.skip", "no secrets directory")
        return
    wrote = False
    for name in os.listdir(secrets_dir):
        path = os.path.join(secrets_dir, name)
        if os.path.isfile(path):
            try:
                with open(path) as f:
                    os.environ[name] = f.read()
                log_info("secrets.write", "credential written", path=path, kind=name)
                wrote = True
            except OSError:
                pass
    if not wrote:
        log_debug("secrets.skip", "no secrets to inject")


def isolate_network(cfg):
    """Apply iptables default-deny and allowlist rules."""
    if not cfg.get("network_isolated", False):
        return

    # Resolve allowed domains to IPs.
    allowed_domains = cfg.get("allowed_domains", [])
    allowed_ips = set()
    for domain in allowed_domains:
        try:
            results = socket.getaddrinfo(domain, None, socket.AF_INET)
            for r in results:
                ip = r[4][0]
                allowed_ips.add((domain, ip))
        except OSError:
            pass

    # Read nameservers from /etc/resolv.conf.
    nameservers = []
    try:
        with open("/etc/resolv.conf") as f:
            for line in f:
                if line.startswith("nameserver "):
                    parts = line.split()
                    if len(parts) >= 2:
                        nameservers.append(parts[1])
    except OSError:
        pass

    # Set up ipset for allowed IPs.
    subprocess.run(["ipset", "create", "allowed-domains", "hash:net"],
                   capture_output=True)
    subprocess.run(["ipset", "flush", "allowed-domains"], capture_output=True)
    for _domain, ip in allowed_ips:
        subprocess.run(["ipset", "add", "allowed-domains", ip], capture_output=True)

    # Flush existing OUTPUT rules.
    subprocess.run(["iptables", "-F", "OUTPUT"], capture_output=True)

    # Allow loopback.
    subprocess.run(["iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"],
                   capture_output=True)
    # Allow established/related.
    subprocess.run(["iptables", "-A", "OUTPUT", "-m", "state",
                    "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"],
                   capture_output=True)
    # Allow DNS to configured nameservers.
    for ns in nameservers:
        for proto in ("udp", "tcp"):
            subprocess.run(["iptables", "-A", "OUTPUT", "-d", ns, "-p", proto,
                             "--dport", "53", "-j", "ACCEPT"], capture_output=True)
    # Allow traffic to allowlisted IPs.
    subprocess.run(["iptables", "-A", "OUTPUT", "-m", "set",
                    "--match-set", "allowed-domains", "dst", "-j", "ACCEPT"],
                   capture_output=True)
    # Reject everything else.
    subprocess.run(["iptables", "-A", "OUTPUT", "-j", "REJECT",
                    "--reject-with", "icmp-port-unreachable"], capture_output=True)

    log_info("network.isolate", "iptables default-deny applied")
    for domain, ip in allowed_ips:
        log_info("network.allow", "domain added to allowlist", domain=domain, ip=ip)


def apply_overlays(cfg, yoloai_dir):
    """Mount overlayfs for each configured overlay mount."""
    overlays = cfg.get("overlay_mounts", [])
    if not overlays:
        log_debug("overlay.skip", "no overlay mounts configured")
        return

    for i, overlay in enumerate(overlays):
        lower = overlay.get("lower", "")
        merged = overlay.get("merged", "")
        if not lower or not merged:
            continue

        tmpfs_dir = f"/tmp/yoloai-overlay-{i}"
        os.makedirs(tmpfs_dir, exist_ok=True)
        subprocess.run(["mount", "-t", "tmpfs", "tmpfs", tmpfs_dir], check=True)
        os.makedirs(os.path.join(tmpfs_dir, "upper"), exist_ok=True)
        os.makedirs(os.path.join(tmpfs_dir, "work"), exist_ok=True)
        os.makedirs(merged, exist_ok=True)

        opts = (f"lowerdir={lower},"
                f"upperdir={tmpfs_dir}/upper,"
                f"workdir={tmpfs_dir}/work")
        subprocess.run(["mount", "-t", "overlay", "overlay", "-o", opts, merged],
                        check=True)
        subprocess.run(["chown", "yoloai:yoloai", merged], capture_output=True)
        log_info("overlay.mount", "overlayfs mounted", path=merged)


def run_setup_commands(cfg):
    """Run each setup command in sequence, logging start/done/error."""
    commands = cfg.get("setup_commands", [])
    total = len(commands)
    for i, cmd in enumerate(commands):
        log_info("setup_cmd.start", f"setup command {i + 1}/{total}",
                 cmd=cmd, index=i, total=total)
        import time
        start = time.monotonic()
        result = subprocess.run(["sh", "-c", cmd], capture_output=True, text=True)
        duration_ms = int((time.monotonic() - start) * 1000)
        if result.returncode == 0:
            log_info("setup_cmd.done", f"setup command {i + 1}/{total} succeeded",
                     duration_ms=duration_ms)
        else:
            log_error("setup_cmd.error", f"setup command {i + 1}/{total} failed",
                      exit_code=result.returncode, duration_ms=duration_ms)
            # Don't abort on setup command failure — match current shell behavior.


# --- Main ---

def main():
    global DEBUG

    yoloai_dir = os.environ.get("YOLOAI_DIR", "/yoloai")
    _init_log(yoloai_dir)

    cfg_path = os.path.join(yoloai_dir, "runtime-config.json")
    cfg = read_config(cfg_path)

    DEBUG = cfg.get("debug", False)

    log_info("entrypoint.python_start", "root Python entrypoint started")

    running_as_root = (os.geteuid() == 0)

    remap_uid(cfg, running_as_root)
    read_secrets()

    # Suppress browser-open attempts inside the sandbox.
    os.environ.setdefault("BROWSER", "true")
    os.environ["IS_SANDBOX"] = "1"

    isolate_network(cfg)
    apply_overlays(cfg, yoloai_dir)
    run_setup_commands(cfg)

    # Flush and close the log before exec (fd will be closed by exec).
    if _log_file:
        _log_file.flush()
        _log_file.close()

    setup_py = os.path.join(yoloai_dir, "bin", "sandbox-setup.py")

    if running_as_root:
        os.execvp("gosu", ["gosu", "yoloai", "python3", setup_py, "docker"])
    else:
        # Podman rootless: already running as the host user.
        os.environ["HOME"] = "/home/yoloai"
        os.execvp("python3", ["python3", setup_py, "docker"])


if __name__ == "__main__":
    main()

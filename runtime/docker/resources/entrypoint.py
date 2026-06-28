#!/usr/bin/env python3
"""Root-level container entrypoint: UID remap, secrets, network isolation,
overlay mounts, setup commands. Execs into sandbox-setup.py when done.

Writes structured JSONL to logs/sandbox.jsonl. Runs as root (or as the host
user under Podman rootless with --userns=keep-id).
"""

import datetime
import json
import os
import subprocess
import sys

import firewall


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
    """Read runtime-config.json into a dict.

    A missing or empty file means a bare substrate instance — one created via the
    runtime's Create without the orchestrator's agent config (the S3 carve, D88) —
    which has no agent to launch. Return an empty dict; main() treats an empty
    config as keepalive_only so the box comes up as a neutral holder instead of
    crashing. A present-but-malformed config still raises: that is a real error,
    not the agent-free case.
    """
    try:
        with open(path) as f:
            data = f.read()
    except FileNotFoundError:
        return {}
    if not data.strip():
        return {}
    return json.loads(data)


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
    try:
        names = os.listdir(secrets_dir)
    except OSError as e:
        log_error("secrets.error", "cannot list secrets dir", error=str(e))
        return
    for name in names:
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


def signal_secrets_consumed(yoloai_dir):
    """Touch a host-visible marker after /run/secrets has been read.

    The host (buildAndStart) waits for this marker before removing the
    ephemeral secrets temp dir bind-mounted at /run/secrets. Without it,
    a slow-booting backend (Kata VM via containerd) could have the dir
    removed before this entrypoint reads it, leaving the agent
    unauthenticated.

    Written under logs/ because only /yoloai subdirs are bind-mounted to
    the host (the /yoloai root is not), and logs/ propagates guest->host
    promptly. Must match store.SecretsConsumedMarker.
    """
    marker = os.path.join(yoloai_dir, "logs", ".secrets-consumed")
    try:
        with open(marker, "w") as f:
            f.write("1")
        log_debug("secrets.consumed", "wrote secrets-consumed marker", path=marker)
    except OSError as e:
        log_error("secrets.consumed_error", "cannot write secrets-consumed marker", error=str(e))


def isolate_network(cfg):
    """Apply iptables default-deny and allowlist rules in this container.

    Raises firewall.NetworkIsolationError if any rule fails to install, so the
    container fails to start rather than running with unenforced isolation.

    When YOLOAI_FIREWALL_EXTERNAL is set the allowlist is installed out-of-band by
    a netns-sharing sidecar that holds CAP_NET_ADMIN this container deliberately
    lacks (tamper-resistant isolation — the agent can't flush a firewall installed
    from outside its reach). The entrypoint must NOT install it here, and could
    not: without NET_ADMIN the iptables calls would fail. See
    docs/contributors/design/plans/tamper-resistant-network-isolation.md.
    """
    if not cfg.get("network_isolated", False):
        return
    if os.environ.get("YOLOAI_FIREWALL_EXTERNAL") == "1":
        log_info("network.external",
                 "network isolation installed out-of-container by sidecar; "
                 "skipping in-container firewall")
        return

    allowed_ips = firewall.resolve_domains(cfg.get("allowed_domains", []), log_error)
    nameservers = firewall.read_nameservers(log_error)
    injector = os.environ.get("YOLOAI_BROKER_INJECTOR_ENDPOINT", "")
    firewall.apply_firewall(allowed_ips, nameservers, injector, log_info, log_error)


def apply_overlays(cfg, yoloai_dir):
    """Mount overlayfs for each configured overlay mount.

    After mounting, verifies the overlay is writable. On macOS Docker Desktop
    (VirtioFS), the host-mounted upper/work directories lack xattr support,
    causing the kernel to silently downgrade the overlay to read-only. When
    this happens, we remount using container-local upper/work directories.
    Trade-off: changes won't persist across container restarts on that platform.
    """
    overlays = cfg.get("overlay_mounts", [])
    if not overlays:
        log_debug("overlay.skip", "no overlay mounts configured")
        return

    for overlay in overlays:
        lower = overlay.get("lower", "")
        upper = overlay.get("upper", "")
        work = overlay.get("work", "")
        merged = overlay.get("merged", "")
        if not lower or not upper or not work or not merged:
            continue

        opts = f"lowerdir={lower},upperdir={upper},workdir={work}"
        try:
            subprocess.run(["mount", "-t", "overlay", "overlay", "-o", opts, merged],
                           check=True, capture_output=True)
        except subprocess.CalledProcessError as e:
            log_error("overlay.error", "overlay mount failed", path=merged,
                      exit_code=e.returncode, stderr=e.stderr.decode(errors="replace").strip())
            raise

        # Verify the overlay is writable. VirtioFS (macOS Docker Desktop)
        # lacks trusted.* xattr support, so overlayfs silently mounts read-only.
        probe = os.path.join(merged, ".yoloai-probe")
        try:
            with open(probe, "w") as f:
                f.write("")
            os.unlink(probe)
            log_debug("overlay.writable", "overlay is writable", path=merged)
        except OSError:
            # Read-only overlay — remount with container-local upper/work on
            # a tmpfs. We use tmpfs (not the container rootfs) because the
            # rootfs is typically overlay2, and nesting overlayfs on overlayfs
            # fails on many kernel versions.
            log_info("overlay.virtofs_fallback",
                     "overlay is read-only (VirtioFS?), remounting with tmpfs-backed dirs",
                     path=merged)
            subprocess.run(["umount", merged], capture_output=True)

            import base64
            encoded = base64.b64encode(merged.encode()).decode()
            local_base = f"/run/yoloai-overlay/{encoded}"
            os.makedirs(local_base, exist_ok=True)
            subprocess.run(["mount", "-t", "tmpfs", "tmpfs", local_base],
                           check=True, capture_output=True)
            local_upper = os.path.join(local_base, "upper")
            local_work = os.path.join(local_base, "ovlwork")
            os.makedirs(local_upper)
            os.makedirs(local_work)

            # Preserve any prior state from the original (VirtioFS) upper dir.
            if os.path.isdir(upper) and os.listdir(upper):
                subprocess.run(["cp", "-a", upper + "/.", local_upper],
                               capture_output=True)

            local_opts = f"lowerdir={lower},upperdir={local_upper},workdir={local_work}"
            try:
                subprocess.run(["mount", "-t", "overlay", "overlay", "-o", local_opts, merged],
                               check=True, capture_output=True)
            except subprocess.CalledProcessError as e:
                log_error("overlay.error", "overlay remount with tmpfs-backed dirs failed",
                          path=merged, exit_code=e.returncode,
                          stderr=e.stderr.decode(errors="replace").strip())
                raise
            log_info("overlay.local_upper",
                     "overlay remounted with tmpfs-backed upper; changes won't persist across restarts",
                     path=merged)

        subprocess.run(["chown", "-R", "yoloai:yoloai", merged], capture_output=True)
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

    if cfg.get("isolation") == "container-privileged":
        # Under rootless Podman privileged (keep-id:uid=1001) the entrypoint runs
        # as the non-root yoloai user, so elevate via sudo; yoloai has passwordless
        # sudo. Running directly as root (Docker, macOS Podman) needs no sudo.
        cmd = ["mount", "--make-shared", "/"]
        if not running_as_root:
            cmd = ["sudo", "-n", *cmd]
        try:
            subprocess.run(cmd, check=True, capture_output=True)
            log_info("mount.shared", "root mount set to shared propagation")
        except subprocess.CalledProcessError as e:
            log_error("mount.shared_error", "mount --make-shared / failed",
                      exit_code=e.returncode, stderr=e.stderr.decode(errors="replace").strip())

    # A bare substrate instance (empty config, no agent) is a neutral keepalive
    # holder by definition — there is no agent_command to launch, and the agent
    # path (sandbox-setup.py) requires the config anyway. So default to
    # keepalive_only when the config is empty, matching the agent-free substrate
    # brought up via runtime.Create + Launch (S3 carve, D88). A non-empty config
    # respects its explicit keepalive_only (default False → launch the agent).
    keepalive = cfg.get("keepalive_only", not cfg)

    # YOLOAI_KEEPALIVE_ONLY is the authoritative signal for the agent-free
    # bring-up (startViaLaunch sets it on the container). It overrides the file
    # because the file's keepalive_only is patched on the host via atomic rename
    # just before Create, and Docker Desktop's gRPC-FUSE serves the *stale*
    # pre-patch content for a single-file bind mount when the entrypoint reads it
    # at container start — so the box would silently take the legacy inline path
    # and never write .substrate-ready, and the host would time out. An env var is
    # baked into the container config at create, immune to that mount staleness.
    # See docs/contributors/backend-idiosyncrasies.md.
    if os.environ.get("YOLOAI_KEEPALIVE_ONLY") == "1":
        keepalive = True

    remap_uid(cfg, running_as_root)
    if not keepalive:
        # In keepalive_only mode the entrypoint must NOT read secrets or write the
        # consumed marker — the launched session-runner (sandbox-setup.py) does
        # both itself, and the host waits for the marker before removing the
        # ephemeral secrets dir. Writing it here first would cause the host to
        # remove the dir before the runner has read /run/secrets.
        read_secrets()
        signal_secrets_consumed(yoloai_dir)

    # Suppress browser-open attempts inside the sandbox.
    os.environ.setdefault("BROWSER", "true")
    os.environ["IS_SANDBOX"] = "1"

    isolate_network(cfg)
    apply_overlays(cfg, yoloai_dir)
    run_setup_commands(cfg)

    if keepalive:
        # S3 carve: agent-free substrate bring-up. The session-runner is launched
        # over this holder via runtime.ProcessLauncher; it reads /run/secrets and
        # writes the consumed marker itself. tini (PID 1) reaps and forwards
        # SIGTERM to sleep, so `docker stop` works cleanly.
        log_info("entrypoint.keepalive_only", "box up on neutral keep-alive; no agent launched")

    # Flush and close the log before exec (fd will be closed by exec).
    if _log_file:
        _log_file.flush()
        _log_file.close()

    if keepalive:
        # Signal that root provisioning is complete and the box is ready for the
        # host to launch the session-runner over the holder. The host blocks on
        # this marker before ProcessLauncher.Launch: a runner started DURING root
        # setup (UID remap, chown -R, network) is silently killed — the readiness
        # race found in the S3 carve smoke (DF44). Written LAST, just before exec.
        try:
            open(os.path.join(yoloai_dir, "logs", ".substrate-ready"), "w").close()
        except OSError:
            pass
        if running_as_root:
            os.execvp("gosu", ["gosu", "yoloai", "sleep", "infinity"])
        else:
            # Podman rootless: already running as the host user.
            os.environ["HOME"] = "/home/yoloai"
            os.execvp("sleep", ["sleep", "infinity"])

    setup_py = os.path.join(yoloai_dir, "bin", "sandbox-setup.py")

    if running_as_root:
        os.execvp("gosu", ["gosu", "yoloai", "python3", setup_py, "docker"])
    else:
        # Podman rootless: already running as the host user.
        os.environ["HOME"] = "/home/yoloai"
        os.execvp("python3", ["python3", setup_py, "docker"])


if __name__ == "__main__":
    main()

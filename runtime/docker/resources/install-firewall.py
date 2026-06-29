#!/usr/bin/env python3
# ABOUTME: Tamper-resistant network-isolation installer. Run from an ephemeral
# ABOUTME: sidecar container that shares the agent's network namespace but holds
# ABOUTME: CAP_NET_ADMIN the agent itself lacks — so the agent (even via sudo)
# ABOUTME: cannot flush the firewall. Reads its inputs from the environment the
# ABOUTME: host sets at sidecar launch; the security-critical rules live in firewall.py.
"""Install the network-isolation allowlist from a netns-sharing sidecar.

Inputs (env, set by the host launch path):
  YOLOAI_FW_ALLOWED_DOMAINS        space-separated allowlist domains
  YOLOAI_BROKER_INJECTOR_ENDPOINT  optional "host:port" of the credential injector

The agent's /etc/resolv.conf nameservers are shared into this container by Docker
(--network container:<id> shares the resolv.conf), so read_nameservers() sees the
same DNS servers the agent uses.

Exits non-zero if any rule fails to install, so the host fails the launch rather
than running the agent with unenforced isolation.
"""

import datetime
import json
import os
import sys

import firewall


def _emit(stream, level, event, msg, **fields):
    now = datetime.datetime.utcnow()
    ts = now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
    entry = {"ts": ts, "level": level, "event": event, "msg": msg}
    entry.update(fields)
    print(json.dumps(entry), file=stream, flush=True)


def log_info(event, msg, **fields):
    _emit(sys.stdout, "info", event, msg, **fields)


def log_error(event, msg, **fields):
    _emit(sys.stderr, "error", event, msg, **fields)


def main():
    domains = os.environ.get("YOLOAI_FW_ALLOWED_DOMAINS", "").split()
    injector = os.environ.get("YOLOAI_BROKER_INJECTOR_ENDPOINT", "")

    allowed_ips = firewall.resolve_domains(domains, log_error)
    nameservers = firewall.read_nameservers(log_error)
    try:
        firewall.apply_firewall(allowed_ips, nameservers, injector, log_info, log_error)
    except firewall.NetworkIsolationError as e:
        log_error("network.install_failed", "firewall installation failed", error=str(e))
        sys.exit(1)


if __name__ == "__main__":
    main()

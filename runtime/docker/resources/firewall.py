#!/usr/bin/env python3
# ABOUTME: Shared network-isolation firewall logic — the iptables/ipset default-deny
# ABOUTME: allowlist sequence. Imported by entrypoint.py (in-container fallback path)
# ABOUTME: and install-firewall.py (the tamper-resistant netns-sidecar path) so the
# ABOUTME: security-critical rules have ONE implementation, never two that can drift.
"""Network-isolation firewall rules.

The rule sequence is identical regardless of who installs it; only the
*enforcement point* differs (inside the agent container, or from an ephemeral
sidecar that shares its network namespace — see
docs/contributors/design/plans/tamper-resistant-network-isolation.md). Logging is
injected via callables so the same code logs to entrypoint.py's JSONL stream or to
the sidecar's stdout/stderr.
"""

import socket
import subprocess


class NetworkIsolationError(RuntimeError):
    """Raised when network isolation rules cannot be applied.

    A sandbox configured with network_isolated=true MUST NOT proceed if the
    iptables/ipset rules can't be installed — silent failure would leave the
    agent with unrestricted egress while the user believes it is contained.
    """


def run_strict(args, log_error, event, **fields):
    """Run a command, raising NetworkIsolationError on non-zero exit or missing binary."""
    try:
        result = subprocess.run(args, capture_output=True, text=True)
    except FileNotFoundError as e:
        log_error(event, "required binary not found", cmd=args, error=str(e), **fields)
        raise NetworkIsolationError(f"required binary not found: {args[0]}") from e
    if result.returncode != 0:
        stderr = (result.stderr or "").strip()
        log_error(event, "command failed", cmd=args,
                  exit_code=result.returncode, stderr=stderr, **fields)
        raise NetworkIsolationError(
            f"{' '.join(args)} failed (exit {result.returncode}): {stderr}"
        )
    return result


def resolve_domains(domains, log_error):
    """Resolve allowlisted domains to (domain, ipv4) pairs.

    A domain that doesn't resolve is logged but not fatal — the user may have
    listed a domain that's down or typo'd, and we still want the deny-by-default
    posture applied.
    """
    allowed_ips = set()
    for domain in domains:
        try:
            for r in socket.getaddrinfo(domain, None, socket.AF_INET):
                allowed_ips.add((domain, r[4][0]))
        except OSError as e:
            log_error("network.resolve_failed",
                      "could not resolve allowlisted domain; entry will be ignored",
                      domain=domain, error=str(e))
    return allowed_ips


def read_nameservers(log_error):
    """Read nameserver IPs from /etc/resolv.conf so DNS egress can be allowed.

    Without these, DNS lookups inside the sandbox are blocked by the default-deny
    rule, so an empty result is logged loudly — the symptom (every hostname fails
    to resolve) is otherwise hard to attribute to network isolation.
    """
    nameservers = []
    try:
        with open("/etc/resolv.conf") as f:
            for line in f:
                if line.startswith("nameserver "):
                    parts = line.split()
                    if len(parts) >= 2:
                        nameservers.append(parts[1])
    except OSError as e:
        log_error("network.resolv_conf_unreadable",
                  "cannot read /etc/resolv.conf; DNS will be blocked", error=str(e))
    if not nameservers:
        log_error("network.no_nameservers",
                  "no nameservers found in /etc/resolv.conf; DNS will be blocked "
                  "and allowlisted domains will be unreachable by name")
    return nameservers


def apply_firewall(allowed_ips, nameservers, injector_endpoint, log_info, log_error):
    """Install the iptables default-deny + allowlist rules on the OUTPUT chain.

    allowed_ips is a set of (domain, ip) pairs; nameservers a list of DNS server
    IPs; injector_endpoint an optional "host:port" for the credential-broker
    injector (allowed port-specifically when brokering composes with isolation).

    Raises NetworkIsolationError if any rule fails to install, so the caller can
    abort rather than proceed with unenforced isolation.
    """
    # ipset for efficient IP matching. ipset+iptables-nft may not be available
    # everywhere (e.g. Podman on macOS uses iptables-nft which lacks xt_set), so
    # probe and fall back to per-IP rules.
    use_ipset = False
    try:
        run_strict(["ipset", "create", "-exist", "allowed-domains", "hash:net"],
                   log_error, "network.ipset_create_failed")
        run_strict(["ipset", "flush", "allowed-domains"],
                   log_error, "network.ipset_flush_failed")
        for domain, ip in allowed_ips:
            run_strict(["ipset", "add", "-exist", "allowed-domains", ip],
                       log_error, "network.ipset_add_failed", domain=domain, ip=ip)
        use_ipset = True
    except NetworkIsolationError:
        log_info("network.ipset_unavailable",
                 "ipset not available; will use per-IP iptables rules")

    # Flush existing OUTPUT rules so we start from a known state.
    run_strict(["iptables", "-F", "OUTPUT"], log_error, "network.iptables_flush_failed")
    # Allow loopback.
    run_strict(["iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"],
               log_error, "network.iptables_loopback_failed")
    # Allow established/related.
    run_strict(["iptables", "-A", "OUTPUT", "-m", "state",
                "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"],
               log_error, "network.iptables_established_failed")
    # Allow DNS to configured nameservers.
    for ns in nameservers:
        for proto in ("udp", "tcp"):
            run_strict(["iptables", "-A", "OUTPUT", "-d", ns, "-p", proto,
                        "--dport", "53", "-j", "ACCEPT"],
                       log_error, "network.iptables_dns_failed", nameserver=ns, proto=proto)
    # Allow traffic to allowlisted IPs — via ipset match if available, else
    # individual per-IP rules. iptables-nft may lack xt_set even when the ipset
    # binary works (e.g. Podman Machine on macOS), so catch failure here too and
    # fall through to the per-IP path.
    if use_ipset:
        try:
            run_strict(["iptables", "-A", "OUTPUT", "-m", "set",
                        "--match-set", "allowed-domains", "dst", "-j", "ACCEPT"],
                       log_error, "network.iptables_allowlist_failed")
        except NetworkIsolationError:
            log_info("network.ipset_match_unavailable",
                     "iptables --match-set failed; falling back to per-IP rules")
            use_ipset = False
    if not use_ipset:
        for domain, ip in allowed_ips:
            run_strict(["iptables", "-A", "OUTPUT", "-d", ip, "-j", "ACCEPT"],
                       log_error, "network.iptables_perip_failed", domain=domain, ip=ip)
    # Allow the credential-broker injector endpoint when brokering is active under
    # isolation (the host-side proxy the agent's base_url points at). Port-specific
    # so it opens only the injector, not the rest of the gateway host. The injector
    # reaches the real upstream host-side, outside this sandbox's allowlist — so the
    # agent's LLM egress collapses to this one endpoint while its credential stays
    # host-side.
    if injector_endpoint:
        host, _, port = injector_endpoint.rpartition(":")
        if host and port:
            run_strict(["iptables", "-A", "OUTPUT", "-d", host, "-p", "tcp",
                        "--dport", port, "-j", "ACCEPT"],
                       log_error, "network.iptables_broker_failed", endpoint=injector_endpoint)
            log_info("network.broker_allow",
                     "allowed credential-broker injector endpoint", endpoint=injector_endpoint)
        else:
            log_error("network.broker_endpoint_malformed",
                      "injector endpoint is not host:port; ignoring", endpoint=injector_endpoint)
    # Reject everything else. This is the load-bearing rule — if every prior rule
    # succeeded but this one failed, the sandbox would be wide open.
    run_strict(["iptables", "-A", "OUTPUT", "-j", "REJECT",
                "--reject-with", "icmp-port-unreachable"],
               log_error, "network.iptables_reject_failed")

    log_info("network.isolate", "iptables default-deny applied",
             nameserver_count=len(nameservers), allowed_ip_count=len(allowed_ips))
    for domain, ip in allowed_ips:
        log_info("network.allow", "domain added to allowlist", domain=domain, ip=ip)

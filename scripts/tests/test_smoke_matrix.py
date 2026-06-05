# ABOUTME: Tests for smoke-harness structural matrix applicability — which
# ABOUTME: (test × backend) pairings dind/isolation_check are scheduled on.

from __future__ import annotations

from typing import Callable

import smoke_test
from smoke_test import (
    BASE_LINUX_BACKENDS,
    BASE_MACOS_BACKENDS,
    BackendSpec,
    FULL_LINUX_BACKENDS,
    FULL_MACOS_BACKENDS,
    dind_applies,
    isolation_check_applies,
    wrong_os_backends,
)

ALL_SPECS = FULL_LINUX_BACKENDS + FULL_MACOS_BACKENDS


def _labels(predicate: Callable[[BackendSpec], bool]) -> set[str]:
    return {s.label for s in ALL_SPECS if predicate(s)}


def test_dind_applies_only_to_container_privileged() -> None:
    # dind needs nested-dockerd caps; only the container-privileged spec grants them.
    assert _labels(dind_applies) == {"docker-priv"}


def test_isolation_check_applies_to_capable_backends_only() -> None:
    # The container daemons (docker/podman/containerd) enforce iptables isolation;
    # gVisor (-enhanced) and the seatbelt/tart backends structurally cannot.
    assert _labels(isolation_check_applies) == {
        "docker",
        "podman",
        "docker-priv",
        "containerd-vm",
    }


def test_isolation_check_excludes_gvisor_enhanced_runtimes() -> None:
    # Backend is capable (docker/containerd) but the -enhanced runtime is gVisor,
    # whose netstack doesn't honor the host iptables rules.
    excluded = {s.label for s in ALL_SPECS if not isolation_check_applies(s)}
    assert {"docker-cenhanced", "containerd-vmenhanced"} <= excluded


def test_isolation_check_runs_on_privileged_and_vm_when_capable() -> None:
    # Regression guard for the coverage gap this refactor closed: privileged and
    # plain-VM backends are capable, so they must be scheduled, not silently skipped.
    by_label = {s.label: s for s in ALL_SPECS}
    assert isolation_check_applies(by_label["docker-priv"])
    assert isolation_check_applies(by_label["containerd-vm"])


def test_seatbelt_and_tart_never_run_network_isolation() -> None:
    by_label = {s.label: s for s in ALL_SPECS}
    assert not isolation_check_applies(by_label["seatbelt"])
    assert not isolation_check_applies(by_label["tart"])
    # ...nor dind, which needs Linux privileged container caps.
    assert not dind_applies(by_label["seatbelt"])
    assert not dind_applies(by_label["tart"])


def test_predicates_partition_cleanly_over_base_matrix() -> None:
    # Sanity: predicates accept any BackendSpec from the shipped matrices without
    # raising, and at least one spec is applicable for each (the phase isn't dead).
    assert any(dind_applies(s) for s in smoke_test.FULL_LINUX_BACKENDS)
    assert any(isolation_check_applies(s) for s in smoke_test.FULL_LINUX_BACKENDS)


def test_wrong_os_on_linux_full_lists_mac_locked_backends() -> None:
    # On a Linux host, the macOS matrix's OS-locked backends (seatbelt, tart)
    # can't run here regardless of install — they're the wrong-OS group.
    labels = {s.label for s in wrong_os_backends(FULL_MACOS_BACKENDS, "mac")}
    assert labels == {"seatbelt", "tart"}


def test_wrong_os_on_mac_full_lists_linux_locked_backends() -> None:
    # On a macOS host, containerd (Kata) is Linux-only; both VM variants surface.
    labels = {s.label for s in wrong_os_backends(FULL_LINUX_BACKENDS, "linux")}
    assert labels == {"containerd-vm", "containerd-vmenhanced"}


def test_wrong_os_never_lists_docker_or_podman() -> None:
    # docker/podman bridge to both hosts (Docker Desktop / Podman Machine), so they
    # are never wrong-OS — only genuinely OS-locked tech is reported.
    for matrix, other in ((FULL_LINUX_BACKENDS, "linux"), (FULL_MACOS_BACKENDS, "mac")):
        labels = {s.label for s in wrong_os_backends(matrix, other)}
        assert "docker" not in labels
        assert "podman" not in labels


def test_wrong_os_base_tier_lists_only_other_host_vm() -> None:
    # Base tier: on Linux the macOS base matrix contributes just tart; on macOS the
    # Linux base matrix contributes just containerd-vm.
    assert {s.label for s in wrong_os_backends(BASE_MACOS_BACKENDS, "mac")} == {"tart"}
    assert {s.label for s in wrong_os_backends(BASE_LINUX_BACKENDS, "linux")} == {"containerd-vm"}

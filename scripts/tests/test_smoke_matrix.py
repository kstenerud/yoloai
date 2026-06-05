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
    uncovered_backends,
    uncovered_reason,
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


def test_uncovered_on_linux_full_lists_mac_only_backends() -> None:
    # On a Linux host (this=FULL_LINUX), the macOS matrix contributes the mac-only
    # backends; docker/podman run here so they're excluded.
    labels = {s.label for s in uncovered_backends(FULL_MACOS_BACKENDS, FULL_LINUX_BACKENDS)}
    assert labels == {"seatbelt", "tart"}


def test_uncovered_on_mac_full_includes_linux_isolation_variants() -> None:
    # On a macOS host (this=FULL_MACOS), the Linux matrix contributes both the
    # OS-locked containerd VMs AND the docker isolation variants that the mac matrix
    # never schedules — the gap this fix closes. docker/podman are excluded.
    labels = {s.label for s in uncovered_backends(FULL_LINUX_BACKENDS, FULL_MACOS_BACKENDS)}
    assert labels == {
        "docker-cenhanced",
        "docker-priv",
        "containerd-vm",
        "containerd-vmenhanced",
    }


def test_uncovered_never_lists_backends_scheduled_here() -> None:
    # docker/podman are in both matrices, so they're never "uncovered" either way.
    for other, here in (
        (FULL_LINUX_BACKENDS, FULL_MACOS_BACKENDS),
        (FULL_MACOS_BACKENDS, FULL_LINUX_BACKENDS),
    ):
        labels = {s.label for s in uncovered_backends(other, here)}
        assert "docker" not in labels
        assert "podman" not in labels


def test_uncovered_base_tier_set_difference() -> None:
    # Base tier: on Linux only tart is uncovered; on macOS docker-priv (isolation
    # variant) and containerd-vm (OS-locked) are.
    assert {s.label for s in uncovered_backends(BASE_MACOS_BACKENDS, BASE_LINUX_BACKENDS)} == {"tart"}
    assert {s.label for s in uncovered_backends(BASE_LINUX_BACKENDS, BASE_MACOS_BACKENDS)} == {
        "docker-priv",
        "containerd-vm",
    }


def test_uncovered_reason_distinguishes_os_lock_from_isolation() -> None:
    # On a macOS run, uncovered Linux backends get specific reasons: OS-locked
    # containerd points at the other host; the docker isolation variants explain
    # the isolation mode rather than claiming the daemon can't run here.
    by_label = {s.label: s for s in FULL_LINUX_BACKENDS}
    assert uncovered_reason(by_label["containerd-vm"], "Linux") == "requires a Linux host"
    assert "gVisor" in uncovered_reason(by_label["docker-cenhanced"], "Linux")
    assert "privileged" in uncovered_reason(by_label["docker-priv"], "Linux")

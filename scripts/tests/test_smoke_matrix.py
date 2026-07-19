# ABOUTME: Tests for smoke-harness structural matrix applicability — which
# ABOUTME: (test × backend) pairings dind/isolation_check are scheduled on.

from __future__ import annotations

from typing import Callable

import pytest

import smoke_test
from smoke_test import (
    LINUX_BACKENDS,
    MACOS_BACKENDS,
    BackendSpec,
    dind_applies,
    docker_provider_candidates,
    isolation_check_applies,
    should_run_under_filter,
    spec_needed_for_filters,
    tag_transfer_applies,
    uncontrolled_backends,
    uncovered_backends,
    uncovered_reason,
)

ALL_SPECS = LINUX_BACKENDS + MACOS_BACKENDS


def _labels(predicate: Callable[[BackendSpec], bool]) -> set[str]:
    return {s.label for s in ALL_SPECS if predicate(s)}


def test_dind_applies_only_to_container_privileged() -> None:
    # dind needs nested-dockerd caps; only the container-privileged specs grant them
    # (docker on both hosts; podman on macOS, verified on a rootless Podman Machine).
    assert _labels(dind_applies) == {"docker-priv", "podman-priv"}


def test_tag_transfer_trimmed_on_expensive_vm_backends_only() -> None:
    # Lever 3 of the speedup plan: tart/apple keep stop_start as their one
    # end-to-end scenario; tag_transfer's second per-backend boot runs only
    # where a boot costs seconds. Everything else stays scheduled.
    excluded = {s.label for s in ALL_SPECS if not tag_transfer_applies(s)}
    assert excluded == {
        s.label for s in ALL_SPECS
        if s.check_backend in smoke_test.EXPENSIVE_VM_BACKENDS
    }
    assert {"tart", "apple"} <= {
        s.check_backend for s in ALL_SPECS if not tag_transfer_applies(s)
    }
    # Kata (containerd-vm) is a VM but not an expensive one — never trimmed.
    assert all(
        tag_transfer_applies(s) for s in ALL_SPECS if s.check_backend == "containerd"
    )


def test_isolation_check_applies_to_capable_backends_only() -> None:
    # The container daemons (docker/podman/containerd) enforce iptables isolation;
    # gVisor (-enhanced) and the seatbelt/tart backends structurally cannot.
    assert _labels(isolation_check_applies) == {
        "docker",
        "podman",
        "docker-priv",
        "podman-priv",
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


def test_predicates_partition_cleanly_over_matrix() -> None:
    # Sanity: predicates accept any BackendSpec from the shipped matrices without
    # raising, and at least one spec is applicable for each (the phase isn't dead).
    assert any(dind_applies(s) for s in LINUX_BACKENDS)
    assert any(isolation_check_applies(s) for s in LINUX_BACKENDS)


def test_one_matrix_per_host_os() -> None:
    # The tier no longer selects a smaller matrix — there is exactly one matrix per
    # host OS, keyed in HOST_MATRICES. apple is macOS-only; containerd is Linux-only.
    assert set(smoke_test.HOST_MATRICES) == {"linux", "mac"}
    assert smoke_test.HOST_MATRICES["linux"] is LINUX_BACKENDS
    assert smoke_test.HOST_MATRICES["mac"] is MACOS_BACKENDS
    assert "apple" in {s.label for s in MACOS_BACKENDS}
    assert "apple" not in {s.label for s in LINUX_BACKENDS}


def test_uncovered_on_linux_lists_mac_only_backends() -> None:
    # On a Linux host (this=LINUX), the macOS matrix contributes the mac-only
    # backends; docker/podman/*-priv run here so they're excluded. The apple,
    # seatbelt, and tart macOS backends surface as uncovered.
    labels = {s.label for s in uncovered_backends(MACOS_BACKENDS, LINUX_BACKENDS)}
    assert labels == {"seatbelt", "tart", "apple"}


def test_uncovered_on_mac_includes_linux_isolation_variants() -> None:
    # On a macOS host (this=MACOS), the Linux matrix contributes the OS-locked
    # containerd VMs and the gVisor variant yoloai blocks on macOS. docker/podman
    # are scheduled here; docker-priv is too (privileged runs via the container
    # backend's Linux VM on macOS), so neither is "uncovered".
    labels = {s.label for s in uncovered_backends(LINUX_BACKENDS, MACOS_BACKENDS)}
    assert labels == {
        "docker-cenhanced",
        "containerd-vm",
        "containerd-vmenhanced",
    }


def test_uncovered_never_lists_backends_scheduled_here() -> None:
    # docker/podman are in both matrices, so they're never "uncovered" either way.
    for other, here in (
        (LINUX_BACKENDS, MACOS_BACKENDS),
        (MACOS_BACKENDS, LINUX_BACKENDS),
    ):
        labels = {s.label for s in uncovered_backends(other, here)}
        assert "docker" not in labels
        assert "podman" not in labels


def test_uncovered_reason_distinguishes_os_lock_from_isolation() -> None:
    # Uncovered backends get specific, host-derived reasons: OS-locked daemons
    # point at the host they need; gVisor (-enhanced) explains its macOS block.
    linux_by_label = {s.label: s for s in LINUX_BACKENDS}
    mac_by_label = {s.label: s for s in MACOS_BACKENDS}
    assert uncovered_reason(linux_by_label["containerd-vm"]) == "requires a Linux host"
    # apple is OS-locked to macOS — the reason is derived from the spec, not a
    # passed-in label, so it stays correct on any host.
    assert uncovered_reason(mac_by_label["apple"]) == "requires a macOS host"
    assert uncovered_reason(mac_by_label["tart"]) == "requires a macOS host"
    enhanced = uncovered_reason(linux_by_label["docker-cenhanced"])
    assert "gVisor" in enhanced and "macOS" in enhanced


def test_docker_provider_candidates_active_first() -> None:
    # Both providers installed: keep both, active listed first to avoid a needless
    # context switch.
    assert docker_provider_candidates(
        ["default", "desktop-linux", "orbstack"], "orbstack"
    ) == ["orbstack", "desktop-linux"]
    assert docker_provider_candidates(
        ["default", "desktop-linux", "orbstack"], "desktop-linux"
    ) == ["desktop-linux", "orbstack"]


def test_docker_provider_candidates_active_not_a_known_provider() -> None:
    # Active is some other context (e.g. default): cycle the known providers in
    # their canonical order.
    assert docker_provider_candidates(
        ["default", "orbstack", "desktop-linux"], "default"
    ) == ["orbstack", "desktop-linux"]


def test_docker_provider_candidates_single_or_none_known() -> None:
    # Only one known provider installed -> just it.
    assert docker_provider_candidates(["default", "orbstack"], "orbstack") == ["orbstack"]
    # No known providers (e.g. Linux native) -> fall back to the single active
    # context so --all-docker-providers collapses to one run.
    assert docker_provider_candidates(["default"], "default") == ["default"]
    assert docker_provider_candidates([], "") == []


# --test / --backend filter matching (DF19). should_run_under_filter (scheduling)
# and spec_needed_for_filters (prereq selection) must agree on every input.


def test_filter_none_runs_everything() -> None:
    # No --test filter: every test/backend slot runs.
    assert should_run_under_filter("stop_start/tart", None) is True
    assert should_run_under_filter("isolation_check/docker", None) is True
    assert should_run_under_filter("clone", None) is True


def test_filter_bare_label_matches_all_backends() -> None:
    # DF19 regression: `--test stop_start` must select every stop_start/<backend>.
    f = ["stop_start"]
    assert should_run_under_filter("stop_start/tart", f) is True
    assert should_run_under_filter("stop_start/docker", f) is True
    # ...but not a different test.
    assert should_run_under_filter("isolation_check/docker", f) is False
    assert should_run_under_filter("clone", f) is False


def test_filter_full_name_pins_one_backend() -> None:
    f = ["stop_start/tart"]
    assert should_run_under_filter("stop_start/tart", f) is True
    assert should_run_under_filter("stop_start/docker", f) is False
    assert should_run_under_filter("stop_start", f) is False  # bare != full


def test_filter_bare_label_matches_clone() -> None:
    # Non-matrix tests are referenced by their bare name; the label IS the name.
    assert should_run_under_filter("clone", ["clone"]) is True
    assert should_run_under_filter("stop_start/tart", ["clone"]) is False


def test_spec_needed_no_filters_needs_every_backend() -> None:
    assert spec_needed_for_filters("tart", None, None) is True
    assert spec_needed_for_filters("docker", None, None) is True


def test_spec_needed_backend_filter() -> None:
    assert spec_needed_for_filters("tart", None, ["tart"]) is True
    assert spec_needed_for_filters("docker", None, ["tart"]) is False


def test_spec_needed_mirrors_should_run_for_bare_and_full() -> None:
    # Bare label -> every backend is needed (so it gets prereq-built), matching
    # should_run_under_filter selecting every backend.
    assert spec_needed_for_filters("tart", ["stop_start"], None) is True
    assert spec_needed_for_filters("docker", ["stop_start"], None) is True
    # Full name -> only the named backend is needed.
    assert spec_needed_for_filters("tart", ["stop_start/tart"], None) is True
    assert spec_needed_for_filters("docker", ["stop_start/tart"], None) is False
    # dind is a matrix test too (regression: it was missing from the prereq list).
    assert spec_needed_for_filters("docker", ["dind"], None) is True


def test_filter_scheduling_and_prereq_agree() -> None:
    # For every matrix label and a couple of representative backends, a backend
    # is prereq-needed exactly when at least one of its slots would be scheduled.
    backends = ["docker", "tart", "podman"]
    filters: list[list[str]] = [
        ["stop_start"],
        ["stop_start/tart"],
        ["isolation_check"],
        ["dind"],
        ["stop_start", "isolation_check/docker"],
    ]
    for f in filters:
        for b in backends:
            scheduled = any(
                should_run_under_filter(f"{label}/{b}", f)
                for label in smoke_test.MATRIX_TEST_LABELS
            )
            needed = spec_needed_for_filters(b, f, None)
            assert scheduled == needed, f"filter={f} backend={b}: scheduled={scheduled} needed={needed}"


# --- Mandatory-infra carve-out (D112) ---------------------------------------
# The env parse mirrors the Go side (internal/testutil/uncontrolled.go); the
# same CSV governs both layers, so absence-handling stays in lockstep.


@pytest.mark.parametrize(
    "raw,want",
    [
        ("", set()),
        ("containerd", {"containerd"}),
        ("containerd,apple", {"containerd", "apple"}),
        (" containerd , , apple ,", {"containerd", "apple"}),
    ],
)
def test_uncontrolled_backends_parse(
    monkeypatch: pytest.MonkeyPatch, raw: str, want: set[str]
) -> None:
    monkeypatch.setenv("YOLOAI_TEST_UNCONTROLLED_BACKENDS", raw)
    assert uncontrolled_backends() == want


def test_uncontrolled_backends_unset_is_empty(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("YOLOAI_TEST_UNCONTROLLED_BACKENDS", raising=False)
    assert uncontrolled_backends() == set()

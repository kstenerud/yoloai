# ABOUTME: Tests for the smoke harness's tart vmnet-wedge liveness checks
# ABOUTME: (DF86) — doctor/info JSON parsing, the fingerprint, and retry suppression.

from __future__ import annotations

from pathlib import Path

from smoke_test import (
    FINGERPRINTS,
    is_wedge_failure,
    parse_info_net_health,
    parse_net_liveness,
    scan_fingerprints,
)


# --- parse_net_liveness ---------------------------------------------------


def test_parse_net_liveness_extracts_wedged_vm() -> None:
    doc = {
        "net_liveness": {
            "vms": [
                {
                    "sandbox_name": "embrace",
                    "vm_name": "yoloai-embrace",
                    "state": "wedged",
                    "detail": "169.254.93.37",
                }
            ]
        }
    }
    assert parse_net_liveness(doc) == [("embrace", "169.254.93.37")]


def test_parse_net_liveness_ok_only_returns_empty() -> None:
    doc = {
        "net_liveness": {
            "vms": [
                {"sandbox_name": "embrace", "vm_name": "yoloai-embrace", "state": "ok", "detail": "10.0.0.5"},
            ]
        }
    }
    assert parse_net_liveness(doc) == []


def test_parse_net_liveness_absent_section_returns_empty() -> None:
    assert parse_net_liveness({}) == []


def test_parse_net_liveness_null_section_returns_empty() -> None:
    assert parse_net_liveness({"net_liveness": None}) == []


def test_parse_net_liveness_malformed_vms_shape_returns_empty() -> None:
    assert parse_net_liveness({"net_liveness": {"vms": "not-a-list"}}) == []
    assert parse_net_liveness({"net_liveness": "not-a-dict"}) == []


def test_parse_net_liveness_skips_malformed_entries() -> None:
    doc = {
        "net_liveness": {
            "vms": [
                "not-a-dict",
                {"vm_name": "yoloai-noname", "state": "wedged"},  # missing sandbox_name
                {"sandbox_name": "", "state": "wedged"},  # empty sandbox_name
                {"sandbox_name": "good", "state": "wedged"},  # missing detail is tolerated
            ]
        }
    }
    assert parse_net_liveness(doc) == [("good", "")]


def test_parse_net_liveness_multiple_wedged() -> None:
    doc = {
        "net_liveness": {
            "vms": [
                {"sandbox_name": "a", "state": "wedged", "detail": "169.254.1.1"},
                {"sandbox_name": "b", "state": "ok", "detail": "10.0.0.2"},
                {"sandbox_name": "c", "state": "wedged", "detail": "169.254.2.2"},
            ]
        }
    }
    assert parse_net_liveness(doc) == [("a", "169.254.1.1"), ("c", "169.254.2.2")]


# --- parse_info_net_health -------------------------------------------------


def test_parse_info_net_health_wedged() -> None:
    assert parse_info_net_health(
        {"net_health": "wedged", "net_health_detail": "169.254.93.37"}
    ) == ("wedged", "169.254.93.37")


def test_parse_info_net_health_ok() -> None:
    assert parse_info_net_health({"net_health": "ok", "net_health_detail": "10.0.0.5"}) == (
        "ok",
        "10.0.0.5",
    )


def test_parse_info_net_health_absent_fields_returns_none() -> None:
    assert parse_info_net_health({}) is None
    assert parse_info_net_health({"status": "running"}) is None


def test_parse_info_net_health_missing_detail_defaults_empty() -> None:
    assert parse_info_net_health({"net_health": "wedged"}) == ("wedged", "")


def test_parse_info_net_health_malformed_returns_none() -> None:
    assert parse_info_net_health({"net_health": 5}) is None
    assert parse_info_net_health({"net_health": ""}) is None


# --- fingerprint ordering ---------------------------------------------------


def _make_attempt(tmp_path: Path) -> Path:
    """An otherwise-clean attempt dir; the wedge signature only ever appears
    in the harness reason (a host-side raise), never in guest artifacts."""
    attempt = tmp_path / "attempt1"
    sandbox = attempt / "sb"
    (sandbox / "logs").mkdir(parents=True)
    return attempt


def test_wedge_fingerprint_matches_the_new_raise_message(tmp_path: Path) -> None:
    reason = (
        "guest network dead (vmnet wedge): guest en0 is link-local 169.254.93.37 — "
        "restart to recover: yoloai stop embrace && yoloai start embrace"
    )
    hits = scan_fingerprints(_make_attempt(tmp_path), extra_text=reason)
    assert hits, "expected the wedge fingerprint to match"
    assert hits[0].fp.label.startswith("wedged tart vmnet session")
    assert "169254-link-local" in hits[0].fp.anchor


def test_wedge_fingerprint_matches_the_stale_lease_variant(tmp_path: Path) -> None:
    """The stale-DHCP-lease wedge variant (DF86 follow-up) has no 169.254
    address at all — it must still match on "guest network dead"/"vmnet
    wedge", not on the link-local alternative."""
    reason = (
        "guest network dead (vmnet wedge): stale DHCP lease: guest has "
        "192.168.65.2 but no host bridge is on that subnet "
        "(bridge100 is 192.168.139.3/23) — "
        "restart to recover: yoloai stop embrace && yoloai start embrace"
    )
    hits = scan_fingerprints(_make_attempt(tmp_path), extra_text=reason)
    assert hits, "expected the wedge fingerprint to match the stale-lease variant"
    assert hits[0].fp.label.startswith("wedged tart vmnet session")
    assert is_wedge_failure(reason)


def test_wedge_fingerprint_precedes_generic_harness_timeout(tmp_path: Path) -> None:
    """Ordering test: the wedge fingerprint must be listed before the generic
    harness-timeout catch-all so it wins the headline (first match wins)."""
    labels = [fp.label for fp in FINGERPRINTS]
    wedge_idx = next(i for i, l in enumerate(labels) if l.startswith("wedged tart vmnet session"))
    catchall_idx = next(i for i, l in enumerate(labels) if l.startswith("harness timeout"))
    assert wedge_idx < catchall_idx


def test_wedge_fingerprint_not_the_generic_harness_timeout(tmp_path: Path) -> None:
    reason = (
        "guest network dead (vmnet wedge): guest en0 is link-local 169.254.93.37 — "
        "restart to recover: yoloai stop embrace && yoloai start embrace"
    )
    hits = scan_fingerprints(_make_attempt(tmp_path), extra_text=reason)
    assert not any(h.fp.label.startswith("harness timeout") for h in hits)


# --- retry suppression ------------------------------------------------------


def test_is_wedge_failure_matches_the_raise_message() -> None:
    reason = (
        "guest network dead (vmnet wedge): guest en0 is link-local 169.254.93.37 — "
        "restart to recover: yoloai stop embrace && yoloai start embrace"
    )
    assert is_wedge_failure(reason)


def test_is_wedge_failure_false_for_unrelated_reasons() -> None:
    assert not is_wedge_failure("sentinel 'done' not seen in 180s (log: /tmp/x.log)")
    assert not is_wedge_failure("agent idle for 9s+ without sentinel 'done'")

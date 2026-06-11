# ABOUTME: Tests for the smoke-harness run-id namespace (base62, collision-safe)
# ABOUTME: and the orphan sweep's pure name-matching logic.

from __future__ import annotations

import re

import pytest

from smoke_test import (
    _B62_ALPHABET,
    _b62,
    _belongs_to_run,
    _is_smoke_name,
    _new_run_id,
    _select_smoke_names,
    _smoke_store_names,
)

# Run ids are "ysmk-<10 base62 chars>"; sandbox names append "-<label>-<NNN>".
RUN = "ysmk-1Ly3kq9fXz"
PRIOR = "ysmk-1Ly3aa0000"


# --- base62 + run-id construction ------------------------------------------

def test_b62_is_fixed_width_and_big_endian() -> None:
    assert _b62(0, 4) == "0000"
    assert _b62(61, 1) == "z"
    assert _b62(62, 2) == "10"
    assert _b62(62 * 62 - 1, 2) == "zz"
    assert all(c in _B62_ALPHABET for c in _b62(123456789, 6))


def test_b62_is_chronologically_sortable_at_fixed_width() -> None:
    earlier, later = _b62(1781157448, 6), _b62(1781157449, 6)
    assert earlier < later  # equal width → lexicographic order == numeric order


def test_b62_rejects_overflow_and_negatives() -> None:
    with pytest.raises(ValueError):
        _b62(62**2, 2)  # needs 3 chars
    with pytest.raises(ValueError):
        _b62(-1, 4)


def test_new_run_id_shape_and_validity() -> None:
    rid = _new_run_id()
    assert re.fullmatch(r"ysmk-[0-9A-Za-z]{10}", rid), rid
    # A real sandbox name built from it is recognized as smoke-owned.
    assert _is_smoke_name(f"{rid}-tag-transfer-containerd-vmenhanced-000")


def test_new_run_id_worst_case_fits_56_char_cap() -> None:
    # Longest label observed: tag-transfer on the longest backend label.
    worst = f"{_new_run_id()}-tag-transfer-containerd-vmenhanced-000"
    assert len(worst) <= 56, f"{worst} is {len(worst)} chars"


# --- name matching ----------------------------------------------------------

def test_is_smoke_name_matches_sandbox_and_backend_instance() -> None:
    assert _is_smoke_name(f"{RUN}-stop-start-tart-002")          # sandbox
    assert _is_smoke_name(f"yoloai-{RUN}-stop-start-tart-002")   # Tart VM
    assert _is_smoke_name(f"yoloai-{RUN}-warmup-tart")           # warm-up VM


def test_is_smoke_name_rejects_real_boxes_and_base_images() -> None:
    assert not _is_smoke_name("yoloai-base")
    assert not _is_smoke_name("ghcr.io/cirruslabs/macos-tahoe-base:latest")
    assert not _is_smoke_name("yoloai-my-real-project")
    assert not _is_smoke_name("ysmk-mybox")          # token isn't 10 chars + '-'
    assert not _is_smoke_name(RUN)                    # bare run id, no sandbox suffix
    assert not _is_smoke_name(f"preysmk-{RUN[5:]}-x")  # token must be at a boundary
    assert not _is_smoke_name("smoke-1781157448-x")  # legacy scheme is no longer ours


def test_belongs_to_run() -> None:
    assert _belongs_to_run(f"yoloai-{RUN}-stop-start-tart-002", RUN)
    assert _belongs_to_run(f"{RUN}-clone-b", RUN)
    assert not _belongs_to_run(f"yoloai-{PRIOR}-stop-start-tart-002", RUN)


def test_select_prior_excludes_current_run() -> None:
    names = [
        f"{RUN}-stop-start-tart-001",          # this run — excluded
        f"yoloai-{PRIOR}-stop-start-tart-002",  # prior run — selected
        "yoloai-base",                          # not smoke
    ]
    assert _select_smoke_names(names, RUN, "prior") == [
        f"yoloai-{PRIOR}-stop-start-tart-002"
    ]


def test_select_current_takes_only_this_run() -> None:
    names = [
        f"{RUN}-a-tart-001",
        f"yoloai-{RUN}-warmup-tart",
        f"yoloai-{PRIOR}-b-tart-002",
    ]
    assert _select_smoke_names(names, RUN, "current") == [
        f"{RUN}-a-tart-001",
        f"yoloai-{RUN}-warmup-tart",
    ]


def test_select_dedups_and_preserves_order() -> None:
    names = [f"yoloai-{PRIOR}-a", f"yoloai-{PRIOR}-a", f"yoloai-{PRIOR}-b"]
    assert _select_smoke_names(names, RUN, "prior") == [
        f"yoloai-{PRIOR}-a",
        f"yoloai-{PRIOR}-b",
    ]


def test_smoke_store_names_reads_ls_json_shape() -> None:
    data = {
        "sandboxes": [
            {"environment": {"name": f"{PRIOR}-stop-start-podman-000", "backend": "podman"}},
            {"environment": {"name": f"{RUN}-x-tart-001", "backend": "tart"}},
            {"environment": {"name": "my-real-box", "backend": "docker"}},
        ]
    }
    assert _smoke_store_names(data, RUN, "prior") == [f"{PRIOR}-stop-start-podman-000"]


def test_smoke_store_names_tolerates_missing_fields() -> None:
    assert _smoke_store_names({}, RUN, "prior") == []
    assert _smoke_store_names({"sandboxes": [{}]}, RUN, "prior") == []
    assert _smoke_store_names({"sandboxes": [{"environment": {}}]}, RUN, "prior") == []

# ABOUTME: Tests entrypoint network validation ordering outside isolated mode.
"""Tests for the entrypoint's custom-DNS validation boundary."""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path
from types import ModuleType

import pytest

_RESOURCES_DIR = Path(__file__).resolve().parent.parent


def _load_entrypoint() -> ModuleType:
    firewall_spec = importlib.util.spec_from_file_location(
        "firewall", str(_RESOURCES_DIR / "firewall.py")
    )
    assert firewall_spec is not None and firewall_spec.loader is not None
    firewall = importlib.util.module_from_spec(firewall_spec)
    sys.modules["firewall"] = firewall
    firewall_spec.loader.exec_module(firewall)
    spec = importlib.util.spec_from_file_location(
        "entrypoint", str(_RESOURCES_DIR / "entrypoint.py")
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


entrypoint = _load_entrypoint()


def test_isolate_network_rejects_custom_dns_mismatch_before_open_mode_returns(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Open networking still fails closed before firewall setup is considered."""
    calls: list[object] = []

    def reject(expected: list[str], _log: object) -> list[str]:
        calls.append(expected)
        raise RuntimeError("resolver mismatch")

    import firewall

    monkeypatch.setattr(firewall, "verified_nameservers", reject)
    with pytest.raises(RuntimeError, match="resolver mismatch"):
        entrypoint.isolate_network({"dns": ["1.1.1.1"], "network_isolated": False})
    assert calls == [["1.1.1.1"]]

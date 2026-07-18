# ABOUTME: Unit tests for firewall.py's nameserver parsing — the DF134 fix that
# ABOUTME: skips non-IPv4 nameservers so a stray v6 entry can't abort v4 isolation.
"""Tests for the shared network-isolation firewall helpers (firewall.py).

firewall.py is loaded via importlib (rather than a plain import) so this test
needs no conftest.py — a second conftest under runtime/ would collide with the
monitor suite's under the single `mypy --strict runtime/` pass (see the Makefile
python-typecheck note).
"""

from __future__ import annotations

import importlib.util
from pathlib import Path
from types import ModuleType

_RESOURCES_DIR = Path(__file__).resolve().parent.parent


def _load_firewall() -> ModuleType:
    spec = importlib.util.spec_from_file_location(
        "firewall", str(_RESOURCES_DIR / "firewall.py")
    )
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


firewall = _load_firewall()


def _noop_log(event: str, msg: str, **fields: object) -> None:
    return None


def test_is_ipv4_accepts_v4_rejects_v6_and_junk() -> None:
    assert firewall.is_ipv4("8.8.8.8") is True
    assert firewall.is_ipv4("127.0.0.1") is True
    assert firewall.is_ipv4("fd00::1") is False
    assert firewall.is_ipv4("2001:4860:4860::8888") is False
    assert firewall.is_ipv4("not-an-ip") is False


def test_parse_nameservers_keeps_v4_drops_v6() -> None:
    lines = [
        "nameserver 8.8.8.8\n",
        "nameserver fd00::1\n",
        "nameserver 1.1.1.1\n",
        "search example.com\n",
    ]
    assert firewall.parse_nameservers(lines, _noop_log) == ["8.8.8.8", "1.1.1.1"]


def test_parse_nameservers_logs_each_skipped_v6() -> None:
    skipped: list[str] = []

    def log(event: str, msg: str, **fields: object) -> None:
        if event == "network.nameserver_non_ipv4_skipped":
            skipped.append(str(fields.get("nameserver")))

    lines = [
        "nameserver fe80::1\n",
        "nameserver 9.9.9.9\n",
        "nameserver 2606:4700:4700::1111\n",
    ]
    assert firewall.parse_nameservers(lines, log) == ["9.9.9.9"]
    assert skipped == ["fe80::1", "2606:4700:4700::1111"]


def test_parse_nameservers_all_v6_returns_empty() -> None:
    # DF134: an all-v6 resolv.conf yields no usable nameservers here; the empty
    # result trips read_nameservers' loud no-nameservers warning rather than
    # aborting the whole firewall install (the pre-fix behavior).
    assert firewall.parse_nameservers(["nameserver fd00::1\n"], _noop_log) == []

# ABOUTME: pytest configuration: adds runtime/monitor to sys.path so tests can
# ABOUTME: import setup_helpers (and the hyphenated scripts via importlib).
"""Shared pytest configuration for runtime/monitor tests.

`setup_helpers.py` lives in `runtime/monitor/`. Adding that directory to
`sys.path` lets tests do a plain `import setup_helpers`. The hyphenated
script names (`sandbox-setup.py`, `status-monitor.py`) cannot be imported
directly; if a test ever needs one, use `importlib.util.spec_from_file_location`.
"""

from __future__ import annotations

import sys
from pathlib import Path

_MONITOR_DIR = Path(__file__).resolve().parent.parent
if str(_MONITOR_DIR) not in sys.path:
    sys.path.insert(0, str(_MONITOR_DIR))

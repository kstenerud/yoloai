# ABOUTME: pytest configuration for the smoke-harness tests: adds scripts/ to
# ABOUTME: sys.path so tests can `import smoke_test` directly.
"""Shared pytest configuration for scripts/ tests.

`smoke_test.py` lives in `scripts/`. Adding that directory to `sys.path`
lets tests do a plain `import smoke_test`.
"""

from __future__ import annotations

import sys
from pathlib import Path

_SCRIPTS_DIR = Path(__file__).resolve().parent.parent
if str(_SCRIPTS_DIR) not in sys.path:
    sys.path.insert(0, str(_SCRIPTS_DIR))

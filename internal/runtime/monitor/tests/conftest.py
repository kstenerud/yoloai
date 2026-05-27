# ABOUTME: pytest configuration: adds runtime/monitor to sys.path so tests can
# ABOUTME: import setup_helpers (and the hyphenated scripts via importlib).
"""Shared pytest configuration for runtime/monitor tests.

`setup_helpers.py` and `tmux_io.py` live in `runtime/monitor/`. Adding
that directory to `sys.path` lets tests do plain `import setup_helpers`
or `import tmux_io`. The hyphenated script names (`sandbox-setup.py`,
`status-monitor.py`) cannot be imported directly; the
`load_sandbox_setup` helper below uses `importlib.util` to load them.
"""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path
from types import ModuleType

_MONITOR_DIR = Path(__file__).resolve().parent.parent
if str(_MONITOR_DIR) not in sys.path:
    sys.path.insert(0, str(_MONITOR_DIR))


def load_sandbox_setup() -> ModuleType:
    """Load sandbox-setup.py as a Python module.

    The hyphen in the filename prevents a plain `import`. Tests that need
    to drive its threading functions (launch_agent, run_lifecycle_background)
    call this helper. The module is loaded once per call — that's fine
    because the test-supplied tmux_io runner is the swappable state, not
    anything module-level inside sandbox-setup.py.
    """
    spec = importlib.util.spec_from_file_location(
        "sandbox_setup",
        str(_MONITOR_DIR / "sandbox-setup.py"),
    )
    if spec is None or spec.loader is None:
        raise RuntimeError("could not locate sandbox-setup.py for tests")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod

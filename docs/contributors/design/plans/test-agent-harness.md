> **ABOUTME:** Replace the plain-`bash` test agent with a controllable harness that simulates
> real agent workflows, so the idle-detection pipeline gets real integration test coverage.

# Test agent harness

- **Status:** UNSPECIFIED — idea only; spec TBD.
- **Depends on:** —

Replace the current test agent (plain `bash`) with a proper test harness process that simulates real agent workflows: startup sequence, accepting input, simulating work, transitioning to idle, and controllable exit. Should support mimicking different detection strategies (hook-based, pattern-based, context signals) via environment variables or commands, enabling integration testing of the full idle detection pipeline. Spec TBD.

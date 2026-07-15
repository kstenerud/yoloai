> **ABOUTME:** Let profile `config.yaml` declare a `detectors` list that replaces the
> auto-resolved idle-detector stack, so users can disable noisy detectors or reorder them.

# User-overridable detector config

- **Status:** UNSPECIFIED — idea only; config schema undecided.
- **Depends on:** —

Allow users to override the auto-resolved detector stack via profile-level config. A `detectors` list in profile `config.yaml` would replace the automatically computed stack, letting users disable noisy detectors or change priority order. No CLI flag — config file only.

See [idle detection research](../research/idle-detection.md) §3.9 Q1.

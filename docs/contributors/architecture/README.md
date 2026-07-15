> **ABOUTME:** Entry point for the architecture directory — code-as-built navigation docs, as
> opposed to `design/` which covers aspirational features. Routes a "how do the pieces fit" or
> "where does this live" question to the right file here; keep those files in sync as the
> architecture changes.

# Architecture

Code-navigation docs for the yoloAI codebase — the source of truth for **where the implemented code lives** and **how it behaves at runtime**. Focused on the code as built, not aspirational features (see [design/](../design/README.md) for those).

## Files in this directory

- **[overview.md](overview.md)** — the conceptual, diagram-first companion: layer diagram, backend plugin model, sandbox lifecycle, workdir modes, create and diff/apply flows, configuration hierarchy, capability detection. Start here for *how the pieces fit*.
- **[code-map.md](code-map.md)** — the package map, per-file index, key public types, and the command→code dispatch table. Start here for *where something lives*.
- **[data-flows.md](data-flows.md)** — runtime call chains for the main operations (create, runtime init, diff, apply, overlay mount, start/restart, doctor, prune classification).
- **[host-layout.md](host-layout.md)** — the on-disk `~/.yoloai/` directory structure and what each path holds.
- **[where-to-change.md](where-to-change.md)** — recipes: "to change X, edit these files."
- **[testing.md](testing.md)** — test tiers (unit / integration / e2e), backend conformance, per-host coverage, and test infrastructure.

Keep these in sync when the architecture changes.

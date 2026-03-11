# Multi-Agent Workflows: Design Sketch

**Status:** Early sketch. Needs significant design work before implementation.

## The Problem

yoloAI's workdir model treats a directory as a unit: one mode (`:copy`, `:overlay`, `:rw`, or read-only) applies to the entire directory. This works perfectly for a single agent working on a single project.

When multiple agents need to collaborate on the same project with different roles, we want:

- A test-writing agent that focuses on `tests/` and doesn't touch `src/`.
- An implementing agent that focuses on `src/` and doesn't modify `tests/`.
- A reviewing agent that sees everything but modifies nothing.
- All agents see the full project tree (imports, configs, build files).

## Key Insight: This Mostly Works Today

The multi-agent authority-split workflow is achievable with current yoloAI primitives and prompt-based role scoping:

```
yoloai new red /path/to/project:copy \
    --prompt "Write failing tests in tests/. Do not modify anything in src/."
yoloai new green /path/to/project:copy \
    --prompt "Implement in src/ to make tests pass. Do not modify tests/."
yoloai new review /path/to/project \
    --prompt "Review the codebase for issues."
```

Each sandbox gets its own `:copy` of the project with its own `.git/`. Agents commit freely within their sandbox. The sandbox's git state never leaks back to the host. When done:

```
yoloai apply red    # lands tests/ changes on host
yoloai apply green  # lands src/ changes on host
```

Since the agents worked on non-overlapping paths (by convention via prompts), patches apply cleanly in any order. No merge logic, no consolidation step, no new features needed.

**The review gate catches violations.** If an agent ignores the prompt and modifies files outside its designated scope, `yoloai diff` shows it. The user can reject the change or apply selectively with `yoloai apply red -- tests/` to land only the intended paths. The diff/apply workflow is already the enforcement mechanism — it catches violations after the fact rather than preventing them.

### What's Missing: Cross-Visibility

The one thing that doesn't work today is agents seeing each other's work. Each sandbox starts from the same host snapshot, but they're isolated — red can't see green's implementation, green can't see red's tests.

For **sequential workflows** (write tests, THEN implement), this is fine. Red finishes, you apply its changes, then create green from the updated host state.

For **parallel workflows**, agents are blind to each other. This limits use cases where agents need to react to each other's output in real time.

## Design Goals

1. **Incidental cross-visibility.** Agents can see each other's work as a natural consequence of how you compose the sandboxes, not through a baked-in coordination protocol. No `--reads-from` flags, no dependency graphs, no orchestration layer.

2. **Composability with existing primitives.** Uses the `-d` flag, mount modes, and existing sandbox layout. Doesn't introduce new concepts beyond what's necessary.

3. **Diff/apply still works.** Changes are reviewable and gated. Nothing lands on the host without explicit approval.

4. **Filesystem-enforced write restriction is a future hardening option**, not a prerequisite. Prompt-based role scoping + diff review is the baseline. `--writable` is a possible later addition for users who want kernel-enforced boundaries.

## Concept: Incidental Cross-Visibility

Each sandbox's work directory already lives on the host in a predictable location:

```
~/.yoloai/sandboxes/<name>/work/<encoded-path>/
```

Another sandbox can mount a path from that location using the existing `-d` flag:

```
yoloai new red /path/to/project:copy \
    --prompt "Write failing tests in tests/. Do not modify src/."

yoloai new green /path/to/project:copy \
    -d ~/.yoloai/sandboxes/red/work/<encoded>/tests/:rw=/path/to/project/tests/ \
    --prompt "Implement in src/ to make tests pass. Do not modify tests/."
```

This mounts red's `tests/` directory into green's container at the expected path. Green sees red's test files appear in real-time. No coordination protocol — just filesystem paths.

The mount mode on the cross-reference controls the relationship:
- `:rw` — green can see AND modify red's tests (bidirectional)
- (no suffix, read-only) — green can see red's tests but not modify them (unidirectional)

### The Ergonomic Problem

The raw path (`~/.yoloai/sandboxes/red/work/^2Fhome^2Fuser^2Fmy-app/tests/`) is ugly. Some options:

**Option A: Convenience shorthand.** A `@sandbox` prefix that expands to the sandbox's work directory:

```
yoloai new green /path/to/project:copy \
    -d @red/tests/=/path/to/project/tests/
```

Where `@red/tests/` resolves to the host-side location of red's `tests/` subdirectory within its work copy. This is sugar, not a coordination primitive. The expansion is deterministic and inspectable.

**Option B: A query command.** Instead of baking shorthand into the CLI syntax:

```
yoloai workdir red tests/
# outputs: /Users/me/.yoloai/sandboxes/red/work/^2Fhome^2Fuser^2Fmy-app/tests/
```

Then compose with shell:

```
yoloai new green /path/to/project:copy \
    -d "$(yoloai workdir red tests/)=/path/to/project/tests/"
```

More unix-y. No magic syntax. But verbose.

**Option C: Don't solve it yet.** Document the raw paths. Let power users compose manually. See if the pattern gets enough use to justify sugar.

### Live vs. Snapshotted Visibility

Two fundamentally different modes of cross-visibility:

**Live (bind mount):** Green sees red's changes as they happen. If red writes a new test file, green can read it immediately. This enables real-time collaboration but couples the sandbox lifecycles — red must exist (work directory intact) for green to see anything.

**Snapshotted (copy at creation time):** Green gets a copy of red's current state when green is created. Changes red makes after green starts are not visible. Simpler mental model but requires sequential workflows (red finishes, then green starts).

The bind mount approach is more powerful and is what makes cross-visibility "incidental" — you're just pointing at a directory on the host. Since sandbox work directories persist until the sandbox is destroyed, it works naturally.

### Coordination Without Coordination

The interesting property of this model: there is no coordination protocol, but coordination emerges from the filesystem layout.

- **Sequential pipeline:** Create `red`, let it finish, create `green` pointing at red's work. Green sees red's final output. Review and apply each independently.

- **Parallel with live visibility:** Create `red` and `green` simultaneously, each mounting the other's work directory via `-d`. Both see each other's changes in real-time. Since each agent writes to non-overlapping paths (by prompt convention), no conflicts arise.

- **Fan-out:** Create multiple sandboxes with the same project and prompt. Each produces an independent version. Compare diffs, pick the best.

- **Review:** Create a read-only sandbox mounting another sandbox's work directory. The review agent reads but cannot modify.

None of these require yoloAI to know that the sandboxes are related. The user composes the relationships through mount paths.

## Diff/Apply in the Multi-Agent World

### What Doesn't Change

- `yoloai diff <sandbox>` still shows what that sandbox changed relative to its baseline.
- `yoloai apply <sandbox>` still lands changes to the host project via `format-patch` + `git am`.
- Each sandbox's changes are independently reviewable and applicable.
- Non-overlapping changes (enforced by prompt convention) guarantee clean patch application in any order.

### Git Consolidation Is Already Solved

Each sandbox has its own `.git/` copy (existing `:copy` behavior). Each agent commits independently within its sandbox. `apply` extracts patches via `format-patch` and applies them to the host with `git am --3way`. Since the patches touch non-overlapping paths (by convention), they apply cleanly in any order.

No merge logic, no consolidation step, no `.git/` sharing between sandboxes. The existing diff/apply workflow handles multi-agent consolidation as a natural consequence of non-overlapping changes.

### Selective Apply for Boundary Violations

If an agent strays outside its designated scope (ignores the "only touch tests/" instruction), the user can apply selectively:

```
yoloai apply red -- tests/    # only land changes in tests/, ignore anything else
```

This already works via the path-filtering support in `apply`. The review gate + selective apply is soft enforcement — it catches and corrects violations rather than preventing them.

### What Gets Awkward

**Composite diff.** After multiple agents have worked, the user might want a combined view:

```
yoloai diff red    # just red's changes
yoloai diff green  # just green's changes
# user wants combined view... how?
```

Maybe `yoloai diff red green` shows both? Or maybe this is a non-problem — reviewing each agent's changes independently is actually better for trust and attribution.

### Agent-Driven Patch Merging

Instead of applying each sandbox's changes directly to the host, export patches and hand them to a merger agent:

```
yoloai apply red --patches /tmp/merge-job/red/
yoloai apply green --patches /tmp/merge-job/green/

yoloai new merger /path/to/project:copy \
    -d /tmp/merge-job/ \
    --prompt "Apply the patches in /tmp/merge-job/ to the project. Resolve any conflicts."
```

The merger agent has the full project as a `:copy` workdir plus all patch files as read-only input. It can inspect the patches, decide ordering, resolve conflicts in cross-cutting files, and commit the combined result. Then `yoloai diff merger` shows the unified outcome and `yoloai apply merger` lands it on the host in a single step.

This uses only existing primitives (`apply --patches`, `-d`, `:copy`). No new features needed.

**Why this is interesting:**

- **Solves cross-cutting files.** If both agents touched `go.mod`, the merger agent reconciles both changes rather than one patch failing to apply. The merger sees the full picture and can make intelligent decisions about combining the changes.
- **Solves composite diff.** Instead of needing `yoloai diff red green`, the user reviews `yoloai diff merger` — one unified diff showing the combined outcome of all agents' work.
- **Solves ordering.** The merger agent decides the right patch application order, not the user.
- **Adds a review layer.** The merger agent is effectively a code-aware merge tool. It can flag problems that mechanical `git am` would miss.
- **Composable.** Works with any number of input sandboxes. Fan out to N agents, collect patches, merge in one step.

The tradeoff is an extra sandbox and agent invocation. For simple non-overlapping changes, sequential `yoloai apply` is simpler. The merger pattern shines when changes overlap or when the user wants a single reviewed result from multiple agents.

### Cross-Cutting Files

Some changes inherently cross boundaries. Agent red needs a new test dependency in `go.mod`. Agent green needs a new library in `go.mod`. Both changes are legitimate but neither agent "owns" `go.mod`.

Options:

1. **Agent-driven merge.** Export patches from both agents, hand them to a merger agent that reconciles conflicts (see above). The merger sees both changes and can combine them intelligently.

2. **Accept the constraint.** Cross-cutting files are handled by a human or a third "setup" agent with broader scope. This forces clean role separation.

3. **A prep/teardown step.** A setup sandbox runs first to install dependencies, update configs, etc. Role-scoped agents run after, building on the prep work.

4. **Let both agents modify it.** If both agents touch `go.mod`, one apply may conflict with the other. The user resolves it manually. This is the normal git workflow — not a new problem.

Option 1 is the most powerful. Options 2-4 are simpler fallbacks depending on the situation. No new mechanism needed for any of them.

## Future Hardening: `--writable` Flag

For users who want kernel-enforced write restrictions (not just prompt-based conventions), a future `--writable` flag could restrict filesystem writes to designated paths:

```
yoloai new red /path/to/project --writable tests/
yoloai new green /path/to/project --writable src/
```

This would use the same `:copy` flow (full project copy, own `.git/`) but apply filesystem-level restrictions so writes outside designated paths fail with EROFS.

### Why This Is Optional, Not Foundational

- **Prompt-based scoping works for most cases.** Agents generally follow "only touch X" instructions. When they don't, `yoloai diff` catches it and `apply -- <path>` filters it.
- **The diff/apply review gate is already enforcement.** Violations are caught after the fact, which is sufficient when the review step is mandatory anyway.
- **Kernel enforcement adds implementation complexity.** Possible approaches (chmod-based, bind-mount layering, overlayfs per-subdirectory) each have tradeoffs in complexity, portability, and interaction with agent tooling.
- **Community signal is mixed.** The HN discussion (2026-03) had one practitioner report that filesystem enforcement "cuts out a surprising amount of self-grading behavior," but the dominant sentiment was that simple setups win and complex harnesses are unproven.

If the pattern sees real adoption with prompt-based scoping, `--writable` becomes a natural hardening step. Build it when users ask for it, not before.

### Implementation Options (When Needed)

- **chmod-based:** Copy full project, then `chmod -R a-w` on non-writable paths inside the container. Simple but soft — root in the container can bypass.
- **Bind-mount layering:** Mount project root read-only from host, bind-mount writable copies of designated paths plus `.git/` on top. Kernel-enforced. More complex mount setup.
- **Per-subdirectory overlay:** Mount project root read-only, use overlayfs per writable path. Instant setup, space-efficient. Linux-only, requires `CAP_SYS_ADMIN`.

## Alternative Models (Explored, Not Recommended)

### Shared Mutable Workdir

Multiple sandboxes share a single `:copy` workdir with filesystem-level access controls.

**Rejected because:** Concurrent git operations risk index corruption. Diff attribution is lost. Conflicts with yoloAI's isolation model.

### Ephemeral Writable Layer

Mount project read-only, overlay writable paths with empty upper layers. Reads fall through; writes land in upper layer.

**Deferred because:** Same platform constraints as `:overlay`. Git baseline management is complex with split layers. Could be a future optimization for the `--writable` implementation.

## What Needs More Thought

1. **Ergonomics of cross-sandbox mounting.** The `@sandbox` shorthand vs. query command vs. raw paths question. This determines how accessible the cross-visibility pattern is to non-power-users.

2. **Is cross-sandbox visibility actually needed?** The sequential pipeline (red finishes → apply → create green from updated host) works today with no new features. The only thing it lacks is parallelism. For the common TDD workflow (write tests THEN implement), sequential is natural.

3. **Extension system interaction.** Could the authority-split TDD pattern be expressed as an extension using existing primitives, without any new CLI features? If so, ship the extension and defer the design work.

4. **Scope creep toward orchestration.** Every step toward multi-agent coordination moves yoloAI closer to being an orchestrator. The research shows the orchestrator space is crowded (60+ tools) and rapidly evolving. yoloAI's value is the sandbox layer. Where's the line between "composable primitives" and "accidental orchestrator"?

## Research References

- [Agentic Workflows research](../dev/research/agentic-workflows.md) — HN discussion analysis, TDD subagent patterns, authority splitting, review gap
- [Parallel Agents research](../dev/research/parallel-agents.md) — coordination patterns, sandbox chaining, spec-driven development
- [Orchestration research](../dev/research/orchestration.md) — ecosystem tools, idle detection, don't-build-an-orchestrator argument

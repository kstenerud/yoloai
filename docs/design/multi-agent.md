# Multi-Agent Workflows: Design Sketch

**Status:** Early sketch. Needs significant design work before implementation.

## The Problem

yoloAI's workdir model treats a directory as a unit: one mode (`:copy`, `:overlay`, `:rw`, or read-only) applies to the entire directory. This works perfectly for a single agent working on a single project.

When multiple agents need to collaborate on the same project with different roles, the workdir-as-unit model breaks down:

- A test-writing agent needs to write to `tests/` but shouldn't touch `src/`.
- An implementing agent needs to write to `src/` but shouldn't modify `tests/`.
- A reviewing agent should see everything but modify nothing.
- All agents need to see the full project tree (imports, configs, build files).

Currently you'd either give each agent full `:copy` access (no authority splitting) or split the project into separate `-d` mounts (agents lose the unified project view).

## Design Goals

1. **Fine-grained write permissions within a workdir.** An agent sees the full project tree but can only write to designated paths. Enforced by the filesystem, not by prompts.

2. **Incidental cross-visibility.** Agents can see each other's work as a natural consequence of how you compose the sandboxes, not through a baked-in coordination protocol. No `--reads-from` flags, no dependency graphs, no orchestration layer.

3. **Composability with existing primitives.** Uses the `-d` flag, mount modes, and existing sandbox layout. Doesn't introduce new concepts beyond what's necessary.

4. **Diff/apply still works.** Changes are reviewable and gated. Nothing lands on the host without explicit approval.

## Concept: Writable Paths

Mount the workdir read-only by default. Designate specific subdirectories as writable.

```
yoloai new red /path/to/project --writable tests/
yoloai new green /path/to/project --writable src/
yoloai new review /path/to/project
```

What happens mechanically:

- The project root is bind-mounted **read-only** into the container at the mirrored host path (same as a default aux dir today).
- Each `--writable` path gets a **copy** stored in the sandbox's `work/` directory on the host (like `:copy` does today, but for a subdirectory rather than the whole tree).
- The copy is bind-mounted **read-write** on top of the read-only base at the correct subpath. The agent sees the full tree; writes to non-writable paths fail silently or with EROFS.
- Git baseline is established for each writable path for diff purposes.

`yoloai diff red` shows changes only in `tests/` — the only place writes could land. `yoloai apply red` lands those changes to the host's `tests/` directory.

### Open Questions: Writable Paths

- **Granularity.** Subdirectories are the natural unit. But what about single files? File-glob patterns? At some point the complexity of specifying permissions exceeds the value.

- **Multiple writable paths.** `--writable src/ --writable docs/` should work. Each gets its own tracked copy. But how does `diff` present changes across multiple paths? Unified diff? Per-path?

- **Interaction with `:copy`.** If the user says `/path/to/project:copy --writable tests/`, what wins? Is `--writable` only valid without an explicit mode (i.e., it implies read-only base)? Or can you combine it with `:copy` to mean "everything is writable but tests/ is separately tracked"? Probably the former — `--writable` implies read-only base.

- **The `.git/` directory.** The read-only base mount includes `.git/`. The agent can't write to it. This means no `git commit`, no `git stash`, etc. inside the container. For many agent workflows this is fine (the agent just edits files), but some agents use git internally. Is this a problem? Could mount `.git/` as a writable copy if needed, but that adds complexity.

- **Overlay as alternative.** Instead of copying the writable subdirectory, could use overlayfs to make specific paths writable. Lower layer = host directory (read-only), upper layer = sandbox work dir. Avoids the copy cost. But adds the same platform constraints as `:overlay` mode (Linux-only, `CAP_SYS_ADMIN`).

## Concept: Incidental Cross-Visibility

Once writable paths are stored in a predictable host-side location, cross-sandbox visibility falls out naturally. The writable layer for sandbox `red`'s `tests/` directory lives on the host at:

```
~/.yoloai/sandboxes/red/work/<encoded-path>/
```

Another sandbox can mount that path using the existing `-d` flag:

```
yoloai new green /path/to/project --writable src/ \
    -d ~/.yoloai/sandboxes/red/work/<encoded>/tests/=/path/to/project/tests/
```

This mounts red's test output at the expected path inside green's container. Green sees the full project tree with red's live test files overlaid on the read-only base. No coordination protocol — just filesystem paths.

### The Ergonomic Problem

The raw path (`~/.yoloai/sandboxes/red/work/^2Fhome^2Fuser^2Fmy-app/tests/`) is ugly. Some options:

**Option A: Convenience shorthand.** A `@sandbox` prefix that expands to the sandbox's work directory:

```
yoloai new green /path/to/project --writable src/ \
    -d @red/tests/=/path/to/project/tests/
```

Where `@red/tests/` resolves to the host-side location of red's writable `tests/` layer. This is sugar, not a coordination primitive. The expansion is deterministic and inspectable.

**Option B: A query command.** Instead of baking shorthand into the CLI syntax:

```
yoloai workdir red tests/
# outputs: /Users/me/.yoloai/sandboxes/red/work/^2Fhome^2Fuser^2Fmy-app/tests/
```

Then compose with shell:

```
yoloai new green /path/to/project --writable src/ \
    -d "$(yoloai workdir red tests/)=/path/to/project/tests/"
```

More unix-y. No magic syntax. But verbose.

**Option C: Don't solve it yet.** Document the raw paths. Let power users compose manually. See if the pattern gets enough use to justify sugar.

### Live vs. Snapshotted Visibility

Two fundamentally different modes of cross-visibility:

**Live (bind mount):** Green sees red's changes as they happen. If red writes a new test file, green can read it immediately. This enables real-time collaboration but couples the sandbox lifecycles — red must be running (or at least have its work directory intact) for green to see anything.

**Snapshotted (copy at creation time):** Green gets a copy of red's current state when green is created. Changes red makes after green starts are not visible. Simpler mental model but requires sequential workflows (red finishes, then green starts).

The bind mount approach is more powerful and is what makes cross-visibility "incidental" — you're just pointing at a directory. But it means both sandboxes need to exist simultaneously, and the work directory must persist.

Since sandbox work directories already persist on the host until the sandbox is destroyed, the bind mount approach works naturally. The user creates `red`, then creates `green` with a `-d` pointing at red's work directory. As red writes tests, green sees them appear.

### Coordination Without Coordination

The interesting property of this model: there is no coordination protocol, but coordination emerges from the filesystem layout.

- **Sequential pipeline:** Create `red`, let it finish, create `green` pointing at red's work. Green sees red's final output. Review and apply each independently.

- **Parallel with live visibility:** Create `red` and `green` simultaneously, each pointing at the other's writable layers. Both see each other's changes in real-time. Merge conflicts are the user's problem (but since each agent writes to non-overlapping paths, conflicts shouldn't arise if roles are properly split).

- **Fan-out:** Create multiple sandboxes with the same `--writable` path. Each produces an independent version. Compare diffs, pick the best.

- **Review:** Create a read-only sandbox pointing at another sandbox's work directory. The review agent reads but cannot modify.

None of these require yoloAI to know that the sandboxes are related. The user composes the relationships through mount paths.

## Diff/Apply in the Multi-Agent World

### What Doesn't Change

- `yoloai diff <sandbox>` still shows what that sandbox changed relative to its baseline.
- `yoloai apply <sandbox>` still lands changes to the host project.
- Each sandbox's changes are independently reviewable and applicable.

### What Gets Awkward

**Ordering.** If `red` wrote tests and `green` wrote implementation, you need to apply both. The order might matter if there are cross-dependencies (e.g., green's code imports a test helper that red created). Today, `apply` works against the host's current state, so:

```
yoloai apply red    # lands tests on host
yoloai apply green  # lands src on host (host now has red's tests too)
```

This works if the changes are to non-overlapping paths. If both agents touched the same file (shouldn't happen with proper `--writable` scoping), you get patch conflicts.

**The "promote" alternative.** Instead of applying to the host, what if you could promote one sandbox's changes into another sandbox's view? This would let you build up a combined result across multiple agents before applying once to the host.

Mechanically: `yoloai promote red green` would copy red's writable layer into green's read-only base (or add it as an additional layer). Green now sees red's changes as part of its baseline. Green's diff would show only green's own changes, not red's.

This is a new concept. It might be overengineering. The sequential `apply` approach probably works for most cases.

**Composite diff.** After multiple agents have worked, the user might want to see the combined diff before applying anything. This could be done by creating a temporary sandbox that mounts all the writable layers:

```
yoloai diff red    # just tests/ changes
yoloai diff green  # just src/ changes
# user wants combined view... how?
```

No obvious clean answer here. Maybe `yoloai diff red green` shows both? Or maybe this is a non-problem — reviewing each agent's changes independently is actually better for trust.

## Alternative Model: Shared Mutable Workdir

Instead of read-only base + writable holes, what if multiple sandboxes shared a single `:copy` workdir with filesystem-level access controls?

```
# Create a shared workdir
yoloai new base /path/to/project:copy

# Create agents that share base's workdir with different permissions
yoloai new red --share base --writable tests/
yoloai new green --share base --writable src/
```

All agents see the same copy. Changes by any agent are immediately visible to all others. `yoloai diff base` shows all accumulated changes.

**Advantages:** Single source of truth. No cross-mounting. One diff shows everything.

**Disadvantages:** Requires a new `--share` concept. The shared workdir is mutable by multiple agents — harder to reason about. Diff attribution is lost (who changed what?). This is closer to a traditional shared filesystem than yoloAI's isolation model.

This model might conflict with yoloAI's core principle of protecting originals. The shared workdir is a copy (so the original is safe), but within the sandbox cluster, there's no isolation between agents.

## Alternative Model: Ephemeral Writable Layer

What if the writable layer isn't a full copy of the subdirectory but an **overlay** that captures only the deltas?

For `:copy` the writable `tests/` layer would be a full copy of `tests/` from the host. For an overlay approach, the writable layer starts empty — reads fall through to the read-only base, writes go to the upper layer.

This is essentially `:overlay` mode applied per-subdirectory rather than per-directory.

**Advantages:** Instant setup (no copy). Space-efficient. Cross-visibility naturally works (the upper layer contains only changed files, which is all the other agent needs to see).

**Disadvantages:** Same platform constraints as `:overlay` (Linux, `CAP_SYS_ADMIN`). The delta-only layer is harder to work with for git-based diff (need the overlay mounted to see the merged view).

## What Needs More Thought

1. **Is `--writable` the right primitive?** It's simple but introduces a new mount concept. Could the same thing be achieved by composing existing modes differently? E.g., mount the project read-only as an aux dir, then mount subdirectories as separate `:copy` workdirs? That technically works today but the mental model is weird — you'd have multiple "workdirs" and lose the unified project view.

2. **`.git/` handling.** Many agents use git internally (committing, branching, diffing). A read-only `.git/` breaks this. Options: (a) just accept it — agents that need git won't work with `--writable` mode; (b) always make `.git/` writable; (c) provide a synthetic `.git/` in the writable layer that tracks changes. None are great.

3. **How does this interact with `:overlay` mode?** Overlay already does "read-only base + writable upper." Is `--writable` just a per-subdirectory version of `:overlay`? Could it share the same implementation? On platforms that support overlayfs, `--writable` could use overlayfs per-subdirectory. On others, fall back to copy.

4. **Is cross-sandbox visibility actually needed?** The sequential pipeline (red finishes → apply → create green from updated host) works today with no new features. The only thing it lacks is parallelism. Is the parallelism worth the added complexity? For the common TDD workflow (write tests THEN implement), sequential is natural.

5. **Extension system interaction.** yoloAI extensions already provide a composition mechanism for multi-sandbox workflows. Could the authority-split TDD pattern be expressed as an extension using existing primitives, without any new CLI features? If so, ship the extension and defer the design work.

6. **Scope creep toward orchestration.** Every step toward multi-agent coordination moves yoloAI closer to being an orchestrator. The research shows the orchestrator space is crowded (60+ tools) and rapidly evolving. yoloAI's value is the sandbox layer. Where's the line between "composable primitives" and "accidental orchestrator"?

## Research References

- [Agentic Workflows research](../dev/research/agentic-workflows.md) — HN discussion analysis, TDD subagent patterns, authority splitting, review gap
- [Parallel Agents research](../dev/research/parallel-agents.md) — coordination patterns, sandbox chaining, spec-driven development
- [Orchestration research](../dev/research/orchestration.md) — ecosystem tools, idle detection, don't-build-an-orchestrator argument

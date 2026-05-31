# Agentic Workflow Patterns and Community Sentiment

Research on how practitioners structure AI coding agent workflows, what works, what doesn't, and community attitudes toward autonomous agent execution. Conducted 2026-03.

**Sources:**
- [HN: Agents that run while I sleep](https://news.ycombinator.com/item?id=47327559) — 200+ comment discussion, 2026-03-10
- [Principled Agentic Software Development](https://www.joegaebel.com/articles/principled-agentic-software-development/) — Outside-in TDD workflow for agents
- [rlm-workflow](https://skills.sh/doubleuuser/rlm-workflow/rlm-workflow) — Claude Code skill with stage gating and TDD enforcement
- [outside-in-tdd-starter](https://github.com/JoeGaebel/outside-in-tdd-starter) — starter repo for outside-in TDD with agents

## 1. Community Sentiment on Autonomous Agents

### 1.1 The "Overnight Agent" Debate

Strong polarization. The article describes running agents unattended overnight; community pushback is significant.

**Skeptics (majority):**
- "Most of the time if I use claude it's done in 5-20 minutes. I've never wanted to have work done for me overnight." — mjrbrennan
- "Claude doesn't fucking work without human intervention. When left to its own devices it makes bad decisions. It writes bad code. It needs constant supervision." — zarzavat
- "I would encourage my competitors to use AI agents on their codebase as much as possible... Then when the music stops... they are left with no one who understands a sprawling tangled web of code." — brobdingnagians

**Defenders (minority):**
- Long build/test cycles (20+ min) justify unattended runs — tudelo, saguntum
- Integration testing loops during refactoring benefit from "send it off to work until the kinks are worked out" — saguntum

**Key takeaway:** The overnight use case is niche. Most practitioners get value from 5-20 minute supervised sessions. The unattended use case becomes compelling only when the feedback loop (build + test) is slow.

### 1.2 Cost Anxiety

Multiple data points on spending:
- "$200 in Claude in 3 days" for manual prompting — p0w3n3d
- "$200/month per developer" for a working team setup — throwaway7783
- "If you are not spending 5-10k dollars a month... you likely won't see interesting results" — aprdm
- "Fast mode that burns tokens at 6 times the rate is just scary" — cube00

Comparison to online ad spending resonated: paying more and hoping for results, with no clear ROI signal.

### 1.3 The Skill Gap Problem

Senior vs. junior perception diverges sharply:

"I was talking to a junior developer and they were telling me how Claude is so much smarter than them and they feel inferior. I couldn't relate. From my perspective as a senior, Claude is dumb as bricks." — zarzavat

Implication: juniors trust agent output uncritically and try to compensate with more tokens/agents. Seniors use agents as accelerators for tasks they already know how to do. The tool's value scales with the user's ability to review output.

### 1.4 Simple Harnesses Win

Strong consensus against complex agent frameworks:

- "You don't need most of this. A CLAUDE.md with instructions and notes about the repo and how to run stuff... Costs about $200 per developer per month." — throwaway7783
- "I'm perfectly happy running two simple agents, one for writing and one for reviewing. I don't need to go be writing code at faster than light speed." — recroad
- "If you follow what the actual designers of Claude/GPT tell you it flys in the face of building out over engineered harnesses for agents." — nojito

Counter-argument: Claude Code skills for TDD are "precisely what skills is meant for and is the opposite of an anti-pattern" — canadiantim

The pattern that works: CLAUDE.md instructions + natural-language prompts that read like what you'd say to another engineer. Complex multi-agent harnesses are unproven at scale.

## 2. Red-Green-Refactor with Subagents

### 2.1 The Pattern

Described by egeozcan with reported success. Uses Claude Code subagents with clean-room isolation rules:

| Role | Can See | Cannot See | Incentive |
|------|---------|------------|-----------|
| Red (test writer) | Spec, existing code | Green's implementation | Write tests that fail — reward is failing tests |
| Green (implementer) | Spec, Red's tests | Cannot modify tests | Make tests pass with minimal code |
| Refactor | All code and tests | N/A | Clean up without breaking tests |
| Coordinator | Everything | N/A | Orchestrate phases, enforce barriers |

**The key innovation is the visibility barrier.** Without it, Green could read the assertions and write trivially passing code. Without it, Red could write tests pre-adapted to the implementation. The barrier forces each role to work from the spec, not from each other's output.

### 2.2 Implementation via Bash Scripts

egeozcan shared a [pastebin example](https://pastebin.com/) of implementing clean-room rules via bash scripts that control what files each subagent can access. The exact mechanism: tell Claude Code to spawn subagents with explicit instructions about what files they may and may not read.

**Limitation:** This relies on the agent honoring instructions, not on actual filesystem enforcement. A misbehaving agent could ignore the "don't read tests" instruction. True enforcement requires filesystem-level isolation.

### 2.3 Skepticism and Failure Modes

- Agents write tests that don't actually fail, then say "the issue is already fixed" — SequoiaHope
- Tests assert that the test harness was set up correctly rather than testing behavior — magicalist
- "Useless tests start to grow in count and important new things aren't tested or aren't tested well" — magicalist
- Red team may be "incentivized to write tests that violate the spec since you're rewarding failed tests" — bcrosby95
- Mock injection is gameable: Green could "mock the entire implementation" — devinplatt

## 3. Authority Splitting via File Permissions

### 3.1 The Pattern

Described by pastescreenshot:

> "One agent can touch app code, one can only write failing tests plus a short bug hypothesis, and one only reviews the diff and test output. Also make test files read only for the coding agent. That cuts out a surprising amount of self-grading behavior."

This is enforcement at the filesystem level, not just prompt instructions. Making `tests/` read-only for the coding agent physically prevents it from modifying tests to match its implementation.

### 3.2 Relevance to Sandboxed Environments

In a Docker container, this is trivially enforceable via mount permissions:
- Mount `src/` as read-write, `tests/` as read-only for the implementation agent
- Mount `tests/` as read-write, `src/` as read-only for the test-writing agent
- Mount everything read-only for the review agent

This is stronger than prompt-based instructions because the agent literally cannot violate the constraint. It doesn't matter if the agent "decides" to modify a test file — the filesystem won't allow it.

## 4. Context Separation for Code Review

### 4.1 The Pattern

Multiple practitioners describe using separate agent sessions for writing vs. reviewing:

- "We also do code reviews with Claude Code, but in a separate session." — throwaway7783
- "Separation of concerns. No single agent plans, implements, and verifies. The agent that writes the code is never the agent that checks it." — kaizenb
- "Works for PR reviews. Separating context for code review with the same model has significant impact." — kaizenb

The rationale: an agent reviewing its own output is inherently compromised. A fresh session with no memory of the implementation decisions provides genuinely independent review.

### 4.2 Why Separate Sandboxes Work Better Than Separate Sessions

A separate Claude Code session on the same filesystem shares state (file contents, git history). A separate sandbox provides:
- No shared agent memory/context from the implementation phase
- Read-only mount of the workdir prevents the review agent from "fixing" issues it finds
- The diff between implementation sandbox and original serves as the review artifact
- Clear lifecycle: implementation sandbox produces changes, review sandbox evaluates them

## 5. TDD Skill Ecosystem

### 5.1 rlm-workflow

A Claude Code skill implementing a full repository lifecycle with 8 phases (0-7), stage gating via cryptographic lock hashes, and mandatory TDD compliance logs.

**Key mechanisms:**
- Git worktree isolation at `.worktrees/<run-id>/` before any work begins
- "Iron Law": no production code without a failing test first
- Lock chain validation: each phase must be cryptographically locked before the next can proceed
- Sub-agent support for parallel sub-phase execution
- Phase 5 (manual QA) is the only intentional pause — everything else is automated gates

**Assessment:** Ambitious but heavy. The cryptographic lock chain and 8-phase lifecycle add significant overhead. May work for large, well-specified features but is likely overkill for typical agent tasks (bug fixes, small features). The git worktree isolation is sound.

### 5.2 Outside-In TDD

Described by joegaebel. Uses specialized sub-agents (planner, test-writer, implementer, refactorer, test-backfiller) with context isolation via separate Claude Code sub-agent sessions.

**Key insight:** Start with a high-level feature test (often end-to-end), then work inward through nested RED-GREEN-REFACTOR cycles at lower test layers. Only conclude when the feature-complete test passes. Run mutation testing to catch gaps.

This inverts the typical AI pattern of "implement first, test after" which tends to produce tests coupled to implementation details rather than behavior.

### 5.3 Claude Code Hooks for Enforcement

cadamsdotcom describes using Claude Code hooks for deterministic enforcement:

> "Code and Claude Code hooks can conditionally tell the model anything... This lets you enforce anything from TDD to a ban on window.alert() in code — deterministically."

Hooks fire on tool use events and can block actions or inject feedback. This is lighter than a full skill framework and provides actual enforcement rather than prompt-based suggestions.

## 6. The Review Gap

### 6.1 The Core Trust Problem

The most consistent theme across the discussion: even when tests pass, you don't know if the code is good.

- "You don't really know what code has been written. You don't know the refactors are right, in alignment with existing patterns." — afro88
- "Tests can pass, the red/green approach can give thumbs up and rocket emojis all day long, and the code can still be shitty, brittle and riddled with security and performance flaws." — timr
- "I still think that we, programmers, having to pay money in order to write code is a travesty." — paganel (broader sentiment about the economics)

### 6.2 What Mitigates the Review Gap

From the discussion, ranked by practitioner confidence:

1. **Human review of diffs** — the highest-trust approach. Multiple practitioners cite diff review as non-negotiable.
2. **Separate review agent** — fresh context, no implementation bias. "Significant impact" reported.
3. **TDD with authority splitting** — filesystem-enforced separation prevents self-grading.
4. **Incremental/small changes** — easier to review than overnight bulk output.
5. **Structured specs** — high-quality input produces more reviewable output.

## 7. Implications for yoloAI

### 7.1 Copy/Diff/Apply Is the Trust Layer

The review gap is the #1 concern in the community. yoloAI's copy/diff/apply workflow directly addresses it:
- Changes are isolated in a sandbox copy — originals are never modified
- `yoloai diff` shows exactly what changed before anything lands
- `yoloai apply` is an explicit human approval gate
- This is the piece that prompt-based TDD skills, worktree tools, and orchestrators don't provide

This should be positioned as the primary value proposition, not just a convenience feature.

### 7.2 Multi-Sandbox Authority Splitting

yoloAI already supports the mount modes needed for authority splitting:

| Sandbox Role | `src/` mount | `tests/` mount | Agent task |
|-------------|-------------|----------------|------------|
| Test writer | read-only | `:copy` | Write failing tests |
| Implementer | `:copy` | read-only | Make tests pass |
| Reviewer | read-only | read-only | Review diffs |

This is filesystem-enforced, not prompt-enforced. The agent physically cannot violate the constraint. No other tool in the ecosystem provides this level of enforcement without container-level isolation.

### 7.3 Separate Sandboxes for Review

A natural workflow:
1. `yoloai new impl-feature-x /path/to/project:copy` — implementation sandbox
2. Agent writes code, tests pass
3. `yoloai diff impl-feature-x` — inspect changes
4. `yoloai new review-feature-x /path/to/project` — review sandbox (read-only)
5. Review agent examines the diff, writes findings
6. `yoloai apply impl-feature-x` — land changes after review

This provides context separation (different sandboxes = different agent sessions) with filesystem enforcement (review sandbox is read-only).

### 7.4 Don't Over-Engineer the Harness

Community consensus: simple setups win. CLAUDE.md + natural-language prompts + a safety net beats elaborate multi-agent frameworks. yoloAI should:
- Keep the CLI simple and composable
- Provide the safety net (sandbox isolation + diff/apply)
- Let users compose their own workflows with existing tools (skills, hooks, scripts)
- Document patterns (TDD split, review separation) rather than building them in

### 7.5 The Overnight Use Case

Niche but real. yoloAI's detached sandbox model supports this naturally:
- `yoloai new overnight-refactor /path/to/project:copy` with a prompt
- Agent runs unattended
- Next morning: `yoloai diff overnight-refactor` to review
- The diff/apply gate catches anything the agent got wrong

The trust issue is mitigated by the mandatory review step. Unlike worktree-based tools where changes land directly on a branch, yoloAI's copy mode ensures nothing reaches the original until explicitly approved.

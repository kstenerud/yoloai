# Parallel Agent Workflows

Research on multi-agent coordination patterns for AI coding agents, based on community practices and HN discussion (2025-06).

**Sources:**
- [Parallel coding agents with tmux and Markdown specs](https://schipper.ai/posts/parallel-coding-agents/) — blog post by a consultant running 4-8 Claude Code agents in parallel
- [HN discussion](https://news.ycombinator.com/item?id=47218318) — community feedback and alternative approaches

## Current Practice: Manual tmux Wrangling

The dominant pattern is manual: developers open multiple tmux windows, launch an agent in each, name windows by task, and visually track which agents need attention. The blog post describes a workflow with three agent roles (Planner, Worker, PM) coordinated through markdown spec files ("Feature Designs").

**Pain points reported by practitioners:**
- Cognitive load beyond 4-8 parallel agents — tracking state across agents degrades decision quality
- Merge conflicts when multiple agents touch overlapping files
- No automated way to know when an agent is idle vs. working
- Context window exhaustion from holistic exploration burns tokens fast
- Token costs (~$400/mo for heavy parallel Opus usage; weekly quotas exceeded by day 3-4)

## Agent Idle Detection

The blog post author rigged idle detection via:
1. Claude Code notification hook sends bell character (`\a`) on idle
2. tmux `monitor-bell on` + `bell-action any` highlights the tab
3. Visual scan of tab colors tells which agents need attention

This is fragile and agent-specific. A sandbox runner could detect agent process state more reliably (process exited, waiting for stdin, etc.) and surface it as structured status.

## Spec-Driven Development

The "Feature Design" (FD) system uses numbered markdown files with structured sections:
- Status (Planned → Design → Open → In Progress → Pending Verification → Complete)
- Problem statement
- Solution alternatives with trade-offs
- Implementation plan (specific files to modify)
- Verification steps

Key insight from HN discussion: "spec quality is everything. A vague spec produces code you'll spend more time debugging than you saved." This aligns with yoloAI's existing `--prompt-file` support but suggests value in structured templates.

## Coordination Approaches Mentioned in Discussion

| Approach | Description |
|----------|-------------|
| Flat markdown specs | Each agent gets a spec file; no inter-agent communication |
| Structured state (ctlsurf) | Agents query shared state ("is anyone working on auth module?") |
| Agent-doc | Document-scoped routing with snapshot-based diffs |
| NERDs framework | Entity-centered memory instead of chronological context |
| CAS factory mode | Automatic task decomposition and orchestration |

The flat markdown approach is simplest and works well with sandbox isolation (each sandbox gets its own spec). Shared-state approaches add complexity that may not be warranted when sandboxes already provide filesystem isolation.

## Relevance to yoloAI

yoloAI's sandbox model is a natural fit for parallel agent workflows:
- Each `yoloai new` creates an isolated sandbox — no merge conflicts during work
- Copy/overlay modes protect the original codebase
- `diff`/`apply` allows sequential review and integration
- tmux session management is already built in

**Gaps to address:**
1. No batch creation of sandboxes from a task list
2. No agent status detection (running/idle/done/error) in `yoloai ls`
3. `yoloai ls` output is minimal for multi-sandbox management
4. No ordering/dependency between sandboxes for sequential apply
5. No cost/token tracking surfaced from agents

**yoloAI advantages over manual tmux approach:**
- Sandbox isolation eliminates the permission-bypass security concerns the blog author raised
- Copy/diff/apply workflow means parallel agents can't conflict at the filesystem level
- Structured sandbox lifecycle (new/start/stop/destroy) vs. ad-hoc tmux window management

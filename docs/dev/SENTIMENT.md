# Developer Sentiment Tracker

Tracks developer sentiment about AI coding tools from community discussion sites. Used to understand how developers feel about AI tooling trends and where yoloAI fits in.

## Update Procedure

When the user says **"update the sentiment doc"**, follow these steps:

1. **Fetch Hacker News front pages.** Use WebFetch on `https://news.ycombinator.com/` and `https://news.ycombinator.com/news?p=2` to get the first two pages of stories.
2. **Fetch Lobsters front page.** Use WebFetch on `https://lobste.rs/` (page 1 only — page 2 triggers rate limiting).
3. **Identify relevant threads.** Look for stories related to: AI coding agents/assistants, AI developer tools, code generation, LLM-assisted development, sandboxing/isolation for AI, autonomous coding, AI pair programming, or specific tools (Claude Code, Cursor, Copilot, Aider, Codex, Windsurf, etc.).
4. **Read comment threads.** For each relevant story, fetch the comment page and read developer reactions. Focus on:
   - Pain points and frustrations with current AI tools
   - What developers praise or find genuinely useful
   - Trust and safety concerns (sandboxing, code review, autonomy levels)
   - Workflow preferences (copy/diff/apply vs live editing, terminal vs IDE, etc.)
   - Adoption barriers and skepticism patterns
   - Comparisons between tools
5. **Update the log below.** Add a dated entry summarizing findings. Each entry should include:
   - Date
   - Sources (links to threads analyzed)
   - Key themes observed
   - Notable quotes or sentiments (paraphrased, with context)
   - Relevance to yoloAI (how findings relate to our value proposition)
6. **Update the rolling summary.** Revise the summary section to reflect the latest overall sentiment trends.

### Fetching Rules

- **Sequential only.** Never make parallel WebFetch calls — risk of getting banned.
- **Cache locally.** Cache every fetch to `/Users/karlstenerud/.yoloai/sandboxes/yoloai/files/sentiment-cache/` so repeated fetches hit local cache. Check cache before fetching.
- **Rate limit handling.** If a fetch returns a rate limit error (429), give up on that specific URL after the first try. If two consecutive *different* URLs on the same site both return rate limit errors, abandon that site entirely and note what happened in the log entry.
- **Lobsters pacing.** Wait 1 minute (`sleep 60` via Bash) between each lobste.rs fetch. Do not fetch `lobste.rs/page/2` — it consistently triggers rate limiting.

## Rolling Summary

Developers are broadly skeptical of autonomous AI coding agents but actively use supervised AI-assisted workflows. The dominant pattern is human-directed, agent-assisted development with mandatory review — not fire-and-forget autonomy.

**Key signals for yoloAI:**

- **Sandboxing is implicitly desired.** Developers worry about AI agents making unexpected changes, but few mention sandboxing by name. The demand is latent — they describe the symptoms (unwanted changes, trust issues) without naming the solution. yoloAI's copy/diff/apply model directly addresses the most commonly described pain point.
- **Review before apply is the consensus workflow.** The community overwhelmingly agrees that AI code must be reviewed before landing. yoloAI's diff/apply flow aligns perfectly with this — it's the workflow developers already want but implement ad-hoc.
- **Autonomy skepticism is high.** "Agents while I sleep" is dismissed; supervised 5-30 minute sessions are preferred. yoloAI should position around controlled, reviewable agent sessions, not autonomous operation.
- **Cost sensitivity is real.** Token costs of $200/3 days, $5-10K/month for heavy users. Multi-agent setups multiply costs. yoloAI doesn't directly address this but shouldn't add overhead.
- **Tool fatigue is emerging.** Developers compare Skills, MCPs, and config files to legacy tech bloat. They want "the JSON over HTTP version of AI: simple but powerful." yoloAI's simplicity (just a CLI + sandbox) is a potential advantage.
- **Terminal vs IDE splits by experience level.** Advanced users favor CLI tools (Claude Code); mainstream users use IDE-integrated tools (Cursor). yoloAI serves the CLI-first audience.
- **Multi-agent interest is cautious.** Token costs and coordination complexity deter most. Two-agent setups (writer + reviewer) are the practical ceiling today.
- **Open source projects are actively hostile to unsupervised AI contributions** but accept AI-assisted work with human oversight. This reinforces the review-first model.

## Key Themes to Watch

- **Sandboxing demand:** Do developers want AI agents sandboxed? Is this a selling point or a non-concern?
- **Review workflow:** Do developers want to review AI changes before applying, or prefer live editing?
- **Terminal vs IDE:** Preference for CLI-based agents vs IDE-integrated ones?
- **Autonomy trust:** How much autonomy are developers comfortable giving AI agents?
- **Tool fatigue:** Are developers overwhelmed by the number of AI coding tools?
- **Cost sensitivity:** Are developers concerned about API costs and token usage?
- **Open source preference:** Do developers prefer open-source AI tools over proprietary ones?
- **Multi-agent interest:** Is there appetite for parallel/coordinated agent workflows?

---

## Sentiment Log

### 2026-03-11

**Sources analyzed:**

Hacker News (pages 1-2):
- [Agents that run while I sleep](https://news.ycombinator.com/item?id=47327559) — 429 comments
- [After outages, Amazon to make senior engineers sign off on AI-assisted changes](https://news.ycombinator.com/item?id=47323017) — 454 comments
- [I built a programming language using Claude Code](https://news.ycombinator.com/item?id=47325595) — 176 comments
- [Redox OS strict no-LLM policy](https://news.ycombinator.com/item?id=47320661) — 427 comments
- [Levels of Agentic Engineering](https://news.ycombinator.com/item?id=47320614) — 116 comments
- [AI Agent Hacks McKinsey](https://news.ycombinator.com/item?id=47333627) — 33 comments

Lobsters (page 1 only — page 2 rate-limited):
- Identified threads but could not fetch comments (two consecutive rate limit errors on lobste.rs comment pages). Titles found: "AI should help us produce better code" (37 comments), "Amazon GenAI outages" (29), "Redox OS no-LLM policy" (16), "Source-available AI contribution policies" (6), "LLMs bad at vibing specifications" (1).

**Key themes:**

1. **Autonomous agents are rejected; supervised workflows are embraced.** The "agents while I sleep" premise was overwhelmingly dismissed. Developers prefer 5-30 minute supervised sessions with active review. 5-7x productivity gains are considered realistic with spec-driven, human-directed workflows. The marketing narrative of full autonomy outpaces what practitioners actually do.

2. **Review is non-negotiable but expensive.** Amazon's AI outage response (requiring senior sign-off) was met with cynicism — review catches fewer bugs than claimed, and the time spent reviewing approaches the time to write code manually. Yet developers still insist on it. The tension: review is both inadequate and essential. yoloAI's diff view reduces review friction without eliminating it.

3. **AI code quality is inconsistent.** Claude Code praised for matching project style but "gets stuff wrong 1 in 5 times." AI introduces "unnecessary indirection, additional abstractions, fails to re-use code." Generated tests are "tautological." Agents "quietly add fallbacks that return mock data." Developers must verify every change.

4. **Cost-benefit is unclear.** Studies cited showing AI tools increased task completion time by 19%. Token costs range from $200/3 days to $5-10K/month. Multi-agent setups multiply costs with unclear ROI. "How much money have you made with this approach?" went largely unanswered.

5. **Context management is the real bottleneck.** Context rot — agents re-discovering the same architecture after 20 minutes — is a bigger problem than model capability. Persistent state files (STATE.md, CLAUDE.md, ADRs) help but require discipline. Better context beats more parallelism.

6. **Open source is actively hostile to unsupervised AI code.** Redox OS's no-LLM policy drew mixed reactions but highlighted the core issue: maintainer burden from low-effort AI-generated PRs. The nuanced position accepts AI-assisted work (human reviews, understands, owns the code) while rejecting AI-generated submissions (copy-paste from LLM).

7. **Tool complexity fatigue.** Developers compare Skills, MCPs, and agent config to legacy tech bloat (servlets, RMI, Flash). Want simplicity: "the JSON over HTTP version of AI." .cursorrules and CLAUDE.md lack standardization.

**Notable sentiments:**

- "unchecked, it's like having a small army of the most eager junior devs going completely fucking ape in the codebase"
- "produce 100x more code, that code gets 1/100th as much validation"
- "there's a human limit on how much garbage they can type out...AI revolutionizes things" (by removing that constraint)
- "Who is going to use it? You certainly won't, because you're dependent on AI"
- "hoping we can soon reach the JSON over HTTP version of AI: simple but powerful"
- "LLMs make everything sound profound...well-written bullshit"

**Relevance to yoloAI:**

- **Copy/diff/apply is exactly what developers want.** They describe needing to review AI changes before landing, working in isolated branches, and avoiding live editing of production code. yoloAI makes this the default workflow rather than an afterthought.
- **Sandboxing addresses unnamed fears.** Developers worry about agents making unexpected changes, adding mock fallbacks, modifying things "not nailed down." Sandboxing is the answer they haven't articulated — yoloAI should make this connection explicit in messaging.
- **Simplicity is a competitive advantage.** Tool fatigue is real. yoloAI's "just a CLI + sandbox" approach contrasts favorably with complex agentic frameworks requiring custom orchestration.
- **Position for supervised sessions, not autonomy.** The market wants better tools for human-directed AI coding, not autonomous agents. yoloAI enables the "run agent for 5-30 minutes, review diff, apply selectively" workflow that practitioners actually use.
- **Multi-agent is premature for marketing** but worth supporting. Two-agent writer+reviewer setups are the practical ceiling; complex orchestration is experimental and expensive.

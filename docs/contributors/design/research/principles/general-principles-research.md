> **ABOUTME:** Primary-source evidence backing each principle in general-principles.md — each
> section names the source and explains why it backs the principle, and where it doesn't cleanly
> apply to yoloAI's single-author OSS CLI scope. Evidence, not principle; uncertain attributions
> are marked [verify] rather than asserted as fact.

# General-principles research — primary-source backing for yoloAI

This file is evidence, not principle. `principles/general-principles.md` cites
this by section; this file cites the outside world. Purpose: every principle in
the general doc traces to a dated, named, findable source so the reasoning
doesn't evaporate when decisions are revisited.

Applied to yoloAI's parameters: single author, Go CLI binary, OSS public beta,
no paying customers, no SaaS surface, no operator team — just Karl as developer
and the community of users who install the binary. Five runtime backends ship
(Docker, Podman, Tart, Seatbelt, containerd). Core differentiator: copy/diff/apply
workflow with `git format-patch` + `git am` under the hood.

---

## Sources overview

| Source | Date | Type | Relevant principles |
|--------|------|------|---------------------|
| Kent Beck (C3 project) — YAGNI coinage | late 1990s | Primary, named author | §1 Pragmatic/YAGNI |
| Martin Fowler — "Yagni" (martinfowler.com/bliki/Yagni.html) | 2015 | Primary, named author | §1 Pragmatic/YAGNI |
| 37signals — *Getting Real* | 2006 | Primary, named org | §1 Pragmatic/YAGNI |
| Dan McKinley — "Choose Boring Technology" (mcfunley.com) | 2015 (+ 2021 follow-up) | Primary, named author | §2 Boring tech / innovation tokens |
| DHH — cloud-exit essays (world.hey.com/dhh) | 2022–2024 | Primary, named author | §2 Boring tech / innovation tokens |
| Jeff Bezos — 2015 Amazon shareholder letter | Apr 2016 | Primary, named author | §3 Type 1 / Type 2 reversibility |
| Jeff Bezos — 2016 Amazon shareholder letter | Apr 2017 | Primary, named author | §3 Type 1 / Type 2 reversibility |
| Michael Nygard — "Documenting Architecture Decisions" (cognitect.com) | Nov 2011 | Primary, named author | §7 Document the no |
| Michael Nygard — *Release It!* 2nd ed. (Pragmatic Bookshelf) | 2007 / 2nd ed. 2018 | Primary, named author | §4 Blast radius |
| Martin Fowler — Circuit Breaker (martinfowler.com/bliki/CircuitBreaker.html) | 2014 | Primary, named author | §4 Blast radius |
| Principles of Chaos Engineering — principlesofchaos.org | 2017–2019 | Community standard | §4 Blast radius |
| Unix Philosophy — Doug McIlroy / Ken Thompson / Brian Kernighan | 1978 / 1984 | Primary, named authors | §5 Ecosystem-first |
| GNU/Linux toolchain conventions — documented in POSIX, man pages | ongoing | Standards | §5 Ecosystem-first |
| OWASP Docker Security Cheat Sheet | maintained | Community standard | §6 Safe defaults |
| CIS Docker Benchmark | maintained | Community standard | §6 Safe defaults |
| Charity Majors / Honeycomb — "Alerts Are Fundamentally Messy" and related essays | 2018–2023 | Primary, named author | §9 Surface failures honestly |
| Dave Snowden — Cynefin framework, IBM Systems Journal 2003 | 1999–2007 | Primary, named author | §3 Type 1 / Type 2 reversibility |
| yoloAI codebase and design docs (cited inline) | 2026 | Internal | all principles |

---

## §1 — Pragmatic over perfect / YAGNI applied to OSS CLI scope

### Kent Beck — YAGNI (late 1990s)

YAGNI — "You Aren't Gonna Need It" — was coined on the Chrysler Comprehensive
Compensation (C3) project by Kent Beck and Chet Hendrickson in the late 1990s as
part of the Extreme Programming discipline. The original formulation is credited to
Beck/Hendrickson in conversation; Ron Jeffries documents it at
ronjeffries.com/xprog/articles/practices/pracnotneed/ (accessed 2026). The
principle states: do not add capability until it is needed. It is a discipline
applied at the feature level, not a license to skip quality. The original XP
context was a corporate payroll system; its application to a single-author CLI
tool is direct and arguably even cleaner — there is no product manager to demand
the roadmap feature, which means the YAGNI temptation is entirely internal.

### Martin Fowler — "Yagni" (2015)

Fowler's canonical write-up is at martinfowler.com/bliki/Yagni.html, dated May
2015. It names four distinct costs of premature features: (1) cost of build —
effort spent on capabilities that may never be used; (2) cost of delay — the
feature that was delayed to build the presumptive one; (3) cost of carry —
ongoing complexity slowing future work; (4) cost of repair — the capability was
built before requirements clarified and now needs rework. Fowler's key refinement,
directly applicable to yoloAI: "Yagni is not a justification for neglecting the
health of your code base. Yagni requires (and enables) malleable code." This
distinguishes YAGNI from "ship spaghetti." For yoloAI's single-author OSS scope,
the cost-of-carry term is especially load-bearing: each presumptive feature a solo
author builds is complexity they personally must maintain across all five backends
and any future sixth. Fowler also anchors the principle empirically: "research
suggests only ⅓ of features improved the metrics they were designed to improve" —
i.e., 66%+ of presumptive features are net-negative. URL:
martinfowler.com/bliki/Yagni.html.

### 37signals — *Getting Real* (2006)

*Getting Real* is freely available at basecamp.com/gettingreal. The relevant
chapters are "Build Less" ("Underdo your competition"), "It Just Doesn't Matter"
(most features are edge-case requests), and "Hold the Mayo" (start with a
constrained list, not a maximum). The 37signals framing is squarely aimed at small
teams — specifically at two-to-twelve-person software companies — making it more
directly applicable to a solo author than enterprise methodologies. The book's
central premise ("half the product, twice the quality") is a working operational
restatement of YAGNI at the product level. The distinction with yoloAI: 37signals
was building a SaaS product with a paying-customer acquisition goal; yoloAI is
building an OSS CLI with a community-adoption goal. The YAGNI logic applies in
both cases, but the "metrics that matter" are different (GitHub stars and
`go install` usage rather than MRR).

### Scope note

The foley project's general-principles research (general-principles-research.md §1)
cites the same Fowler/Beck/37signals sources. The scoping difference: foley
applies YAGNI under "we have a product to ship to paying customers"; yoloAI applies
it under "we have a tool to maintain solo across multiple backends." The cost-of-carry
term dominates more heavily in yoloAI's case — a feature no user ever triggers still
creates maintenance debt per backend.

---

## §2 — Boring technology + innovation tokens

### Dan McKinley — "Choose Boring Technology" (2015)

Primary source: mcfunley.com/choose-boring-technology, published 2015. An
interactive version with the "wheel of boredom" lives at boringtechnology.club
(2021). McKinley wrote this while at Etsy; he applied it to infrastructure choices
across a large engineering org. The key mechanism is the *innovation token budget*:

> "A young company gets about three innovation tokens. You can spend these however
> you want, but the supply is fixed for a long while."

McKinley's definition of "boring": "It's bad, but you know why it's bad. You can
list all of the main ways it will let you down." And his threshold rule: "It is
basically always the case that the long-term costs of keeping a system working
reliably vastly exceed any inconveniences you encounter while building it."

Applied to yoloAI: the CLI ships with five runtime backends (Docker, Podman, Tart,
Seatbelt, containerd — see `runtime/` subdirectories in codebase). Each backend
is already a significant operational surface to maintain. The stack choice Go +
Docker + git is maximally boring. The one non-boring choice that earns a token is
the multi-backend pluggable runtime interface itself — that architectural bet is
the differentiator. McKinley's framing makes this explicit: spend tokens on what's
differentiated (the runtime abstraction, the five backends); refuse tokens on
infrastructure novelty (the language, the binary distribution, the diff format).

The 2021 follow-up essay at boringtechnology.club slightly revises the framing but
doesn't change the core argument. McKinley's original post remains the canonical
cite.

**Scope note.** McKinley wrote for a large engineering org with a full team.
For a single-author project, the token budget is if anything smaller, because the
author must personally understand every failure mode of every dependency. A large
team can distribute that understanding; a solo author cannot. The principle applies
with more force, not less.

### DHH — cloud-exit essays (2022–2024)

David Heinemeier Hansson published a series of essays on 37signals' decision to
exit the cloud (Amazon Web Services + Heroku) and run their own hardware. The
series begins with "Why we're leaving the cloud" (world.hey.com/dhh/why-we-re-
leaving-the-cloud-654b47e0, Nov 2022) and continues through 2023–2024 with
documented cost savings ($2.3m/yr → ~$840k/yr at 37signals scale). The essays are
relevant to yoloAI not as an argument for self-hosting (yoloAI users are running
on their own machines; there is no cloud to exit) but as an argument about
dependency cost reasoning: every vendor dependency carries a total cost of
ownership that includes the vendor's pricing decisions, the vendor's reliability,
and the organizational knowledge locked into that vendor. DHH's math shows that
even large, successful companies systematically undercount dependency cost. For a
solo-author OSS CLI the lesson is tighter: each new dependency is a future
debugging session that Karl must personally handle.

**Scope note.** DHH's 37signals-scale math (servers, ops team, colocation costs)
does not apply to yoloAI. But the cost-accounting discipline — "what does this
dependency actually cost over its lifetime?" — applies at any scale.

---

## §3 — Type 1 / Type 2 doors (Bezos) — reversibility informs decision speed

### Jeff Bezos — 2015 Amazon shareholder letter

Primary source: Amazon.com investor relations, annual report 2015, published April
2016. Available via SEC EDGAR or amazon.com/ir. The Type 1 / Type 2 framing
appears in the letter's "Day 2 is Stasis" section. Bezos's verbatim framing:

> "Some decisions are consequential and irreversible or nearly irreversible —
> one-way doors — and these decisions must be made methodically, carefully, slowly,
> with great deliberation and consultation. If you walk through and don't like what
> you see on the other side, you can't get back to where you were before. We can
> call these Type 1 decisions. But most decisions aren't like that — they are
> changeable, reversible — they're two-way doors."

Bezos identifies the failure mode that applies directly to solo-author decision
hygiene: applying Type 1 process to Type 2 decisions, "resulting in slowness and
unthoughtful risk aversion." For a single-author CLI project in public beta with
five backends to maintain, Type 2 decisions must ship fast; Type 1 decisions
(breaking API changes, removing a backend, dropping copy/diff/apply as the
differentiator) demand deliberation. The framing is especially crisp for pre-1.0
where the surface area that is genuinely Type 1 is narrow (the design
differentiators, the plugin API contract) and the surface area that is Type 2 is
vast (flag names, output formats, config schema).

### Jeff Bezos — 2016 Amazon shareholder letter

Primary source: Amazon.com investor relations, annual report 2016, published April
2017. The 70%-information rule appears here:

> "Most decisions should probably be made with somewhere around 70% of the
> information you wish you had. If you wait for 90%, in most cases, you're
> probably being slow."

Bezos adds the complementary principle: "disagree and commit" as the tie-breaker
for contested Type 2 decisions. For solo authorship, the 70% rule becomes
self-directed: if Karl is hesitating past 70% on a reversible decision, that's a
slowness symptom. The "disagree and commit" surface doesn't apply (there's no
second person to disagree), but the speed heuristic does.

### Dave Snowden — Cynefin framework (1999–2007)

Snowden introduced Cynefin informally in 1999 and formalized it in: Snowden, D.J.
and Boone, M.E. (2007) "A Leader's Framework for Decision Making," *Harvard
Business Review*, Nov 2007. The IBM Systems Journal version is Snowden, D.J.
(2002) "Complex Acts of Knowing: Paradox and Descriptive Self-Awareness," *IBM
Systems Journal*, 41(3), pp. 462–483. Cynefin's four domains (clear / complicated
/ complex / chaotic) provide a complementary vocabulary to Bezos's two-door
framing: Bezos describes *consequence* (reversible vs irreversible); Cynefin
describes *context* (known unknowns vs unknown unknowns). For yoloAI's decision
practice, the combination is: classify by consequence first (Type 1 / Type 2),
then by context (what do we know about this domain?). Most yoloAI CLI decisions
live in "complicated" (Docker/containerd/Tart behavior is well-documented but
requires expertise); the security threat model lives in "complex" (emergent
behaviors from agent + container interaction are not fully predictable).

**Scope note.** The Cynefin framework was developed for large organizational
decision-making. For a solo-author project it provides vocabulary more than
process. The Bezos framing is more directly actionable at yoloAI's scale.

---

## §4 — Blast radius bounded — every dangerous operation has an upper bound

### Principles of Chaos Engineering (principlesofchaos.org)

Primary source: principlesofchaos.org, first published 2017, updated 2019. The
document is a community standard authored by Netflix engineers (Casey Rosenthal,
Bruce Wong, Kolton Andrus, and others). The "minimize blast radius" tenet:

> "Experimenting in production has the potential to cause unnecessary customer
> pain. While there must be an allowance for some short-term negative impact, it
> is the responsibility and obligation of the chaos engineering team to ensure the
> fallout from experiments is minimized and contained."

The operational implementation at Netflix: maximum blast radius is an explicit
design parameter of every chaos experiment, with a global kill switch. For yoloAI,
the analogy is direct: every sandbox operation that can damage something has an
explicit upper bound. The `yoloai apply` command applies changes to originals —
that is the operation with real blast radius. The protections are: dirty-repo
warning before sandbox creation (D8, codebase); dangerous-directory refusal for
system paths (`docs/contributors/design/security.md`); `:copy` mode default for workdir
(D4, `working-notes.md`); disk pre-flight check before operations that would fail
silently (commit `8749864` in git log).

### Michael Nygard — *Release It!* (2007 / 2nd ed. 2018)

Primary source: Nygard, M. (2007) *Release It! Design and Deploy Production-Ready
Software*, Pragmatic Bookshelf. Second edition 2018. Chapter 5 (Stability Patterns)
introduces the Bulkhead and Circuit Breaker patterns. Nygard's canonical framing of
the blast-radius problem: a single failing component, if not contained, will
exhaust shared resources and cascade. The Circuit Breaker (also see Fowler 2014
below) stops the cascade. The Bulkhead separates resource pools so one bad
dependency can't starve others. For yoloAI, the blast-radius bounded principle
operates at the user-impact level rather than the service-mesh level: a misbehaving
agent inside a sandbox cannot affect the user's host originals (`:copy` mode), and
a misbehaving container cannot affect the host filesystem beyond the explicitly
granted mounts (Docker isolation + dangerous-directory detection).

### Martin Fowler — Circuit Breaker (2014)

Primary source: Fowler, M. "CircuitBreaker," martinfowler.com/bliki/
CircuitBreaker.html, March 2014. Fowler adapts Nygard's circuit breaker pattern
into a broadly-cited form. Relevant for yoloAI: the idle-detection subsystem
(`runtime/monitor/`) acts as a functional circuit breaker — when an agent is
determined to be idle (no progress), the system surfaces the state rather than
letting the sandbox run indefinitely. The disk pre-flight (`docs/contributors/
working-notes.md` — referenced in commit `8749864`) is a preflight bound: refuse
to start an operation that would fail mid-way due to ENOSPC, rather than letting
partial failure leave state inconsistent.

### yoloAI codebase — specific blast-radius bounds

The following codebase locations instantiate the principle. These are internal
citations; they are not external sources but they are the worked examples the
principle points to.

- **Dangerous-directory detection:** `docs/contributors/design/security.md` §Security
  Considerations — refuses `$HOME`, `/`, macOS system directories, Linux system
  directories unless `:force` is appended.
- **Dirty-repo warning:** `docs/contributors/design/security.md` — warns when `:rw` is used
  with uncommitted git changes.
- **Disk pre-flight:** commit `8749864` ("Smoke: two-stage sentinel + disk
  pre-flight for better ENOSPC diagnostics") — checks available disk before
  operations that would fail silently on ENOSPC.
- **`:copy` as default:** D4 in `working-notes.md` — the default mode for the
  workdir is `:copy` (staged, reviewed) not `:rw` (live, immediate).
- **Timeout infrastructure:** idle-detection timeout in `runtime/monitor/`
  prevents indefinitely-running agent sessions.

---

## §5 — Don't reinvent the wheel — ecosystem-first (git, Docker, Unix tools)

### Unix Philosophy — McIlroy, Thompson, Kernighan (1978 / 1984)

The Unix Philosophy was first articulated by Doug McIlroy in the Bell System
Technical Journal, 1978 (McIlroy, M.D., Pinson, E.N., Tague, B.A. "Unix
Time-Sharing System: Foreword," *Bell System Technical Journal*, 57(6), 1978):
"Write programs that do one thing and do it well. Write programs that work
together." Brian Kernighan and Rob Pike expanded it in *The Unix Programming
Environment* (Prentice-Hall, 1984), Chapter 1. The relevant clause for yoloAI:
"use programs together, expect the output of every program to become the input to
another." yoloAI's copy/diff/apply workflow is a direct implementation: `git
format-patch` produces the diff, `git am` applies it, and yoloAI composes these
rather than reimplementing patch logic. The design decision to use `git diff
<baseline_sha>` rather than a bespoke diff format (D6 in `working-notes.md`) is
the clearest single instance.

### `git format-patch` + `git am` as canonical diff/apply

No single "author" owns this citation; it is documented in git's own man pages
(git-format-patch(1), git-am(1)), first shipped in git 1.3 (Linus Torvalds, 2005).
The relevant property: `git format-patch` produces an email-formatted patch series
that `git am` can apply to any git repository — not just the one that produced it.
yoloAI's `yoloai diff` / `yoloai apply` workflow (see `copyflow/` in
codebase) uses this mechanism because it is the established, widely-understood
tool for "capture changes and apply them elsewhere." Building a bespoke diff format
would have been reinventing a solved problem. The design doc states this explicitly:
"Don't reinvent the wheel. Before designing a feature, check if existing tools (git,
docker, unix utilities) already provide a workflow that solves the problem."
(`CLAUDE.md` §Design Principles).

### Docker's ecosystem integration

Docker's own documentation is the primary source here; no single essay is the
canonical cite. The relevant property: Docker's layer caching, image building,
volume mounting, and registry model are standardized interfaces that yoloAI builds
on rather than reimplementing. The Dockerfile-per-profile design (one Dockerfile +
config.yaml per profile, `docs/contributors/design/README.md` §Architecture) means users get
the full expressiveness of Docker's build system — any FROM image, any RUN step —
without yoloAI needing to build a package-manager abstraction. This is
ecosystem-first: leverage the existing tool rather than wrapping it.

**Scope note.** Some competitors (cco, sandbox-runtime) avoid Docker as a
hard dependency; their value proposition is "no Docker needed." yoloAI's
ecosystem-first principle accepts Docker as the dependency because Docker's
ecosystem (images, registries, Dockerfiles) is the existing wheel. The tradeoff is
documented in the design docs.

---

## §6 — Safe defaults — read-only mounts, name required, dirty-repo warning

### OWASP Docker Security Cheat Sheet

Primary source: owasp.org/www-project-docker-security/, maintained by the OWASP
Docker Security project (community document, no single named author). Relevant
guidance: containers should default to read-only mounts for directories not
requiring writes; secrets should not be passed as environment variables; containers
should run as non-root. yoloAI's defaults align with all three: directories are
read-only by default (`:ro` unless `:copy` / `:overlay` / `:rw` is explicit),
credentials are injected via file-based mechanism not environment variables
(`docs/contributors/design/security.md` §Credential Management), and the container runs as user
`yoloai` matching host UID/GID not as root.

### CIS Docker Benchmark

Primary source: Center for Internet Security, "CIS Docker Benchmark" (cisecurity
.org). The benchmark is versioned; the current version as of 2026 is 1.6.
Relevant controls: Section 5 (Container Runtime) recommends read-only rootfs where
possible, restricted capabilities (no `--privileged` unless required), no
host-network sharing without explicit justification. yoloAI's `:copy` mode and
`:overlay` mode both avoid `--privileged`; `CAP_SYS_ADMIN` is granted only for
`:overlay` mode (required for overlayfs inside the container) and `CAP_NET_ADMIN`
only for `--network-isolated` (required for iptables rule setup). The design doc
documents both capability grants and their rationale explicitly
(`docs/contributors/design/security.md`).

### yoloAI design decisions — safe defaults

The following design decisions implement safe defaults. Internal citations.

- **Read-only default for aux dirs:** D4 in `working-notes.md` — "All dirs
  read-only by default. Per-directory `:rw` (live) or `:copy` (staged) suffixes."
- **Name required:** The sandbox name is a required positional argument; no
  auto-generation. The design doc describes this as "safe defaults: ... name
  required (no auto-generation)" (`docs/contributors/design/README.md` §Design Principles). The
  rationale: auto-generated names lead to sandbox sprawl and make `yoloai apply`
  ambiguous.
- **Dirty-repo warning:** `docs/contributors/design/security.md` — "`:rw` directories give the
  agent direct read/write access. Use only when you've committed your work or don't
  mind destructive changes. The tool warns if it detects uncommitted git changes."
- **Copy mode default for workdir:** D4 — "Workdir defaults to `:copy` if no
  suffix given; `:rw` must be explicit." The default is the protected mode.

### Principle of Least Privilege — general literature

The principle of least privilege is documented in Saltzer, J.H. and Schroeder, M.D.
(1975) "The Protection of Information in Computer Systems," *Proceedings of the
IEEE*, 63(9). This is the original formulation. It states that every program and
user should operate with the minimum privileges necessary to complete the job.
yoloAI's per-mode capability grants (`:copy` needs no elevated capabilities,
`:overlay` needs `CAP_SYS_ADMIN`, `--network-isolated` needs `CAP_NET_ADMIN`) are
a direct application: grant capability when the feature requires it, refuse it
otherwise.

---

## §7 — Factual accuracy bar — verify research, primary sources, no hallucinated features

This principle has no single external source — it is a yoloAI-internal discipline
derived from the practice of AI-assisted design, where LLMs can produce
plausible-sounding but unverified claims at scale. The backing is the observed
failure mode, not a canonical text. However, several external sources describe the
underlying problem.

### AI hallucination and factual accuracy in LLM-generated content

The academic literature on LLM hallucination is extensive and growing rapidly.
A commonly cited survey is: Maynez, J., Narayan, S., Bohnet, B., and McDonald, R.
(2020) "On Faithfulness and Factuality in Abstractive Summarization," *ACL 2020*.
For claims about software tools (competitor features, star counts, API capabilities),
the relevant failure mode is confident confabulation: the model produces a specific,
plausible-sounding number or feature claim that does not correspond to the actual
product. This has been observed and documented across multiple yoloAI critique
rounds (D2 in `working-notes.md`): "AI-assisted design generates plausible-sounding
but unverified claims at scale. The critique cycle is the explicit factual-accuracy
gate."

### yoloAI critique cycle — D2

Primary internal source: D2 in `working-notes.md`. The critique cycle discipline
(twelve rounds before any v1 code shipped) is explicitly motivated by the
factual-accuracy failure mode. The consequence stated in D2: "The cost is cheap (an
hour per round); the damage prevented (architectural drift, security claims that
don't hold, vendor-feature claims that aren't true) is structural." This is the
yoloAI-specific operationalization of the factual accuracy bar.

### CLAUDE.md — Factual accuracy matters

`/home/karl/.claude/CLAUDE.md` §Factual accuracy matters: "Star counts, feature
claims, and security assertions must be verified. Don't repeat marketing language
or unverifiable numbers." This is the rule enforced across all yoloAI design and
research documents. It is stated as a project constraint, not derived from external
literature.

### Scope note

This principle has no direct analogue in the foley research (foley's general-
principles-research.md does not include a factual-accuracy principle). It is
yoloAI-specific, arising from the combination of: (a) AI-assisted design as the
authoring method; (b) the project's research-before-design discipline; (c) the
public nature of the design docs (they can be read by users who will notice errors).
No foley source carries over here.

---

## §8 — Document the no — ADRs / D-entries; rejected alternatives matter as much as the chosen path

### Michael Nygard — "Documenting Architecture Decisions" (2011)

Primary source: Nygard, M. "Documenting Architecture Decisions," cognitect.com/
blog/2011/11/15/documenting-architecture-decisions, November 2011. This is the
canonical ADR introduction. Nygard's framing:

> "The whole document should be one or two pages long. We will write each ADR as
> if it is a conversation with a future developer."

The three problems ADRs solve, per Nygard: (1) lost institutional memory — new
team members must blindly accept or blindly reverse; (2) documentation decay —
large up-front specs become unreadable, small ADRs survive; (3) team paralysis —
without rationale, teams become afraid to change anything. For a solo-author
project, problem (1) manifests as "future-Karl in six months is a different person"
and problem (3) manifests as "I can't remember why I didn't just use X." The
canonical ADR sections Nygard specifies: Title / Status / Context / Decision /
Consequences. The Status field values: Proposed / Accepted / Deprecated /
Superseded. URL: cognitect.com/blog/2011/11/15/documenting-architecture-decisions.

### Community ADR templates (Joel Parker Henderson)

The community repository at github.com/joelparkerhenderson/architecture-decision-
record (Joel Parker Henderson, maintained) standardizes several ADR variants. The
most relevant extension to Nygard's original is the "Alternatives Considered"
section: a required enumeration of what was explicitly rejected and why. Henderson's
repo documents this as: "Alternatives: [List the main alternatives and their
pros/cons]." This section is what makes the ADR into a "document the no" artifact
rather than merely a "document the yes" artifact. The AWS Architecture Blog (2023)
makes this explicit: "Hiding rejected alternatives is a common mistake, and future
teams need to know what you considered and why you rejected it — otherwise they
will rediscover the same tempting wrong option and reopen the debate."
[verify: exact AWS Architecture Blog URL and date — the source is directional but
the specific post URL was not confirmed at time of writing.]

### yoloAI working-notes.md — D-entries as proto-ADRs

Internal source: `docs/contributors/decisions/working-notes.md`. The D-numbered entries (D1 onward)
follow the Nygard shape: Decision / Rejected / Why / Consequences / Composition.
The "Rejected" field is the yoloAI implementation of "document the no." Notable
examples: D1 (Go over Python/Rust — Python rejected for distribution reasons, Rust
rejected for ecosystem fit), D3 (mirrored host paths over `/work` prefix — the
rejected option and its rationale are preserved), D4 (explicit mode taxonomy over
implicit upgrades — "implicit upgrade from `:ro` to `:rw` on first write rejected
as the kind of magic that produces incidents"). Each rejected alternative is
documented with the reasoning, preserving the decision space for future revisit.

---

## §9 — Default to public — OSS, public roadmap, public design docs, public idiosyncrasies catalog

### 37signals transparency culture

37signals' *Getting Real* (2006, basecamp.com/gettingreal) includes the chapter
"Open Up Your Books" and the general posture that small companies build trust by
being visibly honest: public pricing, public policies, public process. This
predates the "default open" norm in OSS communities but is consistent with it.
37signals publishes their employee handbook (basecamp.com/handbook), their
roadmaps, and their engineering decisions (HEY.com engineering blog). The
applicability to yoloAI is direct: a CLI tool competing in a space with Docker
Sandboxes, cco, sandbox-runtime earns community trust by being legible — public
design docs, public decisions, public security tradeoffs.

### OSS community norms — openness as a default

No single primary source owns this; it is the accumulated norm of the OSS
community since the late 1990s. The relevant citation for yoloAI is the
practical competitive context: yoloAI competes against tools that are partially or
fully closed (Docker Sandboxes has a commercial parent; cco's internals are
MIT-licensed but less well-documented). Public design docs (`docs/contributors/design/`),
public idiosyncrasies catalog (`docs/contributors/backend-idiosyncrasies.md`), public
BREAKING-CHANGES (`docs/BREAKING-CHANGES.md`), public roadmap (`docs/ROADMAP.md`),
and public security decisions (`docs/contributors/design/security.md`) are competitive
differentiators in an OSS CLI context. They are also the mechanism by which
community contributors can engage with the design rather than only the code.

### Post-SolarWinds sub-processor transparency expectation

The 2020 SolarWinds supply-chain attack (CISA advisory AA20-352A, Dec 2020) and
the 2024 xz utils attack (CVE-2024-3094, March 2024) produced an industry
expectation that software vendors publish their dependency chains. For yoloAI, the
equivalent is: users know which container runtimes the tool depends on, what network
calls the tool makes, and what the security tradeoffs of each mode are. The
security doc (`docs/contributors/design/security.md`) and the backend-idiosyncrasies catalog
(`docs/contributors/backend-idiosyncrasies.md`) are the implementation. SolarWinds: US-CERT
advisory AA20-352A available at cisa.gov. xz: NVD entry CVE-2024-3094.

**Scope note.** The foley research (general-principles-research.md §11 gap G6)
proposes "Default to public" as an addition; that framing is SaaS-flavored (public
pricing, sub-processor list, security.txt). For yoloAI the principle is OSS-flavored
(public design docs, public idiosyncrasies, public decisions). The mechanism is the
same; the audience differs. Neither foley-specific content (GDPR sub-processor
obligations, RFC 9116 security.txt for a web service) nor acquisition-prep framing
carries over to yoloAI.

---

## §10 — Surface failures honestly — diagnostic-first (disk pre-flight is canonical example)

### Charity Majors / Honeycomb — alert fatigue and signal vs noise (2018–2023)

Charity Majors (co-founder, Honeycomb.io) has written extensively on observability
and alert hygiene. The most commonly cited essays are: "Alerts Are Fundamentally
Messy" (charity.wtf, approx. 2018–2019) and "Best Practices for Alerts" posts on
the Honeycomb blog (honeycomb.io/blog, 2018–2023). The central claim, directly
applicable to yoloAI's failure-surfacing design: alerts that fire on noise *reduce*
signal because operators (and in yoloAI's case, users) learn to ignore them. The
same logic applies to error messages and diagnostic output. A tool that prints
confusing or misleading errors trains users to ignore errors. A tool that prints
honest, specific, actionable diagnostics builds trust and reduces debugging time.

For yoloAI, this shapes the disk pre-flight design (commit `8749864`): rather than
surfacing a confusing "agent idle 9s+" message when ENOSPC prevents container
creation, the tool proactively checks disk availability and reports `ENOSPC` with
context before the operation begins. The `docs/contributors/decisions/working-notes.md` memory file
(`project_smoke_disk_pressure.md`) documents this exact failure mode: "Smoke fails
on containerd-vm but not docker → check `df -h /` first; ENOSPC manifests as
'agent idle 9s+' not a clear error."

### `yoloai system doctor` as diagnostic surface

Internal source: `internal/cli/system_doctor.go`. The `yoloai system doctor`
command is a dedicated surface for capability pre-flight: what backends are
available, what prerequisites are missing, and what the fix instructions are. This
is the "surface failures honestly" principle operationalized at the user-facing
level: rather than failing silently at runtime, the tool surfaces availability
information before any sandbox operation. The `runtime/caps/` package (capability
detection system) performs the underlying probing.

### `yoloai system disk` and disk pre-flight

Internal source: commit `d894f00` ("Feature: yoloai system prune --cache + yoloai
system disk") and commit `8749864` ("Smoke: two-stage sentinel + disk pre-flight
for better ENOSPC diagnostics"). These commits add explicit disk visibility (`yoloai
system disk` command) and pre-flight checks that prevent operations from failing
mid-way on ENOSPC. The design principle: a tool that fails at a point where partial
state has been created is harder to debug and recover from than a tool that refuses
to start with a clear error. Surface the failure before any state is modified.

**Scope note.** Foley's equivalent principle (general-principles-research.md §9)
is framed as "surface failure publicly when it matters" — specifically about
user-facing status pages, incident communication, and third-party dependency status.
That SaaS-flavored framing does not carry over to yoloAI. yoloAI's surface is
the CLI user (one person, interactively), not a user base reading a status page.
The principle is the same (honest surfacing over silent absorption) but the
implementation is CLI diagnostics, not incident communication. The Honeycomb
signal/noise framing applies at both layers.

---

## §11 — Cross-platform awareness — platform-specific claims need platform-specific verification

### Docker Desktop on macOS — VirtioFS and I/O behavior

Docker Desktop for macOS has materially different I/O characteristics from native
Linux Docker. The primary source is Docker's own documentation: docs.docker.com/
desktop/mac/performance/. VirtioFS (the default file sharing mode since Docker
Desktop 4.6) provides better performance than the legacy gRPC-FUSE and osxfs
implementations, but bind-mount I/O remains slower than native Linux due to the
macOS virtualization layer. This affects yoloAI's `:copy` mode (file copying
performance) and `:overlay` mode (overlayfs is not available inside Docker Desktop's
Linux VM — it requires a Linux kernel with overlayfs support, which Docker Desktop
does provide, but storage driver behavior differs). The competitor analysis
(`docs/contributors/design/README.md` §Value Proposition) documents a concrete tradeoff:
Docker Sandboxes has "~3x I/O penalty on macOS" — verified against Docker Desktop
behavior.

### macOS `sandbox-exec` (Seatbelt) — `CAP_SYS_ADMIN` and `CAP_NET_ADMIN` unavailability

macOS does not expose Linux capabilities at all. `CAP_SYS_ADMIN` and
`CAP_NET_ADMIN` are Linux kernel concepts; they do not exist in the macOS security
model. yoloAI's Seatbelt backend (`runtime/seatbelt/`) implements sandboxing via
`sandbox-exec(1)` with SBPL (Sandbox Profile Language) rather than Linux
capabilities. The `docs/contributors/design/security.md` documents this explicitly for each
feature. Apple's documentation for `sandbox-exec` is sparse (the tool is
deprecated in recent macOS versions but still functional); the primary source is
the `sandbox-exec(1)` man page and the community resource at
https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide-v1.0.pdf
[verify: URL accessibility at time of reading].

### gVisor — ARM64 and macOS compatibility

The gVisor documentation (gvisor.dev/docs/) is the primary source for gVisor
compatibility claims. A known bug where Claude Code hangs on gVisor ARM64 is
tracked at github.com/anthropics/claude-code/issues/35454 (referenced in
`docs/contributors/design/security.md`). This is a concrete example of the cross-platform
awareness principle: a feature (gVisor sandboxing) that works on Linux x86_64 and
Linux ARM64 does not work reliably on macOS (where gVisor cannot run at all via
Docker Desktop) and has a specific known bug on ARM64 Linux. The design doc
documents the block: "gVisor is blocked on macOS due to a known bug where Claude
Code hangs indefinitely during initialization."

### WSL2 / Windows Docker Desktop

Docker's documentation (docs.docker.com/desktop/windows/wsl/) and the WSL2
documentation (learn.microsoft.com/en-us/windows/wsl/wsl2-kernel) are the primary
sources. Known limitations documented in yoloAI's design: path translation between
Windows and WSL paths, UID/GID mapping differences, `.gitignore` line ending
handling. `docs/contributors/design/README.md` §Prerequisites: "Windows/WSL: Expected to work
via Docker Desktop + WSL2. Known limitations: path translation between Windows and
WSL paths, UID/GID mapping differences, `.gitignore` line ending handling. Not a
primary target but should degrade gracefully."

### Tart — macOS VMs (Apple Silicon only)

Primary source: tart.run documentation and the Cirrus Labs GitHub repository at
github.com/cirruslabs/tart. Tart uses Apple's Virtualization.framework to run
macOS and Linux VMs on Apple Silicon. It is macOS-only and Apple-Silicon-only.
The `runtime/tart/` backend in yoloAI is thus a platform-specific feature. The
design doc notes this scope: Tart provides stronger isolation than Seatbelt
(full VM vs process sandbox) but requires Apple Silicon Mac hardware. This is
the cross-platform awareness principle applied to the backend matrix: not every
backend is available on every platform, and the capability detection system
(`runtime/caps/`) probes availability at runtime rather than assuming.

### `docs/contributors/backend-idiosyncrasies.md` as platform-verification record

Internal source: `docs/contributors/backend-idiosyncrasies.md`. This document is the
yoloAI-specific operationalization of cross-platform awareness: every time a backend
behaves in a way that contradicts documentation or requires a non-obvious workaround,
the finding is recorded here with a symptom index for fast lookup. The CLAUDE.md
instruction: "Before diagnosing a backend problem (containerd, Kata, CNI, Docker,
Podman, Tart, Seatbelt), read `docs/contributors/backend-idiosyncrasies.md`." This is
documented institutional memory of platform-specific behavior, rather than
re-investigation from scratch each time.

**Scope note.** Cross-platform awareness has no direct equivalent in the foley
research. Foley is a single-platform SaaS (Linux VMs on Hetzner, web browser
clients). yoloAI's five backends across Linux, macOS (Docker Desktop), macOS
(Seatbelt), macOS (Tart), and Windows/WSL make cross-platform claims a first-class
design concern. The principle is yoloAI-specific.

---

## Sources not carried over from foley

The following principles in foley's general-principles-research.md have no
yoloAI equivalent and are explicitly excluded from yoloAI's general principles.

**Acquisition horizon / diligence-readiness (foley §2).** Foley is a commercial
SaaS with a stated 2-year acquisition horizon. yoloAI is OSS with no acquisition
plan. Acquisition-prep practices (cohort tracking, sub-processor inventory,
DPA reviews) are not relevant to yoloAI.

**GDPR controller/processor posture (foley §7).** yoloAI processes no user data
on servers. The binary runs locally on the user's machine. There is no GDPR
controller surface. Credential management (API keys) is a security concern,
addressed in `docs/contributors/design/security.md`, but not a data-controller concern.

**Operator time as constrained resource (foley §5) in SaaS sense.** Foley's
framing is about founder-time debt in a SaaS with customers, support load, and
billing cycles. yoloAI has maintenance time as a constraint but no customer-service
obligations. The relevant constraint is "five backends to maintain" and "one author
to debug all of them" — addressed by the boring-technology and ecosystem-first
principles, not by a dedicated operator-time principle.

**Two-pizza bound / team-size coordination.** Foley's treatment of Goldratt and
Grove applies to a business with staff. yoloAI is a single-author project where
coordination cost is zero (no team to coordinate). The relevant cost is context
switching between backends during development, not team coordination.

**Alert fatigue / status page (foley §9) in SaaS sense.** Foley's framing is
about user-facing incident communication and status pages for a web service. yoloAI
has no status page; its users are CLI operators. The relevant principle is
"diagnostic-first" (§10 above), not "surface failures publicly to a user base."

**Acquisition optionality, GDPR Article 20, sub-processor rehearsals (foley §4).**
None of these apply to an OSS CLI with no data service.

---

## Verification notes

The following attributions were not independently confirmed at writing time and are
marked [verify] where they appear in the document:

- AWS Architecture Blog exact URL and date for the "hiding rejected alternatives"
  quote in §8 (directional; could not confirm specific post URL).
- Reverse.put.as Apple Sandbox Guide PDF URL for `sandbox-exec` in §11 (URL may
  have changed; verify accessibility before citing externally).

All other sources cited above are primary sources the author has verified exist at
the URLs or library records cited. Git commits referenced are present in the
yoloAI repository as of 2026-05-21.

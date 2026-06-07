ABOUTME: Index for yoloAI's principles docs. Principles explain WHY; standards
ABOUTME: under ../standards/ explain WHAT and HOW. A principle wins over any
ABOUTME: conflicting standard. Each principles doc cites research backing in
ABOUTME: ../research/principles/ and D-entries in ../working-notes.md.

# Principles

The five principles docs that govern yoloAI's engineering disposition. Principles explain **why**; the per-language standards under `../standards/` explain **what** and **how**.

## Layout

```
principles/
├── rule-index.md                 ← fast-lookup rule sheet (one line + symptom per principle)
├── general-principles.md         ← meta: strategic + decision-making (applies to everything)
│   ├── development-principles.md     ← engineering surface: code structure + practices
│   ├── architecture-principles.md    ← architecture surface: module stance + public-surface contract
│   ├── testing-principles.md         ← engineering surface: testing philosophy + discipline
│   └── security-principles.md        ← security surface: sandbox containment + defense
```

`rule-index.md` is the working-memory layer: a one-line **Rule** + **Bites when** symptom for every principle, cheap enough to load whole before a task. Match your action against a row, then open the cited section here for the full reasoning. The five principles docs are the authority; the index only points in.

The four specialised docs reference back to the general parent; the general doc does not duplicate specialised content. When a new cross-cutting pattern is identified, the question is "is this specific to one surface (engineering / architecture / testing / security) or does it apply across all of them?" — if cross-cutting, it lands in `general-principles.md`; otherwise in the relevant specialised doc.

## Index

| File                                                   | Established | Scope                                                                                                                                                                                                                                                                                  |
| ------------------------------------------------------ | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [general-principles.md](general-principles.md)         | D22         | Cross-cutting principles for a single-author OSS CLI: boring tech, innovation-tokens, ecosystem-first, reversibility, blast-radius, safe defaults, factual accuracy, document the no, default to public, design-as-hypothesis (§12, D25), speak-up-against-the-plan (§13, D54). |
| [development-principles.md](development-principles.md) | D22         | Engineering practice: YAGNI / KISS / DRY / SOLID, boundary discipline ("none of your business" — comply-or-complain, both halves: policy owns what/why, mechanism owns how — §2, D27, D56), validate-at-every-layer, parse-don't-validate, raw-until-it-has-to-change (§13, D64), library-defaults-are-safety-only (§14), no-half-finished, plan-then-execute on cleanup, code quality gate, warnings-are-signal. |
| [architecture-principles.md](architecture-principles.md) | D58         | Module stance + public-surface contract: library-first / CLI-as-honesty-keeper (§1, D55/D57), public surface as a contract with mechanical teeth (F1 detector + depguard twins — §2, D55/D57), and the *emerging* binding-lifetime frame (deployment vs principal scope; library never resolves ambient `~`/`${VAR}` — §3, D58, not yet enforced). The stance behind development §2/§12. |
| [testing-principles.md](testing-principles.md)         | D22         | Testing philosophy: confidence over coverage, behavior over implementation, test at the right layer, integration tests hit real backends, regression-by-default. |
| [security-principles.md](security-principles.md)       | D22         | Sandbox security: threat model is bounded, containment not prevention, default-deny credential access, least privilege by mode, agent output untrusted, defense in depth as opt-in layers. |

Each principles doc cites research backing in `../research/principles/` and D-entries in `../working-notes.md`.

## How principles change

Principles are durable. Changing one is the highest-friction documentation move in yoloAI:

1. The change starts as a `../working-notes.md` D-entry that articulates the new principle, its scope, and what it supersedes or refines.
2. The D-entry lands; the principles doc is updated with the change cross-referenced to the D-number.
3. Downstream docs that referenced the prior version may need updates — captured in the D-entry's **Consequences** section.

Standards (`../standards/`) can change without principles changing. Decisions (`../working-notes.md`) can refine principles. Principles change rarely and deliberately.

## Authority

A principle wins over any standard or design choice that conflicts. If a standard contradicts a principle, fix the standard (or the principle, with a deliberate D-entry). If a design doc contradicts a principle, the principle wins by default and the design needs justification.

## How to use these docs

- **Writing code (fast path).** Start at [rule-index.md](rule-index.md): scan the **Bites when** column for the symptom matching what you're about to do, then open the cited section. It's small enough to load whole.
- **Writing code (deep).** When you're about to deviate from convention or add complexity, check the relevant principles doc. Most "should I do X?" questions have an answer here, with reasoning you can cite.
- **Reviewing code (yours or an agent's).** A reviewer can say "this violates `development-principles.md §Validate at every layer`" and the conversation is grounded. The cost-vs-benefit framing in each principle is the resolution mechanism for "is this the right place to draw the line."
- **Onboarding a contributor.** The principles + standards + `CLAUDE.md` are the contract. Anything not covered there is open.

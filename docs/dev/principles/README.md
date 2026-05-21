ABOUTME: Index for yoloAI's principles docs. Principles explain WHY; standards
ABOUTME: under ../standards/ explain WHAT and HOW. A principle wins over any
ABOUTME: conflicting standard. Each principles doc cites research backing in
ABOUTME: ../research/principles/ and D-entries in ../working-notes.md.

# Principles

The four principles docs that govern yoloAI's engineering disposition. Principles explain **why**; the per-language standards under `../standards/` explain **what** and **how**.

## Layout

```
principles/
├── general-principles.md         ← meta: strategic + decision-making (applies to everything)
│   ├── development-principles.md     ← engineering surface: code structure + practices
│   ├── testing-principles.md         ← engineering surface: testing philosophy + discipline
│   └── security-principles.md        ← security surface: sandbox containment + defense
```

The three specialised docs reference back to the general parent; the general doc does not duplicate specialised content. When a new cross-cutting pattern is identified, the question is "is this specific to one surface (engineering / testing / security) or does it apply across all three?" — if cross-cutting, it lands in `general-principles.md`; otherwise in the relevant specialised doc.

## Index

| File                                                   | Established | Scope                                                                                                                                                                                                                                                                                  |
| ------------------------------------------------------ | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [general-principles.md](general-principles.md)         | D22         | Cross-cutting principles for a single-author OSS CLI: boring tech, innovation-tokens, ecosystem-first, reversibility, blast-radius, safe defaults, factual accuracy, document the no, default to public. |
| [development-principles.md](development-principles.md) | D22         | Engineering practice: YAGNI / KISS / DRY / SOLID, boundary discipline, validate-at-every-layer, parse-don't-validate, no-half-finished, plan-then-execute on cleanup, code quality gate, warnings-are-signal. |
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

- **Writing code.** When you're about to deviate from convention or add complexity, check the relevant principles doc. Most "should I do X?" questions have an answer here, with reasoning you can cite.
- **Reviewing code (yours or an agent's).** A reviewer can say "this violates `development-principles.md §Validate at every layer`" and the conversation is grounded. The cost-vs-benefit framing in each principle is the resolution mechanism for "is this the right place to draw the line."
- **Onboarding a contributor.** The principles + standards + `CLAUDE.md` are the contract. Anything not covered there is open.

# GitHub Issue Workflow Design

## Overview

yoloAI generates structured `.md` bug reports via `--bugreport` and `sandbox bugreport`.
This document covers how those reports integrate with the GitHub issue tracker, and the
design of all issue templates used on the project.

---

## Bug Report Issue Workflow

### User journey

1. User encounters a problem.
2. User re-runs the failing command with `--debug --bugreport safe` (or runs
   `yoloai sandbox <name> bugreport safe` after the fact).
3. yoloai writes `yoloai-bugreport-<timestamp>.md` to the current directory.
4. User opens a new GitHub issue, selects the **Bug Report** template.
5. User fills in the problem description at the top.
6. User pastes the entire contents of the `.md` file into the designated section.
7. The pasted report renders as a single collapsible block — minimal visual noise.

### Bugreport format: outer `<details>` wrapper

The existing report format wraps individual sections in `<details>` blocks, but the
top-level header is a bare `## yoloai Bug Report` heading. When pasted into a GitHub
issue, this creates a wall of raw markdown before any collapsed content begins.

**Proposed change:** wrap the entire report in a single outer `<details>` block.

```markdown
<details>
<summary>yoloai bug report — v0.5.1 (abc1234) — 2026-03-16 14:30:00 UTC — safe</summary>

> ⚠️ Review before sharing: this report may contain proprietary code,
> task descriptions, file paths, and internal configuration.

**Version:** 0.5.1 (abc1234, 2026-03-10)
**Type:** safe
**Command:** `yoloai --debug --bugreport safe sandbox mybox start`
**Exit code:** 1 — container failed to start

<details>
<summary>System</summary>
...
</details>

<details>
<summary>Backends</summary>
...
</details>

... (all existing subsections unchanged)

</details>
```

When pasted into a GitHub issue, this renders as a **single collapsed line**:

> ▶ yoloai bug report — v0.5.1 (abc1234) — 2026-03-16 14:30:00 UTC — safe

Click to expand → all subsections appear. The `<summary>` provides at-a-glance triage
information (version, timestamp, safe/unsafe) without expanding anything.

The existing `## yoloai Bug Report — <timestamp>` heading is removed; the `<summary>`
carries that information instead.

### GitHub body size limit

GitHub issue bodies are capped at **65,535 bytes** (not characters — UTF-8 byte length).
The current warn threshold is 65,536, leaving no headroom for the user's problem
description above the pasted report.

**Proposed threshold: 64,000 bytes.** This reserves ~1,500 bytes for the issue template
boilerplate and the user's summary. Users who exceed 64,000 bytes are prompted to use a
GitHub Gist:

```
Warning: bug report is large (>64,000 bytes). Paste a Gist link instead:
  gh gist create --public yoloai-bugreport-20260316-143000.000.md
```

(The Gist can be public or private per user preference; the suggestion uses `--public`
since the report is already `safe` mode. For `unsafe` reports, the warning omits
`--public`.)

---

## Issue Templates

Issue templates live in `.github/ISSUE_TEMPLATE/`. GitHub supports two formats:
- **Markdown (`.md`)** — rendered as a prefilled textarea. Simple, supports free-form
  paste.
- **YAML form (`.yml`)** — structured fields rendered as a web form. Better for
  guided input but does not support raw paste well.

For the bug report template, Markdown is preferred because users paste a large block of
content rather than filling structured fields.

For feature requests and other lightweight templates, YAML forms provide better structure
and allow GitHub to automatically apply labels.

### Templates to create

| Template | Format | Purpose |
|----------|--------|---------|
| Bug Report | `.md` | Problem + pasted bugreport |
| Feature Request | `.yml` | Structured feature proposal |
| Question / Support | `.yml` | Usage questions, configuration help |
| Documentation | `.yml` | Missing or incorrect docs |

### Bug Report template

**File:** `.github/ISSUE_TEMPLATE/bug_report.md`

```markdown
---
name: Bug Report
about: Something isn't working
title: ''
labels: bug
assignees: ''
---

## What happened?

<!-- What were you trying to do? What went wrong? What did you expect instead? -->



## Bug report

<!--
Run the failing command with --debug and --bugreport:

  yoloai --debug --bugreport safe <your command>

Or generate a forensic report from an existing sandbox:

  yoloai sandbox <name> bugreport safe

Then paste the entire contents of the generated .md file below.
If the report is >64,000 bytes, upload it as a Gist and paste the link instead.
-->
```

The template intentionally leaves the "Bug report" section empty — users paste directly
below the comment block. The comment block is hidden after submission.

### Feature Request template

**File:** `.github/ISSUE_TEMPLATE/feature_request.yml`

```yaml
name: Feature Request
description: Suggest a new feature or improvement
labels: ["enhancement"]
body:
  - type: markdown
    attributes:
      value: |
        Thanks for the suggestion. Please be as specific as possible.

  - type: textarea
    id: problem
    attributes:
      label: What problem does this solve?
      description: What are you trying to do that you can't do today?
    validations:
      required: true

  - type: textarea
    id: solution
    attributes:
      label: Proposed solution
      description: How would you like to see this work?
    validations:
      required: true

  - type: textarea
    id: alternatives
    attributes:
      label: Alternatives considered
      description: What workarounds or alternatives have you tried?

  - type: dropdown
    id: scope
    attributes:
      label: Area
      options:
        - Agent support (new agent or agent behavior)
        - Sandbox workflow (create, start, stop, diff, apply)
        - Configuration / profiles
        - Networking / security
        - CLI / UX
        - Performance
        - Documentation
        - Other
    validations:
      required: true
```

### Question / Support template

**File:** `.github/ISSUE_TEMPLATE/question.yml`

```yaml
name: Question / Support
description: Ask a usage question or get help with configuration
labels: ["question"]
body:
  - type: markdown
    attributes:
      value: |
        Check the [guide](../../docs/GUIDE.md) and existing issues first.

  - type: textarea
    id: question
    attributes:
      label: What do you want to do?
      description: Describe what you're trying to accomplish.
    validations:
      required: true

  - type: textarea
    id: tried
    attributes:
      label: What have you tried?
      description: Commands run, config tried, error messages seen.

  - type: input
    id: version
    attributes:
      label: yoloai version
      placeholder: "e.g. 0.5.1 — run: yoloai version"
    validations:
      required: true
```

### Documentation template

**File:** `.github/ISSUE_TEMPLATE/documentation.yml`

```yaml
name: Documentation
description: Missing, incorrect, or unclear documentation
labels: ["documentation"]
body:
  - type: textarea
    id: location
    attributes:
      label: Where is the documentation problem?
      description: Link to the page, section, or command help text.
    validations:
      required: true

  - type: textarea
    id: problem
    attributes:
      label: What's wrong or missing?
    validations:
      required: true

  - type: textarea
    id: suggestion
    attributes:
      label: Suggested improvement
      description: Optional — how should it read instead?
```

---

## Issue Triage

### Labels

| Label | Color | Used for |
|-------|-------|---------|
| `bug` | red | Confirmed or reported bugs |
| `enhancement` | blue | Feature requests |
| `question` | purple | Support/usage questions |
| `documentation` | teal | Doc improvements |
| `needs-info` | yellow | Waiting for more detail from reporter |
| `good first issue` | green | Good entry point for contributors |
| `wontfix` | grey | Deliberate non-fix |

Templates apply `bug`, `enhancement`, `question`, and `documentation` labels
automatically via the `labels` frontmatter field. `needs-info` is applied manually when
a report lacks sufficient detail (e.g. no bugreport file, no steps to reproduce).

### Triage checklist (bug reports)

1. Is a bugreport attached? If not → apply `needs-info`, ask user to run with
   `--bugreport safe`.
2. Is the version in the bugreport current? If old → ask user to reproduce on latest.
3. Is the report `safe` or `unsafe`? `unsafe` reports must not be made public; close
   and ask user to open a private report or share via Gist with limited access.
4. Assign area label if obvious from the bugreport (backend, agent, config, etc.).

---

## Open Questions

- Should the GitHub issue limit check use UTF-8 byte length (accurate but requires
  encoding) or `len(string)` byte count (Go default for `.md` file size stat)? File size
  stat is sufficient for ASCII-heavy reports.
- Should `unsafe` bugreports warn more strongly against public posting? Currently the
  banner is in the report itself; the gist suggestion could add `--private` for unsafe.
- Is a `config.yml` needed to disable blank issues (forcing template selection)?
- Should the outer `<details>` be open by default on `unsafe` reports, since those are
  typically shared only with maintainers privately?

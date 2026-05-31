# GitHub Issue Workflow Design

## Overview

yoloAI generates structured `.md` bug reports via `--bugreport` and `sandbox bugreport`.
This document covers how those reports integrate with the GitHub issue tracker, the
design of all issue templates, and the triage workflow.

---

## Research

Templates and workflows from 7 major projects were examined: aider, continuedev/continue,
cline, cli/cli (gh), docker/cli, hashicorp/terraform, and helm/helm. Key findings that
inform the yoloAI design:

- **YAML form templates (`.yml`)** are the current standard. All studied projects except
  spf13/cobra use them. They offer structured fields, built-in validation, and auto-label
  application. The one exception: bug report templates that require free-form paste of
  large diagnostic blocks are better served by `.md` (see below).

- **`blank_issues_enabled: false`** is the near-universal choice (gh CLI is the only
  holdout). Forces reporters through a template, dramatically improving quality.
  Features and questions are redirected to GitHub Discussions via `contact_links`.

- **Diagnostic fields use `render: bash`** (docker, terraform) to show a shell code
  block placeholder, making it clear the field expects command output.

- **Gist for large diagnostic data** — terraform explicitly tells users not to paste
  large output inline and to use a GitHub Gist instead. gh CLI does this implicitly.
  Applies directly to yoloAI's bugreport files.

- **Helm pre-fills textarea `value:` with `<details>` HTML** so diagnostic output
  collapses by default in the rendered issue. Only project in the sample to do this.

- **Docker auto-applies two labels at creation** (`kind/bug` + `status/0-triage`),
  creating a self-documenting triage queue. The `status/*` progression
  (`0-triage → 1-design-review → ...`) is a Kanban pipeline in label form.

- **Terraform asks whether the config was AI-generated.** Directly applicable to
  yoloAI: knowing if a user's prompt or config was AI-generated helps distinguish
  tool bugs from hallucinated instructions.

- **Removing `needs-info` on new comment** (terraform): a single `issue_comment`
  workflow removes the `needs-info` label when the reporter responds. Trivially simple
  and very effective.

- **gh CLI per-command labels** (`gh-pr`, `gh-issue`, `gh-auth`) let the team filter
  issues to a specific subcommand instantly. Directly applicable to yoloAI (see label
  taxonomy below).

- **Aider's version placeholder** is domain-specific (includes model name, edit format,
  repo size, token count), so reporters automatically provide what matters. The idea of
  a domain-specific placeholder over a generic "run `yoloai version`" hint is worth
  borrowing.

---

## Bug Report Issue Workflow

### User journey

1. User encounters a problem.
2. User re-runs the failing command with `--debug --bugreport safe`
   or runs `yoloai sandbox <name> bugreport safe` for a forensic report.
3. yoloai writes `yoloai-bugreport-<timestamp>.md` to the current directory.
4. User opens a new GitHub issue, selects the **Bug Report** template.
5. User fills in the problem description at the top of the form.
6. If the report is under 64,000 bytes: pastes the entire `.md` file contents into
   the Bug Report field.
   If the report exceeds 64,000 bytes: creates a GitHub Gist and pastes the link.
7. The pasted report renders as a single collapsible block — minimal visual noise.

### Bugreport format: outer `<details>` wrapper

The existing report format wraps individual sections in `<details>` blocks, but the
top-level header is a bare `## yoloai Bug Report` heading. When pasted into a GitHub
issue, this creates a wall of raw markdown above the collapsed sections.

**Change:** wrap the entire report in a single outer `<details>` block.

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

... (all existing subsections unchanged inside the outer block)

</details>
```

When pasted into a GitHub issue, this renders as a **single collapsed line:**

> ▶ yoloai bug report — v0.5.1 (abc1234) — 2026-03-16 14:30:00 UTC — safe

Click to expand → all subsections appear. The `<summary>` provides at-a-glance triage
information (version, timestamp, safe/unsafe) without expanding anything.

The `## yoloai Bug Report — <timestamp>` heading is removed; the `<summary>` carries
that information instead.

### GitHub body size limit

GitHub issue bodies are capped at **65,535 bytes** (UTF-8 byte length, not character
count). The current warn threshold of 65,536 leaves no headroom for the user's problem
description that precedes the pasted report.

**Threshold: 64,000 bytes.** This reserves ~1,500 bytes for template boilerplate and
the user's summary text. The warning message differs by report type:

```
# safe report:
Warning: bug report exceeds 64,000 bytes. Upload as a public Gist instead:
  gh gist create --public yoloai-bugreport-20260316-143000.000.md

# unsafe report:
Warning: bug report exceeds 64,000 bytes. Upload as a private Gist instead:
  gh gist create yoloai-bugreport-20260316-143000.000.md
```

(`gh gist create` defaults to secret/private; `--public` is explicitly added for safe
reports only.)

The size check uses file byte length (from `os.Stat`), which matches GitHub's UTF-8
byte limit for the typical ASCII-heavy report content.

---

## Issue Templates

Templates live in `.github/ISSUE_TEMPLATE/`. A `config.yml` disables blank issues and
redirects features and questions to GitHub Discussions.

### `config.yml`

```yaml
blank_issues_enabled: false
contact_links:
  - name: Feature Request
    url: https://github.com/kstenerud/yoloai/discussions/new?category=ideas
    about: Suggest a new feature or improvement
  - name: Question / Support
    url: https://github.com/kstenerud/yoloai/discussions/new?category=q-a
    about: Ask a usage question or get help with configuration
```

Feature requests and questions are redirected to Discussions rather than issues.
This keeps the issue tracker focused on actionable bugs and documentation gaps.
If Discussions are not enabled, the contact links fall back to a simple external
link (e.g. a docs page) — the `config.yml` is valid either way.

### Template: Bug Report

**File:** `.github/ISSUE_TEMPLATE/bug_report.md`
**Format:** Markdown (`.md`) — needed for free-form paste of the bugreport block.

```markdown
---
name: Bug Report
about: Something isn't working correctly
title: ''
labels: bug, needs-triage
assignees: ''
---

## What happened?

<!-- What were you trying to do? What did you expect? What went wrong? -->



## Bug report

<!--
Generate with one of:

  yoloai --debug --bugreport safe <your command>
  yoloai sandbox <name> bugreport safe

Then paste the entire contents of the generated .md file below.
If the report exceeds 64,000 bytes, create a Gist and paste the link:
  gh gist create --public yoloai-bugreport-<timestamp>.md
-->
```

Notes:
- Two labels applied at creation: `bug` and `needs-triage`. This mirrors the docker
  pattern and creates a self-documenting triage queue.
- The template body ends after the comment block — users paste directly below it.
  The comment is hidden after submission, so the rendered issue shows "Bug report"
  as a section heading followed immediately by the pasted `<details>` block.
- Unsafe reports: the comment block notes that unsafe reports should not be posted
  publicly and suggests a private Gist or direct contact instead.

### Template: Documentation

**File:** `.github/ISSUE_TEMPLATE/documentation.yml`
**Format:** YAML form.

```yaml
name: Documentation
description: Missing, incorrect, or unclear documentation
labels: ["docs", "needs-triage"]
body:
  - type: input
    id: location
    attributes:
      label: Where is the problem?
      description: Link to the page, command help text, or section.
      placeholder: "e.g. https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#profiles"
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

## Label Taxonomy

### Type labels (auto-applied by templates)

| Label | Applied by | Meaning |
|-------|-----------|---------|
| `bug` | bug report template | Reported defect |
| `docs` | documentation template | Documentation gap or error |

### Status labels (manually applied)

| Label | Meaning |
|-------|---------|
| `needs-triage` | Auto-applied at creation; removed once triaged |
| `needs-info` | Waiting for more detail from reporter |
| `confirmed` | Reproduced or accepted |
| `stale` | No activity for 30 days |
| `wontfix` | Deliberate non-fix |

### Component labels (manually applied)

Following the gh CLI pattern of per-command labels for fast filtering:

| Label | Meaning |
|-------|---------|
| `runtime/docker` | Docker backend |
| `runtime/tart` | Tart backend |
| `runtime/seatbelt` | Seatbelt backend |
| `agent/claude` | Claude Code agent |
| `agent/gemini` | Gemini agent |
| `agent/codex` | Codex agent |
| `cmd/new` | `yoloai new` |
| `cmd/diff` | `yoloai diff` |
| `cmd/apply` | `yoloai apply` |
| `cmd/sandbox` | `yoloai sandbox` subcommands |
| `cmd/config` | `yoloai config` |
| `cmd/files` | `yoloai files` |

### Contribution labels

| Label | Meaning |
|-------|---------|
| `good first issue` | Suitable for new contributors |
| `help wanted` | Maintainer would welcome a PR |
| `keep` | Exempt from stale bot closure |

---

## Triage Workflow

### On new bug report

1. Check that a bugreport is attached or linked. If not: apply `needs-info`, ask user to
   run with `--bugreport safe`. Comment template:
   > Could you generate a bug report and attach it? Run:
   > ```
   > yoloai --debug --bugreport safe <your command>
   > ```
   > Then paste the contents of the `.md` file here, or link a Gist if it's large.

2. Check the version in the bugreport. If old: ask user to reproduce on latest before
   investigating.

3. Check report type. `unsafe` reports contain unsanitized agent output and must not be
   left public. If an unsafe report is posted publicly: close the issue with a note
   asking the user to re-open with a `safe` report or share the unsafe version privately.

4. Apply component label if the bugreport makes it clear (e.g. `runtime/docker` if
   backend errors are present, `agent/claude` if Claude-specific).

5. Remove `needs-triage` once the above steps are complete.

### Automation

**Remove `needs-info` on comment** — when a reporter responds to a `needs-info` request,
remove the label automatically. Adapted from the terraform pattern:

```yaml
# .github/workflows/remove-needs-info.yml
name: Remove needs-info on reply
on:
  issue_comment:
    types: [created]
jobs:
  remove_label:
    if: github.event.issue.user.login == github.event.comment.user.login
    runs-on: ubuntu-latest
    steps:
      - uses: actions-ecosystem/action-remove-labels@v1
        with:
          labels: needs-info
```

**Stale bot** — close inactive issues after a reasonable period. Suggested config:

```yaml
# .github/stale.yml (using probot/stale or actions/stale)
daysUntilStale: 60
daysUntilClose: 14
exemptLabels:
  - keep
  - confirmed
  - help wanted
staleLabel: stale
markComment: >
  This issue has had no activity for 60 days. It will be closed in 14 days
  unless there is new activity. Add the `keep` label to exempt it.
closeComment: >
  Closing due to inactivity. Please reopen if this is still a problem on the
  latest version.
```

---

## Open Questions

- **UTF-8 byte check:** the 64,000-byte threshold uses `os.Stat` file size. For reports
  that contain non-ASCII content (non-English prompts, Unicode paths), this
  underestimates the character count. Should we switch to explicit UTF-8 byte counting
  (`len([]byte(content))`) rather than relying on file size? For typical yoloAI reports
  the difference is negligible, but it's technically inaccurate.

- **Unsafe reports and public posting:** should the CLI warn more strongly when
  generating an `unsafe` report? Currently the warning is in the report header itself;
  the gist suggestion already adds `--private` for unsafe. An additional stderr warning
  at generation time ("⛔ This is an unsafe report — do not post publicly") would
  reinforce this.

- **LLM disclosure field:** terraform asks whether the reporter used an AI assistant to
  generate their config. For yoloAI, the analogous question is whether the user's prompt
  or yoloai configuration was AI-generated. This is relevant because yoloAI is itself an
  AI tool, and AI-generated prompts can cause issues that aren't yoloAI bugs. Worth
  adding to the bug report template? Would add friction for all reporters.

- **Discussions vs. issue templates for features/questions:** redirecting features and
  questions to GitHub Discussions requires Discussions to be enabled on the repo. If not
  enabled, the `contact_links` fallback should point somewhere useful (docs, README).

- **`open by default` for unsafe reports:** should the outer `<details>` block be
  `open` (expanded by default) for unsafe reports, since those are typically shared
  privately? The `open` attribute on `<details>` makes it render expanded. Low priority.

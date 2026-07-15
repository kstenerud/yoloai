# Issues

Issues are filed through the forms in [`.github/ISSUE_TEMPLATE/`](../../../.github/ISSUE_TEMPLATE/).
Blank issues are disabled: feature requests and questions route to Discussions via
`contact_links`, so the tracker holds bugs and documentation defects.

The design behind the templates — the field choices, the label scheme, the triage flow, and the
research they came from — is [`../design/github-issues.md`](../design/github-issues.md). Change
the design first, then the templates.

## Filing a bug

Use the bug report template. yoloAI spans several sandbox backends across two host OSes, and
most reports are unactionable without knowing which combination you hit.

The fastest way to give a complete report is to let the tool write it:

```sh
yoloai --debug --bugreport safe <command>          # a one-shot command
yoloai sandbox <name> bugreport safe               # an existing sandbox
```

That writes `yoloai-bugreport-<timestamp>.md` with version, backend, isolation mode, host OS,
and agent already filled in. Paste it into the template.

`safe` redacts; `unsafe` does not. **Do not post an `unsafe` report publicly** — it can contain
credentials and paths. Send it privately, or use a private Gist.

Before filing a backend bug, check [`../backend-idiosyncrasies.md`](../backend-idiosyncrasies.md).
It catalogues backend behaviour that contradicts its own documentation and has a symptom index;
several "bugs" are known upstream quirks with documented workarounds.

## Security

Do **not** open a public issue for a sandbox escape, credential leak, mount escape, or
network-isolation bypass. See [`SECURITY.md`](../../../SECURITY.md).

## Feature requests

They go to Discussions, not the tracker. Check [`../../ROADMAP.md`](../../ROADMAP.md) first —
it lists what is already planned. Say what you are trying to accomplish rather than only the
mechanism you have in mind; the copy/diff/apply model often solves things a different way than
expected.

## Working an issue

An issue does not need a plan file. A PR that resolves it does, if the work spans multiple
commits or phases — see [`pull-requests.md`](pull-requests.md) and
[`AGENTS.md`](../../../AGENTS.md) rule 8.

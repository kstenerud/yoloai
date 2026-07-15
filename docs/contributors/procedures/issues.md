# Issues

This is deliberately thin, and the reason is worth stating: there is barely any issue
practice to document. The repo has a handful of issues, and the labels GitHub created by
default have never been applied to any of them. What follows is the useful minimum, not a
transcription of an existing ritual.

## Filing a bug

yoloAI spans several sandbox backends across two host OSes, and almost every bug report is
unactionable without knowing which combination you hit. Include:

- **`yoloai version`** output.
- **Backend and isolation mode** — Docker, Podman, containerd, Tart, Seatbelt, Apple
  `container`; and `--isolation` if you set it.
- **Host OS** — Linux, macOS, or Windows/WSL. Behaviour genuinely differs, and a bug that
  reproduces on Linux may not on macOS Docker Desktop.
- **Agent** — Claude, Codex, Gemini, Aider, OpenCode.
- **What you ran and what happened**, ideally the smallest command that shows it.

Before filing a backend bug, check
[`../backend-idiosyncrasies.md`](../backend-idiosyncrasies.md). It catalogues backend
behaviour that contradicts its own documentation, with a symptom index — several
"bugs" are known upstream quirks with documented workarounds.

## Security

Do **not** open a public issue for a sandbox escape, credential leak, mount escape, or
network-isolation bypass. See [`SECURITY.md`](../../../SECURITY.md).

## Feature requests

Check [`../../ROADMAP.md`](../../ROADMAP.md) first — it lists what is already planned.
Say what you are trying to accomplish, not only the mechanism you have in mind; the
copy/diff/apply model often solves things a different way than expected.

## Working an issue

An issue does not need a plan file. A PR that resolves it does, if the work spans multiple
commits or phases — see [`pull-requests.md`](pull-requests.md) and
[`AGENTS.md`](../../../AGENTS.md) rule 8.

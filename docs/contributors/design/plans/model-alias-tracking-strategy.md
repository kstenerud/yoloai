> **ABOUTME:** Decide a process to keep model aliases current as providers release new models —
> Gemini's aliases have already drifted once, and Claude's drifted twice before being retired.

# Model alias tracking strategy

- **Status:** UNSPECIFIED — still an idea for gemini/codex/opencode. **Answered for claude**
  (2026-08-19): the alias is passed through and the vendor CLI resolves it, so there is nothing
  left to keep current.
- **Depends on:** —
- **Rides:** **any.**

Model aliases drift as providers release new models. The original framing offered three options —
a manual review cadence, automated checks against provider docs, or pinning to `-latest`
identifiers where they exist. A fourth turned out to be available for Claude and is strictly
better than all three: **don't translate the alias at all.**

## What the claude case established

`ResolveModel` passes any name it does not recognise straight through, so removing an alias from
the table *is* the pass-through. `claude --model opus` then resolves to the current Opus, decided
by the CLI at launch, and yoloAI holds no version knowledge that can go stale. `aider` had always
worked this way; the mapping is now identity for both.

Measured before the change (docker/Linux, Max account):

| `--model` sent to the CLI | Resolves to | Context window |
| --- | --- | --- |
| `opus` | `claude-opus-5` | 1,000,000 |
| `sonnet` | `claude-sonnet-5` | 1,000,000 |
| `haiku` | `claude-haiku-4-5-20251001` | 200,000 |
| `claude-opus-4-6` — *what the pinned table sent* | `claude-opus-4-6` | 200,000 |

Two costs of the pin, and the second was invisible: it served a **previous-generation model**, and
it forfeited the **1M context window**, which the CLI applies only to a model it resolved itself
([DF223](../findings-unresolved.md)). The table said `4-6` while the account's current model was
`5`; `docs/GUIDE.md` and `design/commands.md` both said `4-latest`, describing a pin that had
already been replaced. Three surfaces, three different answers, none of them right — which is the
real argument against any option that requires keeping a number current by hand.

## What is still open

Whether the same trick works for the others is **unverified**, and guessing is how the table got
this way:

- **gemini** — pinned to `gemini-2.5-pro` / `gemini-2.5-flash` plus two preview ids. Does the
  Gemini CLI accept a bare `pro` / `flash`?
- **codex** — pinned to `gpt-5.3-codex`, `gpt-5.3-codex-spark`, `codex-mini-latest`.
- **opencode** — already uses `-latest` ids in `provider/model` form, so it is the least exposed;
  it needs the provider prefix regardless, so full pass-through may not be available.

Each needs one sandbox and the agent's own credentials to answer. Until then they keep the pin,
and keeping it is a decision to accept drift, not an absence of one.

## The general rule this suggests

Prefer, in order: **pass the user's name through** and let the vendor resolve it; failing that, a
provider-supplied `-latest` identifier; failing that, a pinned id — which is a commitment to a
review cadence, and the cadence is the thing that never happens. A pinned id should be exceptional
and should say in a comment why the first two were unavailable.

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §98.

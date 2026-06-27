# Agent self-update in the sandbox — and why the substrate needs no install-method fact

**Verified 2026-06-26** (spike during Stage 3b of the public-layering Move, to resolve
whether `BackendDescriptor.AgentInstallMethod` must stay on the substrate). Two-pronged:
the repo's own usage + authoritative Claude Code behaviour (docs + GitHub issues, via the
claude-code-guide agent). Feeds the **D97 / architecture-principles §4** substrate
surface-cleanup; backs the decision to **delete `AgentInstallMethod`** rather than re-home
or manifest it.

## The question

`BackendDescriptor.AgentInstallMethod` (`"npm-global"` for container backends, `"native"`
for Tart) was the last value-bearing agent field welded onto the substrate descriptor. It is
read only to patch the seeded `~/.claude.json` `installMethod`. The Move wants the public
substrate surface agent-free. Options considered: keep it (fact-query), re-home to the agent
layer, or model it as an image manifest. The deciding question: **does the agent need the
fact at all?**

## Verified facts

1. **Why we patch it today** (`internal/envsetup/envsetup.go ensureHomeSeedConfig`): the
   seeded `.claude.json` comes from the **host**, which usually says `installMethod: native`
   (the user installed Claude natively on their machine). Our **image** installs Claude via
   `npm install -g` (Dockerfile:125, chosen over the native binary deliberately because
   "native bundles Bun which ignores proxy env vars and auto-updates"). The mismatch makes
   Claude warn about a missing `~/.local/bin/claude` and PATH misconfiguration. Patching
   `installMethod` to the image's real method silences it.

2. **What `installMethod` actually drives** (Claude Code docs + GitHub
   [#28625](https://github.com/anthropics/claude-code/issues/28625)): its **only** load-bearing
   use is the **auto-updater** — it selects the update mechanism (native binary vs `npm`). On
   a mismatch the updater takes the wrong path (e.g. deletes the native symlink, runs `npm
   install -g`, rewrites `installMethod`). It is **not** consumed by onboarding, `/status`,
   telemetry, or doctor routing. The PATH warning in (1) is this update machinery reacting to
   the mismatch.

3. **Auto-update can be disabled, first-class:** `DISABLE_AUTOUPDATER=1` (env var, settable in
   `settings.json`'s `env` block) disables background auto-checks; `DISABLE_UPDATES` blocks
   even manual `claude update`. With updates off, **`installMethod` is inert** — it can be
   wrong or absent with no effect on startup, onboarding, or normal operation.

4. **Onboarding gate (orthogonal but load-bearing):** a missing/`false` `hasCompletedOnboarding`
   in `~/.claude.json` forces an onboarding prompt that blocks non-interactive startup
   ([#4714](https://github.com/anthropics/claude-code/issues/4714)). Seeding
   `hasCompletedOnboarding: true` is the workaround — independent of `installMethod`, but must
   stay seeded.

## Decision

**Delete `AgentInstallMethod` from the substrate; eliminate the need rather than model the
fact.** A disposable, image-pinned sandbox should never self-update (the Dockerfile already
engineered around update behaviour), so:

1. **Agent layer:** Claude declares `DISABLE_AUTOUPDATER=1` as static self-config — "don't
   auto-update in the sandbox." Squarely the agent's own operating preference; no substrate
   input.
2. **envsetup:** drop the `installMethod` patching; also strip any stale `installMethod` key
   carried in from the host file so the host's lie does not propagate.
3. **Substrate:** remove `AgentInstallMethod` from `BackendDescriptor`, all four backends, and
   the `SeedSandbox` signature.

No manifest, no location fact, no backend constant — the cross-cut collapses into a one-line
agent policy. The "install method vs. binary location" framing is moot once the consumer
(auto-update) is removed.

## Generalization & residue

- The **pattern** generalizes: each agent owns "don't self-update in the sandbox" as static
  self-config; none of it touches the substrate. The other shipped agents are npm-installed
  too and can get their own update-off setting when wired.
- This leaves **`AgentProvisionedByBackend`** as the one remaining agent-named substrate field
  — a cleaner, separate case (a "does my image ship the agent" fact-query gating *seeding*,
  not a value transcribed into agent config). Its disposition is a separate question.
- Keep seeding `hasCompletedOnboarding: true` (fact 4) so non-interactive startup is
  unaffected by this change.

> **ABOUTME:** Plan for keeping the agent's credential host-side without degrading the agent, by
> redirecting it to the injector through name resolution and TLS rather than through a base-URL
> override. Demonstrated end-to-end on docker; not built.

# Brokering by interception, not by configuration

- **Status:** PLANNED — the mechanism is demonstrated (2026-08-19, docker/Linux), no production
  code written. **Deliberately gated on an upstream answer** — see "Why this may never be built".
- **Depends on:** —
- **Rides:** **any.** Every increment is additive: a backend either gains interception or keeps
  today's base-URL brokering, and `--no-broker` is unaffected either way.

## The problem this solves

Today's injector redirects the agent by **configuration**: `applyBrokerEnv` sets
`ANTHROPIC_BASE_URL` to the injector's address and hands the agent a placeholder token. That is a
boring, correct reverse-proxy deployment, and it costs the user two things ([DF223](../findings-unresolved.md)):
Claude Code reads a non-official base URL as *"you are behind a third-party gateway"*, stops
claiming the account's 1M-token context window, and stops rendering rate-limit usage — even though
the usage headers arrive on every response and pass through the proxy untouched.

The gate is the base URL's **value**, not its presence. A sandbox launched with
`ANTHROPIC_BASE_URL=https://api.anthropic.com` set verbatim keeps both. So the endpoint does not
have to stop being a proxy; it has to stop being *renamed*.

## The mechanism

Redirect by **resolution** instead. Four pieces, three of which already exist:

1. **The placeholder stays exactly as it is.** The guest holds
   `CLAUDE_CODE_OAUTH_TOKEN=<per-sandbox placeholder>` and no real credential. Verified with the
   literal `yoloai-broker-dummy` — nothing depends on Anthropic's token format.
2. **`ANTHROPIC_BASE_URL` is not set at all.** The agent addresses `https://api.anthropic.com`
   because that is where it always sends requests.
3. **The guest resolves that name to the injector** — one `/etc/hosts` line for a container or VM
   guest; a DNS answer where a hosts file is not the right seam.
4. **The injector terminates TLS as `api.anthropic.com`**, presenting a leaf signed by a CA whose
   certificate is installed in the guest trust store and **whose key never leaves the host**. It
   then does what it does today: verify the placeholder, strip it, inject the real credential,
   forward to the fixed upstream.

The unforgeable-reach property is unchanged — the injector still forwards only to its configured
upstream regardless of what the agent asks for.

## What was measured

A spike ran all four pieces against real hardware and a real account (docker/Linux, Claude Code
2.1.228, Opus 5): the agent answered a prompt normally, the statusline read
`🧠 31k/1M (3%) · ⏳ 11% · 📅 16%`, and the sandbox contained no credential but the placeholder.
Node accepted the injected CA via `NODE_EXTRA_CA_CERTS` plus the system store; there was no
certificate pinning to defeat.

**A second route was tried and failed, and the failure is worth keeping.** Forging the credential
*file* — a synthetic `~/.claude/.credentials.json` with the right shape, a placeholder token and a
far-future expiry — started cleanly and completed two `/v1/messages` calls, then the client
invalidated and cleared its own token and reported *"Login expired"*. **No request through the
injector failed**, so whatever revalidates the credential does so out of band, where a proxy cannot
reach it. The environment-variable path is the only one of the two that survives, which is
fortunate: it is also the one that forges less.

## What has to be decided before any code

- **CA custody and lifetime.** Per-sandbox CA or one per host; how long a leaf lives; what happens
  when a long-running sandbox outlives it. A guest-trusted CA becomes a standing property of every
  brokered sandbox. The exposure is bounded — only that guest trusts it, the key stays host-side,
  and the host already owns the box — but it changes what a sandbox is, and that is the owner's
  call, not an implementation detail.
- **The backend that cannot.** Container and VM guests own their `/etc/hosts`. A **Seatbelt**
  sandbox is a process on the user's own machine sharing the host's, with no per-process
  equivalent — so it cannot be redirected this way and needs a stated fallback: keep today's
  degraded brokering, warn and continue, or refuse and make the user pass `--no-broker`. Deciding
  this is deciding what an acceptable failure looks like, so it belongs to the owner.
- **Trust wiring per agent runtime.** Node (Claude, Gemini) reads `NODE_EXTRA_CA_CERTS` and the
  system store — verified for Claude only. Codex is a different runtime with its own trust
  plumbing and is untested; anything that ships must name which agents are covered.
- **What this replaces, if anything.** `BaseURLEnvVar`, `AuthTokenEnvVar`, the placeholder-token
  env plumbing and Codex's config-file patching all exist to tell each agent, in its own dialect,
  where to send traffic. One resolution-level chokepoint could retire all of it — including for
  agents that expose no such knob. Whether to converge them or run both is a scope decision.

## What this is not

It is not containment against a hostile agent, and it must not be described as one. It is the same
credential-protection primitive the injector is today, reached by a different route. An agent with
root in the guest can edit `/etc/hosts` or ignore the trust store; the consequence is that its
requests fail, not that it obtains the credential.

## Why this may never be built

Both halves of [DF223](../findings-unresolved.md) are arguably defects in the client rather than in
the broker — the meters especially, since the client receives the numbers and discards them because
of where the request was addressed. If that is fixed upstream, the boring reverse proxy works and
none of the machinery above is needed. The report costs an hour; this plan costs a workstream. So
the report goes first, and this stays PLANNED until there is an answer.

The cheap partial in the meantime: naming the 1M model variant explicitly (`--model
'claude-opus-5[1m]'`) restores the context window under today's brokering — measured — but not the
meters, and it asserts an entitlement the account may not have, so it is not a safe default.

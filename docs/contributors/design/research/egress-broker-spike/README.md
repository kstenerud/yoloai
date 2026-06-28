# Egress-broker spike — `base_url` redirect + credential injection (D105)

ABOUTME: Records the 2026-06-28 spike that validated the egress-proxy credential-broker's
central mechanism end-to-end with a real agent. The mock here is the seed for the future
injector's integration test.

## What this proves

D105's always-on broker default rests on one mechanic: point the agent at a host-side
proxy via `base_url`, give it a **placeholder** credential, and have the proxy swap in the
real upstream key — so the agent never holds the credential, and it must **not** fall back
to interactive `/login`. This spike confirmed that live with real Claude Code.

## How to run

```sh
# from this directory; needs the yoloai-base image (make build + image build) and Docker.
docker run --rm --entrypoint bash \
  -v "$PWD":/spike:ro \
  -e ANTHROPIC_BASE_URL=http://127.0.0.1:8765 \
  -e ANTHROPIC_AUTH_TOKEN=dummy-broker-token-123 \
  -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
  yoloai-base -lc '
    printf "{\"hasCompletedOnboarding\": true}\n" > "$HOME/.claude.json"
    python3 /spike/mock-anthropic.py 2>/tmp/mock.log &
    sleep 1
    claude -p "Reply with exactly: hello" --output-format text
    echo "=== what claude sent ==="; cat /tmp/mock.log
  '
```

Two non-obvious requirements the spike surfaced:
- `--entrypoint bash` — yoloai-base's ENTRYPOINT (`entrypoint.py`) otherwise swallows the
  command and runs the agent-setup flow instead of the script.
- Pre-seed `~/.claude.json` `{"hasCompletedOnboarding": true}` — else Claude's first-launch
  onboarding check calls `api.anthropic.com` **directly, ignoring `ANTHROPIC_BASE_URL`**
  (Claude Code issue #26935), and stalls if egress is blocked.

## Result (2026-06-28)

```
2.1.195 (Claude Code)
BROKER_SPIKE_OK          # Claude rendered the mock's canned SSE
CLAUDE_EXIT=0            # no /login fallback
[MOCK] POST /v1/messages?beta=true auth='Bearer dummy-broker-token-123'
```

The mock log line is the whole thesis: Claude **redirected to the proxy**, sent the
**dummy bearer** (the exact seam where the injector substitutes the real key), **did not
`/login`**, and **rendered the proxy-supplied stream**. See D105's validation addendum for
the full cross-agent results and the generalized broker shape.

## For the build

**Landed.** This manual spike is now superseded by an automated end-to-end test:
`internal/orchestrator/broker_integration_test.go` (`TestIntegration_CredentialBroker`,
`//go:build integration`, runs under `make integration` / `releasetest`). It uses a Go
`httptest` mock upstream (rather than relocating this `mock-anthropic.py` — a Go mock is
more idiomatic in a Go integration test) and asserts the full real-Docker path: the real
key never enters the container, the agent env is rewritten to the injector + placeholder,
a container→gateway→injector→mock request swaps in the real key host-side, and the
injector is reaped on destroy. This `mock-anthropic.py` stays here as the historical
record of the original manual reproduction (referenced by D105).

Still open: extend with the other agents' wire shapes — OpenAI-`/responses` (Codex),
Chat-Completions (Aider/OpenCode), `generativelanguage` (Gemini) — when those agents gain
`Broker` configs.

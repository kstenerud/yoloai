# macOS verification queue (`substrate-move`)

**ABOUTME:** Verification smokes that need a real Mac (tart VM / seatbelt sandbox / macOS
**ABOUTME:** Keychain), batched so a Mac run knocks them out in one pass before merge.

**Status:** Live queue for the `substrate-move` branch. **Working model:** the Linux session is the
**sole code writer**; this queue holds **verification-only** tasks — run the commands, record
pass/fail + any output, do **not** commit code from the Mac (avoids two-writer branch divergence).
If a smoke fails, report the failure here and the Linux session fixes it. Pull `substrate-move`,
build (`make build` or `go build ./cmd/yoloai`), then work the queue.

Record results inline under each item (`✅ verified <date>` / `❌ <what failed>`), and move
finished items to the "Verified" section at the bottom.

---

## Pending

_(none)_

---

## Verified

### V2 — auth gate detects macOS Keychain credentials (headless not wrongly downgraded) ✅ verified 2026-06-26
**Why Mac:** `agentHasUsableAuth` → `envsetup.HasAnyAuthFile` reads the macOS **Keychain**
(`KeychainService`, claude's `Claude Code-credentials`) — a code path that can't run on Linux. Only
the **claude** agent declares a `KeychainService`; it's a *fallback* used when the host file
`~/.claude/.credentials.json` is absent. The native macOS Claude Code installer (`/login`) stores
subscription OAuth creds in the **login Keychain**, not a file — so the Keychain arm is the primary
auth-detection path for native-install macOS subscription users.
**Host state for this run:** `/login` on this Mac created Keychain item `svce=Claude Code-credentials`,
`acct=karlstenerud` (readable), and **no** `~/.claude/.credentials.json` file → the file check misses
and `KeychainReader` is the *only* possible auth source. Run with **no** `ANTHROPIC_API_KEY` /
`CLAUDE_CODE_OAUTH_TOKEN` in env so the Keychain is unambiguously the sole source.
**Results (`--agent claude … -p '…' --wait --rm`, both backends):**
- **seatbelt** → ran **headless** (no downgrade, no "no auth" error), `Agent finished in sandbox
  mac-kc-sb (done).`, exit 0.
- **tart (VM)** → ran headless, `Agent finished in sandbox mac-kc-tart (done).`, exit 0.

Both runs printed (from claude **inside** the sandbox) `Warning: using OAuth credentials from
~/.claude/.credentials.json` — proof that yoloai read the Keychain secret host-side and
**materialized it into the sandbox's credentials file**, which the agent then authenticated from.
Confirms: `HasAnyAuthFile`'s Keychain detection fires on macOS, the gate stays headless, and the
keychain→file seeding works through both legacy backends (incl. injection into a real VM).

**Also spot-checked (env-var arm / cross-check):** with a real `CLAUDE_CODE_OAUTH_TOKEN` exported
instead (no Keychain reliance), `--agent claude` headless also ran and reached `done` on seatbelt +
tart — the env-var auth path works end-to-end on the macOS legacy launch path too.

### V1 — `yoloai run` headless flow on tart + seatbelt ✅ verified 2026-06-26
**Why Mac:** the headless launch (D100/D101) and `fall_to_shell=off` change the *legacy* launch
path (the non-Docker path tart/seatbelt use: agent launched by `sandbox-setup.py` in the tmux pane,
pane-death → `done`). Only Docker was verified on Linux.
**Setup:** workdir with one file (git-init'd); `--agent test` (credential-free, `HeadlessCmd:
sh -c "PROMPT"`). Built from `350da68` (`go build ./cmd/yoloai`).
**Results (both backends):**
- **seatbelt** success (`-p 'echo OK; exit 0'`) → `Agent finished in sandbox mac-hl-sb (done).`,
  exit 0; `--rm` removed it (`yoloai ls` shows it gone). Failure (`-p 'echo boom; exit 3'`) →
  `agent in sandbox … exited with a non-zero status`, real exit **1** (non-zero), sandbox removed.
- **tart (VM)** success → `Agent finished in sandbox mac-hl-tart (done).`, exit 0, `--rm` removed it.
  Failure (`exit 3`) → `agent … exited with a non-zero status`, real exit **1**, sandbox removed.
**Note:** the failure path surfaces as the message above + non-zero process exit (not a literal
`(failed)` status string, since `--rm` tears the sandbox down). The agent launched, ran, and the
pane reached `done`/propagated failure on both backends — no regression in the legacy launch path.

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

### V1 — `yoloai run` headless flow on tart + seatbelt
**Why Mac:** the headless launch (D100/D101) and `fall_to_shell=off` change the *legacy* launch
path (the non-Docker path tart/seatbelt use: agent launched by `sandbox-setup.py` in the tmux pane,
pane-death → `done`). Only Docker was verified on Linux.
**Setup:** a workdir with any file; a real agent with auth, or use `--agent test` (credential-free,
`HeadlessCmd: sh -c "PROMPT"`).
**Steps (run for each backend):**
```
# tart (VM)
yoloai run mac-hl-tart <workdir> --agent test --backend tart -p 'echo OK; exit 0' --wait --rm
# seatbelt (host sandbox)
yoloai run mac-hl-sb <workdir> --agent test --backend seatbelt -p 'echo OK; exit 0' --wait --rm
```
**Expected:** each prints `Agent finished in sandbox … (done).`, exits 0, and `--rm` removes the
sandbox (`yoloai ls` shows it gone). A non-zero exit prompt (`exit 3`) should give `(failed)` +
non-zero exit. **Watch for:** the agent NOT launching (a regression in how the legacy path builds
the headless command / applies `fall_to_shell`), or the pane never reaching `done`.

### V2 — D101 auth gate detects macOS Keychain credentials (headless not wrongly downgraded)
**Why Mac:** `agentHasUsableAuth` → `envsetup.HasAnyAuthFile` reads the macOS **Keychain**
(`KeychainService`, e.g. Claude's `Claude Code-credentials`) — a code path that can't run on Linux.
A Claude **subscription** user on macOS has creds in the Keychain, not an env var, so the gate must
still see auth and run headless (not fall back to TTY).
**Steps (Claude subscription login on the host, NO `ANTHROPIC_API_KEY` set):**
```
unset ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN
yoloai run mac-auth <workdir> --agent claude -p 'print "hi" and exit' --wait --rm
```
**Expected:** runs **headless** (NO "no usable credentials … running interactively" notice), agent
authenticates from the seeded subscription creds, reaches `done`. **If** it prints the
interactive-fallback notice, `HasAnyAuthFile`'s Keychain detection isn't firing → report (the gate
is over-conservative on macOS). Cross-check: with `ANTHROPIC_API_KEY` set it should also run headless.

---

## Verified

_(none yet)_

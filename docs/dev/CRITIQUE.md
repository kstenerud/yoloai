# Critique: Bug Report and Structured Logging Design

Open questions from reviewing `docs/design/bugreport.md` from an implementer's perspective. Each item needs a design decision before implementation can proceed. Apply resolutions to `docs/design/bugreport.md` and clear this file when done.

---

## 6. `--debug` reaching container processes — timing issue

**Problem:** The doc says `--debug` enables debug-level entries in `sandbox.jsonl` and `monitor.jsonl`. These are Python processes inside the container that read their debug flag from `runtime-config.json` at startup. But `runtime-config.json` is written at container creation time.

**Timing issue:** `yoloai --debug start x` — the container already exists, `runtime-config.json` is already written with whatever `debug` value was set at `new` time. Does `--debug` on `start` affect container-side logging at all, or only the CLI's own `cli.jsonl` output?

---

## 7. JSONL interleaving algorithm and `--follow`

**Problem:** `sandbox <name> log` interleaves four JSONL files by timestamp. The algorithm is unspecified for the `--follow` case in particular:

- **Static:** Read all four files, merge-sort by `ts`, emit. Straightforward.
- **`--follow`:** Tail all four files simultaneously and merge in real-time. A goroutine-per-file with channel merge is the natural Go approach, but polling interval and latency characteristics need specifying.

---

## 9. `sandbox.jsonl` event type taxonomy

**Problem:** Section 8 filters entries by event type (e.g. `entrypoint.setup_cmd`, `entrypoint.network.*`) for `safe` mode. But there is no complete list of event types that `sandbox.jsonl` will emit.

An implementer writing the Python/bash side needs the full taxonomy upfront to know what to emit. An implementer writing the `safe`-mode filter needs the complete list to know what to omit.

---

## 11. ANSI stripping sufficiency for `agent.log`

**Problem:** `agent.log` is a raw terminal recording containing not just SGR color codes but full VT100 sequences: cursor positioning (`[180C[1A`), terminal mode switches (`[?2004l`, `[?2026h`), terminal identification queries (`>0q`, `[c`), bracketed paste, window title sequences. The existing `stripANSI` from `ansi.go` handles SGR codes — it may leave significant noise from these other sequences, producing largely unreadable output for `--agent` mode.

**Options:**
- Use a more comprehensive VT100 stripping library.
- Accept best-effort readability and document the limitation.
- Use `tmux capture-pane -p` for the stripped view (renders to plain text) instead of `agent.log` — but only works on live sessions.

> **ABOUTME:** Holding pen for questions parked as not-now, each carrying a revival trigger.
> Distinct from questions-resolved.md (answered) and questions-abandoned.md (permanently
> dropped).

# Deferred questions

Questions parked as "not now." Unlike [`questions-resolved.md`](questions-resolved.md)
(terminal history), every item here is still potentially actionable and carries a
**`Trigger:`** line — the condition that should pull it back into
[`questions-unresolved.md`](questions-unresolved.md). The trigger may be unlikely, but it
must exist so the item can be evaluated for eviction later. Newest first.

52. ~~**Re-use prompt after destroy**~~ — **Deferred.** `yoloai reset` (#45) covers the main retry case without destroying.

**Trigger:** if users hit a retry need that `yoloai reset` (#45) doesn't cover (e.g. wanting the original prompt re-sent after a full `destroy`).

55. ~~**No way to send a new prompt without attaching**~~ — **Deferred.** Between reset re-sending prompt (#54) and `--prompt-file` for scripting, the gap is small. Add `yoloai prompt` command post-MVP if needed.

**Trigger:** if users request non-interactive prompt delivery beyond `yoloai reset` re-send and `--prompt-file` — implement `yoloai prompt`.

56. ~~**Quick successive tasks have too much ceremony**~~ — **Deferred.** `yoloai run` (create, wait, diff, prompt for apply, auto-destroy) is high-value sugar but the building blocks need to work first. Post-MVP.

**Trigger:** once the core building blocks (`new`/`wait`/`diff`/`apply`) are stable — revisit `yoloai run` (create → wait → diff → prompt-apply → auto-destroy) as high-value sugar.

69. ~~**No inline prompt entry on `yoloai new` without `--prompt`**~~ — **Deferred.** `--prompt`, `--prompt-file`, and `--prompt -` (stdin) cover the bases. `$EDITOR` integration is polish for post-MVP.

**Trigger:** if users request `$EDITOR`-based prompt composition on `yoloai new`.

72. ~~**Shell quoting for `--prompt` is painful**~~ — **Deferred.** Same as #69. `--prompt-file` and stdin already address the pain. `--edit` for `$EDITOR` deferred.

**Trigger:** same as #69 — if `$EDITOR`/`--edit` prompt composition is requested.

96. ~~**Profile env var / agent_args unset mechanism**~~ — **Deferred.** Env vars set to empty string (`MY_VAR: ""`) remain defined in the container, which differs from being absent — scripts using `${MY_VAR+x}` to check for existence will still see the variable. A child profile cannot remove an inherited env var or agent_arg. A sentinel value (e.g. `!unset`) could work but adds complexity to every code path that reads env/agent_args, and users can restructure their profile chain as a workaround. Revisit if users report inheritance conflicts.

**Trigger:** if users report profile-inheritance conflicts where a child can't unset an inherited env var / agent_arg (the `!unset` sentinel becomes worth its complexity cost).

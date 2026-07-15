@AGENTS.md

# Claude Code specifics

Everything above is the shared agent contract (`AGENTS.md`), imported so that Claude Code and
the agents that follow the `AGENTS.md` convention read the same rules. Only Claude-specific
mechanics belong in this file.

## The quality gate runs itself here

`.claude/settings.json` registers two hooks, both committed so any clone picks them up:

- `.claude/hooks/post-edit.sh` stamps the project when a source file is edited.
- `.claude/hooks/on-stop.sh` runs `make check` at end of turn if the stamp exists. On failure
  it **blocks completion** and feeds the output back.

So you rarely need to run `make check` by hand — but it is still the gate, and a change is not
done until it passes.

Two things the hooks do **not** do:

- `post-edit.sh` exempts `docs/*`, so a docs-only edit never stamps and `make check` never runs
  on it. Docs sit outside the gate entirely.
- `on-stop.sh` captures `make check` output and discards it on success, so a target that
  *skips* rather than fails reports that into a void.

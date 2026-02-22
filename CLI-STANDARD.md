# CLI Design Standard

Reference for consistent CLI behavior across all yoloai commands.

Based on POSIX Utility Conventions, GNU Coding Standards, and [clig.dev](https://clig.dev/).

## Argument Ordering

**Options first, then positional arguments** (POSIX Guideline 9). Avoids ambiguity when parsing variable-length positional lists.

```
yoloai <command> [options] <positional-args...>
```

Good: `yoloai new --agent claude --profile gpu my-sandbox ./src ./lib`
Bad:  `yoloai new my-sandbox --agent claude ./src --profile gpu ./lib`

Support `--` to terminate option processing (POSIX Guideline 10):

```
yoloai exec my-sandbox -- ls -la    # everything after -- is passed verbatim
```

Note: Many popular tools (git, docker) silently accept interleaved options via GNU getopt reordering. We enforce options-first as the documented convention but should tolerate interleaved ordering where the parser supports it.

## Flag Naming

- Long flags use `--kebab-case` (not `--snake_case` or `--camelCase`)
- Short flags (`-a`) only for very frequently used options
- Every short flag must have a long form
- Short flags can be grouped: `-vq` is equivalent to `-v -q` (POSIX Guideline 5)
- Boolean flags use `--flag` to enable and `--no-flag` to disable (e.g., `--color` / `--no-color`)
- Flags that take values: `--flag value` (space-separated) and `--flag=value` are both accepted

## Positional Arguments

- Required positionals use `<angle-brackets>` in help text
- Optional positionals use `[square-brackets]` in help text
- Variable-length positionals go last: `<name> <dir> [<dir>...]`
- Never mix optional positionals before required ones

## Standard Flags

Every command supports:

| Flag | Purpose |
|------|---------|
| `--help`, `-h` | Show usage (to stdout, exit 0) |
| `--version` | Show version (to stdout, exit 0; top-level only) |
| `--verbose`, `-v` | Increase output verbosity (stackable: `-vv`) |
| `--quiet`, `-q` | Suppress non-essential output |
| `--no-color` | Disable colored output |
| `--yes`, `-y` | Skip confirmation prompts (where applicable) |

## Output

- Normal output goes to **stdout**
- Errors and warnings go to **stderr**
- Progress indicators and prompts go to **stderr** (so stdout is pipeable)
- `--help` and `--version` output goes to **stdout** (per GNU standards)
- Structured output available via `--json` where useful
- After state-changing operations, suggest logical next commands (e.g., after `yoloai new`, suggest `yoloai attach`)

### Color

Disable color/formatting automatically when any of these is true:

1. `--no-color` flag is passed (highest priority)
2. `NO_COLOR` environment variable is set and non-empty ([no-color.org](https://no-color.org/))
3. stdout/stderr is not a TTY (piped or redirected output)
4. `TERM=dumb`

When color is disabled, also disable progress animations and spinners.

### Progress

- Long-running operations (>1 second) should show progress on stderr
- Disable animations when not a TTY (use static updates or no progress instead)
- First visible response within 100ms of invocation

### Pager

For commands that produce long output (e.g., `yoloai log`), pipe through a pager when stdout is a TTY. Respect `PAGER` environment variable; default to `less -R` (or no pager if unavailable).

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Usage error (bad arguments, missing required args) |
| 3 | Configuration error (bad config file, missing required config) — project-specific |
| 128+N | Terminated by signal N (POSIX convention) |
| 130 | Interrupted by SIGINT / Ctrl+C (128+2) |

## Signal Handling

- On SIGINT (Ctrl+C), print a short message immediately ("interrupted"), then clean up and exit 130
- Do not silently hang during cleanup — if cleanup takes time, indicate what's happening
- On SIGTERM, clean up gracefully and exit 128+15 (143)

## Error Messages

Format: `yoloai: <message>`

- Start with lowercase
- No trailing period
- Include actionable guidance when possible
- Reference the flag or config key that needs fixing

```
Good: yoloai: sandbox 'my-sandbox' not found. Run 'yoloai list' to see available sandboxes
Bad:  Error: NotFoundError - The specified sandbox could not be located in the system.
```

## Confirmation Prompts

- Destructive operations require confirmation: `Destroy sandbox 'my-sandbox'? This cannot be undone. [y/N]`
- Default to the safe option (capital letter = default: `[y/N]` defaults to No)
- Skippable with `--yes` or `-y` for scripting
- Never prompt when stdin is not a TTY — error instead with a message suggesting `--yes`

## Subcommand Structure

- Top-level: `yoloai <command>`
- Prefer flat structure while the command set is small
- If the tool grows beyond ~15 commands or develops clear noun-verb groupings, introduce one level of nesting (e.g., `yoloai profile create`)
- Use verbs for commands: `new`, `start`, `stop`, `destroy`, `list`, `apply`, `log`, `exec`
- `yoloai` with no args shows help
- `yoloai help <command>` works as an alternative to `yoloai <command> --help`

## Help Text

Format for each command:

```
Usage: yoloai <command> [options] <required-arg> [optional-arg]

Description of what the command does (one sentence).

Arguments:
  <name>         Sandbox name
  <dir>          Project directory to mount

Options:
  -a, --agent <agent>     Agent preset (default: claude)
  -p, --profile <name>    Resource profile to use
  -h, --help              Show this help

Examples:
  yoloai new my-sandbox ./my-project
  yoloai new --agent codex --profile gpu my-sandbox ./src ./lib
```

- Order: Usage line, description, arguments, options, examples
- `--help` output goes to stdout and exits 0
- Wrap at 80 columns

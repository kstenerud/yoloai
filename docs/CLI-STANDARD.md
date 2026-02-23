# CLI Design Standard

Reference for consistent CLI behavior across all yoloai commands.

Based on POSIX Utility Conventions, GNU Coding Standards, and [clig.dev](https://clig.dev/).

## Argument Ordering

**Options first, then positional arguments** (POSIX Guideline 9). Avoids ambiguity when parsing variable-length positional lists.

```
yoloai <command> [options] <positional-args...>
```

Good: `yoloai new --profile go-dev --prompt "fix the build" my-sandbox ./src ./lib`
Bad:  `yoloai new my-sandbox --profile go-dev ./src --prompt "fix the build" ./lib`

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

| Flag              | Purpose                                          |
|-------------------|--------------------------------------------------|
| `--help`, `-h`    | Show usage (to stdout, exit 0)                   |
| `--version`       | Show version (to stdout, exit 0; top-level only) |
| `--verbose`, `-v` | Increase output verbosity (stackable: `-vv`)     |
| `--quiet`, `-q`   | Suppress non-essential output                    |
| `--no-color`      | Disable colored output                           |
| `--yes`, `-y`     | Skip confirmation prompts (where applicable)     |

### Verbosity Mapping

| Flags     | Log Level  |
|-----------|------------|
| (default) | Info       |
| `-v`      | Debug      |
| `-q`      | Warn       |
| `-qq`     | Error only |

`-vv` reserved for future trace-level output if needed.

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
- Acknowledge receipt within 100ms (e.g., spinner or status line) — not final output. Go starts in <5ms; this is about UX for slow operations like `yoloai new`

### Pager

For commands that produce long output (`yoloai diff`, `yoloai log`), pipe through a pager when stdout is a TTY. Respect `PAGER` environment variable; default to `less -R` (or no pager if unavailable).

## Exit Codes

| Code  | Meaning                                                                                                                      |
|-------|------------------------------------------------------------------------------------------------------------------------------|
| 0     | Success                                                                                                                      |
| 1     | General error                                                                                                                |
| 2     | Usage error (bad arguments, missing required args) — requires Cobra customization; Cobra returns 1 for all errors by default |
| 3     | Configuration error (bad config file, missing required config) — project-specific                                            |
| 128+N | Terminated by signal N (POSIX convention)                                                                                    |
| 130   | Interrupted by SIGINT / Ctrl+C (128+2)                                                                                       |

## Signal Handling

- On SIGINT (Ctrl+C), print a short message immediately ("interrupted"), then clean up and exit 130
- Do not silently hang during cleanup — if cleanup takes time, indicate what's happening
- On SIGTERM, clean up gracefully and exit 128+15 (143)

## Error Messages

Format: `yoloai: <message>` (requires `SilenceErrors: true` on Cobra's root command with custom error formatting — Cobra's default is `Error: <message>`)

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

Align with Cobra's standard output format (used by kubectl, gh, hugo):

```
Description of what the command does. Can be multiple sentences
for important details.

Usage:
  yoloai new [flags] <name> <dir> [<dir>...]

Flags:
      --profile string   Resource profile to use
      --prompt string    Initial prompt/task for Claude
      --model string     Claude model to use
  -h, --help             Help for new

Global Flags:
  -v, --verbose   Increase output verbosity
  -q, --quiet     Suppress non-essential output
      --no-color  Disable colored output

Examples:
  yoloai new my-sandbox ./my-project
  yoloai new --profile go-dev --prompt "fix the build" my-sandbox ./src ./lib
```

- Long description first, then `Usage:`, `Flags:`, `Global Flags:`, `Examples:`
- Positional args described in the usage line and long description — no separate "Arguments:" section
- `--help` output goes to stdout and exits 0
- Wrap at 80 columns

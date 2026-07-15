> **ABOUTME:** Add a `yoloai batch` command that creates many sandboxes from a task list, so N
> prompts against one workdir can start in parallel instead of N manual `yoloai new` calls.

# Batch sandbox creation

- **Status:** UNSPECIFIED — idea only; command shape (naming, prompt delivery, flag
  inheritance) undecided.
- **Depends on:** —

Add a `yoloai batch` command (or similar) that creates multiple sandboxes from a task list. Input could be a file with one prompt per line, a markdown file with structured specs, or inline arguments. Each task gets its own sandbox against the same workdir. All sandboxes start in parallel.

Example: `yoloai batch ./project tasks.md` creates N sandboxes, one per task in the file.

Design considerations:
- Naming: auto-generate names from task index or allow a prefix (`--prefix feat-`)
- Prompt delivery: each sandbox gets its task as `--prompt-file` or `--prompt`
- Options: inherit shared flags (agent, model, profile, aux dirs) from the batch command
- Output: summary table of created sandboxes

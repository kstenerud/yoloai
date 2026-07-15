> **ABOUTME:** Chain sandboxes sequentially so one stage's applied output becomes the next
> stage's starting state, running a pipeline of agent prompts over a workdir.

# Sandbox chaining (pipelines)

- **Status:** UNSPECIFIED — idea only; pipeline definition format and resume behavior
  undecided.
- **Depends on:** —

Chain sandboxes sequentially so the output of one becomes the input of the next. Each stage runs an agent with its own prompt on the workdir as modified by prior stages.

Example: `yoloai chain ./project pipeline.yaml` runs stages in order, applying each stage's changes before starting the next.

Pipeline definition (YAML or similar) specifies an ordered list of stages, each with:
- Prompt or prompt file
- Agent and model (optional, inherit from defaults)
- Whether to pause for user review between stages (`--step` flag for interactive, default is unattended)

Data flow: stage N's workdir changes are applied (auto-apply) to produce stage N+1's starting state. Intermediate diffs are preserved for inspection. If a stage's agent exits with an error or the user rejects a stage's diff in `--step` mode, the pipeline stops.

Design considerations:
- Compose with batch: independent pipelines could run in parallel
- Resume: if a pipeline stops mid-way, allow resuming from the failed stage
- Naming: sandboxes could be named `<pipeline>-stage-1`, `<pipeline>-stage-2`, etc.
- Keep intermediate sandboxes around for inspection, or clean up on success (`--cleanup`)

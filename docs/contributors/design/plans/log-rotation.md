> **ABOUTME:** Cap and rotate the sandbox directory's `log.txt`, which today grows unbounded
> and can accumulate gigabytes on long-running or high-output sessions.

# Log rotation

- **Status:** UNSPECIFIED — idea only; rotation mechanism undecided.
- **Depends on:** —

`log.txt` in the sandbox directory grows unbounded. There is no rotation or size cap. For long-running sandboxes or sessions that produce a lot of output, this can accumulate gigabytes of log data.

Options: size-based rotation (cap at N MB, keep last N files), integration with `logrotate`, or a `--max-log-size` config key. Low priority but worth addressing before GA.

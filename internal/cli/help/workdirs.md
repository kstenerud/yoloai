WORKDIR MODES AND DIRECTORIES

  The workdir is your primary project directory. By default it is copied
  into the sandbox (safe, isolated). You can change the mode with a suffix.

MODES

  :copy (default)   Isolated copy. Review changes with diff/apply.
  :overlay          Overlay mount. Instant setup, diff/apply workflow.
                    Requires Docker backend. Container must be running for diff.
  :rw               Live bind-mount. Changes are immediate.

     yoloai new task ./my-project           # copy (default)
     yoloai new task ./my-project:overlay   # overlay mount (Docker only)
     yoloai new task ./my-project:rw        # live mount

OVERLAY MODE

  :overlay provides instant sandbox setup using Linux kernel overlayfs inside
  the Docker container. Changes are tracked via diff/apply like :copy mode.

  Tradeoffs vs :copy:
  - No snapshot isolation. Changes to the original directory are visible for
    files the agent hasn't modified.
  - Container must be running for diff/apply (auto-started if stopped).
  - Requires CAP_SYS_ADMIN capability in the container.
  - Docker backend only (not available with seatbelt or tart).

AUXILIARY DIRECTORIES

  Mount extra directories alongside your workdir with -d (repeatable).
  Auxiliary directories are read-only by default.

     yoloai new task . -d /path/to/lib              # read-only
     yoloai new task . -d /path/to/lib:copy          # isolated copy
     yoloai new task . -d /path/to/lib:rw             # writable mount

CUSTOM MOUNT POINTS

  By default, directories mount at their host path. Use =<path> to
  override the container mount point:

     yoloai new task ./app=/opt/app
     yoloai new task . -d ./lib=/opt/lib

MULTIPLE DIRECTORIES

     yoloai new task ./app -d ./shared-lib -d ./common-types

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#workdir-modes

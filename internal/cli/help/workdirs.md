WORKDIR MODES AND DIRECTORIES

  The workdir is your primary project directory. By default it is copied
  into the sandbox (safe, isolated). You can change the mode with a suffix.

MODES

  :copy (default)   Isolated copy. Review changes with diff/apply.
  :rw               Live bind-mount. Changes are immediate.

     yoloai new task ./my-project           # copy (default)
     yoloai new task ./my-project:rw        # live mount

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

WORKFLOW — THE SANDBOX LIFECYCLE

  yoloai follows a create/work/review/apply cycle. Your originals are
  never modified until you explicitly apply changes.

CREATE

  Copy your project into a sandbox and launch the agent:

     yoloai new my-task ./my-project
     yoloai new my-task ./my-project -p "fix the login bug"

ATTACH

  Connect to the agent's terminal session:

     yoloai attach my-task

  Detach with Ctrl-B then D (tmux default).

REVIEW

  See what the agent changed:

     yoloai diff my-task             # full diff
     yoloai diff my-task --stat      # summary only

APPLY

  Apply changes back to your original directory:

     yoloai apply my-task

  A dry-run check runs first, then prompts for confirmation.

RESET

  Re-copy the original and start over:

     yoloai reset my-task
     yoloai reset my-task --clean    # also wipe agent memory

CLEANUP

  Stop or destroy sandboxes when done:

     yoloai stop my-task
     yoloai destroy my-task

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#how-it-works

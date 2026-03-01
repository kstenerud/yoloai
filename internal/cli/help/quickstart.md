YOLOAI QUICK START

  yoloai runs AI coding agents inside disposable sandboxes.
  Your original files stay protected — review changes before applying.

BASIC WORKFLOW

  1. Create a sandbox with a copy of your project:

     yoloai new my-task ./my-project

  2. Attach to watch the agent work (or let it run headless):

     yoloai attach my-task

  3. When the agent is done, review the diff:

     yoloai diff my-task

  4. Apply the changes you want to keep:

     yoloai apply my-task

HEADLESS MODE

  Give the agent a prompt and let it work unattended:

     yoloai new my-task ./my-project -p "refactor the auth module"

NEXT STEPS

  Run 'yoloai help topics' to see all help topics.
  Run 'yoloai help workflow' for the full lifecycle guide.
  Run 'yoloai -h' for command-line options.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md

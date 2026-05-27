YOLOAI QUICK START

  yoloai runs AI coding agents inside disposable sandboxes.
  Your original files stay protected — review changes before applying.

INTERACTIVE

  1. Create a sandbox and attach to the agent's terminal:

     yoloai new my-task ./my-project -a

  2. Work with the agent interactively, then detach (Ctrl-B D).

  3. Review the diff and apply changes you want to keep:

     yoloai diff my-task
     yoloai apply my-task

HEADLESS

  Give the agent a prompt and let it work unattended:

     yoloai new my-task ./my-project -p "refactor the auth module"

  Check back later:

     yoloai diff my-task
     yoloai apply my-task

NEXT STEPS

  Run 'yoloai help topics' to see all help topics.
  Run 'yoloai help workflow' for the full lifecycle guide.
  Run 'yoloai -h' for command-line options.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md

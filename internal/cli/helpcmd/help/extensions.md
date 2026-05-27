EXTENSIONS — USER-DEFINED CUSTOM COMMANDS

  Extensions are shell scripts wrapped in YAML that add custom commands
  to yoloai. They compose yoloai with unix tools like gh, jq, and git.

  Run extensions with:     yoloai x <name> [args...] [--flags...]
  List extensions:         yoloai x

CREATING AN EXTENSION

  Add a YAML file to ~/.yoloai/extensions/:

     # ~/.yoloai/extensions/from-issue.yaml
     description: "Create a sandbox from a GitHub issue"

     agent: claude            # optional: restrict to specific agent(s)

     args:
       - name: issue
         description: "GitHub issue number or URL"

     flags:
       - name: repo
         short: r
         description: "GitHub repo (owner/name)"
         default: ""

     action: |
       title=$(gh issue view "$issue" --repo "$repo" --json title -q .title)
       body=$(gh issue view "$issue" --repo "$repo" --json body -q .body)
       yoloai new "issue-${issue}" . -p "Fix: ${title}\n\n${body}"

  The extension name comes from the filename (e.g., from-issue.yaml -> from-issue).

EXTENSION FORMAT

  description    Short description shown in 'yoloai x' listing
  agent          Optional. String or list of agent names to restrict to
  args           Positional arguments (parsed in definition order)
  flags          Named flags with optional short form and default value
  action         Shell script executed via sh -c

  All args and flags are passed as environment variables. Flag names
  with hyphens become underscores (e.g., --my-flag -> $my_flag).
  The current agent name is available as $agent.

INSTALLING EXTENSIONS

  Extensions are standalone YAML files — just copy them into
  ~/.yoloai/extensions/. No package manager needed.

     mkdir -p ~/.yoloai/extensions
     cp from-issue.yaml ~/.yoloai/extensions/

CONSTRAINTS

  - Extension names must not collide with built-in commands.
  - Flag names/shorts must not collide with global flags (-v, -q, -y, -h).
  - The action field is required.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/design/commands.md#yoloai-x-extensions

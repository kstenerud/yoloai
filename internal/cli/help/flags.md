KEY FLAGS

GLOBAL FLAGS

  -v             Verbose output (-v for debug, -vv reserved)
  -q             Quiet output (-q for warn, -qq for error only)
  --no-color     Disable colored output

CREATING SANDBOXES (yoloai new)

  --agent <name>      Agent to use (claude, gemini, etc.)
  --model, -m <name>  Model name or alias
  --backend <name>    Runtime backend (docker, tart, seatbelt)
  --prompt, -p <text> Prompt for headless mode
  --prompt-file, -f   File containing the prompt
  --dir, -d <path>    Auxiliary directory (repeatable)
  --port <h:c>        Port mapping (host:container)
  --network-none      Disable network access
  --network-isolated  Allow only agent API traffic (iptables allowlist)
  --network-allow     Extra domain to allow (repeatable, implies --network-isolated)
  --attach, -a        Auto-attach after creation
  --replace           Replace existing sandbox with same name
  --no-start          Create without starting
  --yes, -y           Skip confirmations

REVIEWING AND APPLYING

  yoloai diff <name> --stat       Summary only
  yoloai diff <name> -- <paths>   Filter to specific files
  yoloai apply <name> --yes       Skip confirmation

LIFECYCLE

  yoloai stop --all               Stop all sandboxes
  yoloai destroy --all --yes      Destroy all sandboxes
  yoloai reset <name> --clean     Reset and wipe agent memory
  yoloai reset <name> --no-prompt Don't re-send prompt on reset

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md

KEY FLAGS

GLOBAL FLAGS

  -v             Verbose output (-v for debug, -vv reserved)
  -q             Quiet output (-q for warn, -qq for error only)
  --json         Output as JSON (machine-readable)

CREATING SANDBOXES (yoloai new)

  --agent <name>      Agent to use (claude, gemini, etc.)
  --model, -m <name>  Model name or alias
  --backend <name>    Runtime backend (docker, podman, tart, seatbelt,
                      containerd)
  --prompt, -p <text> Prompt for headless mode
  --prompt-file, -f   File containing the prompt
  --dir, -d <path>    Auxiliary directory (repeatable)
  --port <h:c>        Port mapping (host:container)
  --network-none      Disable network access
  --network-isolated  Allow only agent API traffic (IPv4 iptables allowlist;
                      IPv6 is not filtered)
  --network-allow     Extra domain to allow (repeatable, implies --network-isolated)
  --attach, -a        Auto-attach after creation
  --replace            Replace an existing sandbox of the same name
  --abandon-unapplied  Replace even when it has unapplied changes (implies --replace)
  --no-start          Create without starting
  --allow-dirty       Proceed even if the workdir has uncommitted changes
  --cpus <num>        CPU limit (e.g., 4, 2.5)
  --memory <size>     Memory limit (e.g., 8g, 512m)
  --isolation <mode>  Isolation mode: container (default),
                      container-enhanced (gVisor), container-privileged
                      (--privileged, for Docker-in-Docker workloads),
                      vm (Kata+QEMU), vm-enhanced (Kata+Firecracker).
                      VM modes require containerd backend; both are
                      experimental.
  --os <name>         Target OS: linux (default), mac
  --vscode-tunnel     Launch a VS Code Remote Tunnel (connect from VS Code
                      on any machine via vscode.dev/tunnel/<name>)
                      See: yoloai help vscode-tunnel

REVIEWING AND APPLYING

  yoloai diff <name> --stat       Summary only
  yoloai diff <name> -- <paths>   Filter to specific files
  yoloai apply <name> --yes       Skip confirmation

LIFECYCLE

  yoloai stop --all               Stop all sandboxes
  yoloai destroy --all            Destroy all sandboxes
  yoloai destroy <name> --abandon-unapplied  Destroy despite unapplied work
  yoloai reset <name> --clear-state  Reset and wipe agent state
  yoloai reset <name> --no-prompt Don't re-send prompt on reset
  yoloai reset <name> --abandon-unapplied  Reset despite unapplied work

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md

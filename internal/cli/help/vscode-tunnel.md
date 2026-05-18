VS CODE REMOTE TUNNEL

  Launch a VS Code Remote Tunnel alongside the agent so you can connect
  from VS Code on any machine — your laptop, a browser, anywhere.

  The tunnel runs in a separate tmux window inside the sandbox. Your
  project files are visible directly in VS Code without any file copying.

CREATE WITH TUNNEL

  Pass --vscode-tunnel when creating a sandbox:

     yoloai new my-task ./my-project --vscode-tunnel
     yoloai new my-task ./my-project --vscode-tunnel --agent claude

ADD TO AN EXISTING SANDBOX

  Enable the tunnel on next restart with --vscode-tunnel:

     yoloai restart my-task --vscode-tunnel

FIRST-RUN AUTHENTICATION

  The first time you use a tunnel, VS Code CLI shows a license agreement
  then prompts you to choose an auth provider (Microsoft or GitHub).
  Switch to the tunnel window to complete both steps:

     1. Attach to the sandbox:   yoloai attach my-task
     2. Switch windows:          Ctrl-b n  (next window)
     3. Select your auth provider and follow the link shown
     4. Return to the agent:     Ctrl-b p  (previous window)

  After authenticating, the tunnel URL appears:

     Open this link in your browser https://vscode.dev/tunnel/my-task

CREDENTIAL PERSISTENCE

  Auth tokens are stored in ~/.yoloai/vscode-cli/ on the host and
  bind-mounted into every sandbox as ~/.vscode/cli/. You authenticate
  once — all future sandboxes and restarts reuse the tokens automatically.

  To force re-authentication, remove the stored credentials:

     rm -rf ~/.yoloai/vscode-cli/

CONNECTING

  Open the tunnel URL in a browser, or use the VS Code desktop app:

     1. Install the "Remote - Tunnels" extension
     2. Open the Command Palette → "Remote Tunnels: Connect to Tunnel"
     3. Select your tunnel by name

  Tunnel name is derived from the sandbox name (lowercase, max 20 chars).

LOGS

  Tunnel output is logged to /yoloai/logs/vscode-tunnel.log inside
  the container. Access it from within the sandbox or via:

     yoloai exec my-task -- cat /yoloai/logs/vscode-tunnel.log

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md

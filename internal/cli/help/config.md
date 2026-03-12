CONFIGURATION

  yoloai uses two config files:
  - ~/.yoloai/config.yaml — global settings (tmux_conf, model_aliases)
  - ~/.yoloai/profiles/base/config.yaml — profile defaults (agent, model, etc.)

  On first run, interactive setup creates these files. Use 'yoloai config'
  to view and change settings (keys are automatically routed to the correct file).

COMMANDS

     yoloai config get                # show all settings
     yoloai config get <key>          # show a specific setting
     yoloai config set <key> <value>  # change a setting
     yoloai config reset <key>        # revert to default

KEY SETTINGS

  agent            Agent to use (default: claude)
  model            Model name or alias (default: agent's default)
  backend          Runtime backend: docker, tart, seatbelt
  tmux_conf        Tmux config mode: default+host, default, host, none
  env.<NAME>       Environment variable forwarded to container

EXAMPLES

     yoloai config set agent gemini
     yoloai config set model sonnet
     yoloai config set backend tart
     yoloai config set env.OLLAMA_API_BASE \
       http://host.docker.internal:11434
     yoloai config reset model

  You can also edit ~/.yoloai/config.yaml directly.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#configuration

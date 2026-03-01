CONFIGURATION

  yoloai stores config in ~/.yoloai/config.yaml. On first run, interactive
  setup creates this file. Use 'yoloai config' to view and change settings.

COMMANDS

     yoloai config get                # show all settings
     yoloai config get <key>          # show a specific setting
     yoloai config set <key> <value>  # change a setting
     yoloai config reset <key>        # revert to default

KEY SETTINGS

  defaults.agent       Agent to use (default: claude)
  defaults.model       Model name or alias (default: agent's default)
  defaults.backend     Runtime backend: docker, tart, seatbelt
  defaults.tmux_conf   Tmux config mode: default+host, default, host, none
  defaults.env.<NAME>  Environment variable forwarded to container

EXAMPLES

     yoloai config set defaults.agent gemini
     yoloai config set defaults.model sonnet
     yoloai config set defaults.backend tart
     yoloai config set defaults.env.OLLAMA_API_BASE \
       http://host.docker.internal:11434
     yoloai config reset defaults.model

  You can also edit ~/.yoloai/config.yaml directly.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#configuration

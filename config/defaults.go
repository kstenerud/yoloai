package config

// ABOUTME: Default YAML content for profile and global config files.

// DefaultConfigYAML is the default content for a new profile config.yaml.
const DefaultConfigYAML = `# yoloai base profile configuration
# See https://github.com/kstenerud/yoloai for documentation
# Run 'yoloai config set <key> <value>' to change settings.
#
# Available settings:
#   agent              Agent to use: aider, claude, codex, gemini, opencode
#   model              Model name or alias passed to the agent
#   container_backend  Runtime backend: docker, podman, containerd (auto-detect if unset)
#   isolation          Isolation mode: container, container-enhanced, vm, vm-enhanced
#   tart.image         Custom base VM image (tart backend only)
#   env.<NAME>         Environment variable passed to container

{}
`

// DefaultGlobalConfigYAML is the default content for the global config.yaml.
const DefaultGlobalConfigYAML = `# yoloai global configuration
# These settings apply to all sandboxes regardless of profile.
# Run 'yoloai config set <key> <value>' to change settings.
#
# Available settings:
#   tmux_conf                Tmux configuration: default, default+host
#   model_aliases.<alias>    Custom model alias (overrides agent built-in aliases)

{}
`

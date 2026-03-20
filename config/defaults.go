package config

// ABOUTME: Default YAML content for profile and global config files.

import "strings"

// DefaultConfigYAML is the baked-in defaults YAML. Contains every profile/defaults
// setting with its default value and inline documentation. This is the authoritative
// source of truth for all defaults — all merge paths fall back to it.
const DefaultConfigYAML = `# --- Agent ---

# Agent to launch inside the sandbox.
# Valid values: aider, claude, codex, gemini, opencode
# CLI --agent overrides.
agent: claude

# Model name or alias passed to the agent. Empty = agent's own default.
# CLI --model overrides.
model: ""

# --- Runtime ---

# Guest OS for the sandbox. Valid values: linux (default), mac.
#   os=linux: Docker or Podman for Linux containers; containerd for vm/vm-enhanced
#   os=mac: Seatbelt (container isolation) or Tart (vm isolation); requires macOS host
# CLI --os overrides.
os: linux

# Preferred Linux container backend. Valid values: docker, podman, or "" (auto-detect).
# Both work on Linux and macOS. Ignored for vm/vm-enhanced (uses containerd)
# and os=mac (uses Seatbelt or Tart). CLI --backend overrides.
# Empty string (default): auto-detect — prefers docker over podman if both are present.
container_backend: ""

# Isolation level for the sandbox.
# Valid values: container, container-enhanced, vm, vm-enhanced
#   container:          os=linux: Docker or Podman; os=mac: Seatbelt
#   container-enhanced: os=linux: Podman required; os=mac: not supported
#   vm:                 os=linux: KVM + Kata required; os=mac: Tart required
#   vm-enhanced:        os=linux: KVM + Kata + Firecracker required; os=mac: not supported
# CLI --isolation overrides.
isolation: container

# --- Tart (macOS VM backend) ---

# Custom base VM image for the Tart backend (os=mac, isolation=vm).
tart:
  image: ""

# --- Network ---

# Network isolation settings.
network:
  # Set to true to enable network isolation for all sandboxes.
  isolated: false
  # Additional domains to allow when isolation is active (additive with agent defaults).
  allow: []

# --- Files and mounts ---

# Files copied into the sandbox's agent-state directory on first run.
# String form: base directory (agent subdir appended), e.g. "${HOME}"
# List form:   specific files or dirs, e.g. ["~/.claude/settings.json"]
# Omit to copy nothing (safe default).
# WARNING: ${HOME} and other env vars are expanded at runtime — values differ per machine.
# agent_files: ""

# Extra bind mounts added at container run time.
# Format: host-path:container-path[:ro]
# WARNING: paths are machine-specific. Personal mounts belong in defaults/, not profiles.
mounts: []

# --- Ports and resources ---

# Default port mappings. Format: host-port:container-port
ports: []

# Container resource limits.
resources:
  cpus: ""
  memory: ""

# --- Agent behaviour ---

# Per-agent default CLI args inserted before -- passthrough args.
# Set per agent: agent_args.aider, agent_args.claude, etc.
agent_args: {}

# Environment variables forwarded to the container via /run/secrets/.
# Supports ${VAR} expansion. WARNING: expanded values are machine-specific.
env: {}

# Seconds between automatic git commits in :copy directories. 0 = disabled.
auto_commit_interval: 0

# --- Advanced ---

# Linux capabilities to add (Docker/Podman only).
cap_add: []

# Host devices to expose (Docker/Podman only).
devices: []

# Commands to run at container start before the agent launches.
setup: []
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

// GenerateScaffoldConfig takes the baked-in defaults YAML and returns a version
// where every non-blank, non-comment line is commented out. The result is suitable
// for writing as defaults/config.yaml on first run — self-documenting but inert
// until the user uncomments and edits specific settings.
func GenerateScaffoldConfig(bakedInYAML string) string {
	var out strings.Builder
	for _, line := range strings.Split(bakedInYAML, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out.WriteString(line + "\n")
		} else {
			out.WriteString("# " + line + "\n")
		}
	}
	return out.String()
}

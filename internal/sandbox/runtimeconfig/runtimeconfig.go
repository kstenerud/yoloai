// ABOUTME: The Go↔Python runtime-config.json contract: the serializable
// ABOUTME: ContainerConfig written by create/ and read back by lifecycle/, plus
// ABOUTME: its versioned schema. Pure data — no behavior.
package runtimeconfig

import (
	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// SchemaVersion is the contract version between Go (writer) and Python
// (reader, via sandbox-setup.py and status-monitor.py) for
// runtime-config.json. Bump when adding a required field, removing a field,
// renaming, or changing the semantics of any field. Additive changes (new
// optional fields with sensible defaults on both sides) do NOT require a bump.
// W2 of the architecture remediation plan.
const SchemaVersion = 1

// OverlayMountConfig describes a single overlay mount for config.json.
type OverlayMountConfig struct {
	Lower  string `json:"lower"`
	Upper  string `json:"upper"`
	Work   string `json:"work"`
	Merged string `json:"merged"`
}

// LifecycleConfig describes lifecycle command execution for a sandbox.
type LifecycleConfig struct {
	DockerDRequired bool             `json:"dockerd_required"`
	OnCreateDone    bool             `json:"on_create_done"`
	OnCreate        []map[string]any `json:"on_create,omitempty"`
	OnStart         []map[string]any `json:"on_start,omitempty"`
}

// ContainerConfig is the serializable form of runtime-config.json written by
// the create pipeline. lifecycle.go reads it back to extract agent command,
// tmux config, socket, passthrough args, and ready/startup settings for
// container restart and prompt delivery.
type ContainerConfig struct {
	SchemaVersion      int                   `json:"schema_version"`
	HostUID            int                   `json:"host_uid"`
	HostGID            int                   `json:"host_gid"`
	AgentCommand       string                `json:"agent_command"`
	AgentLaunchPrefix  string                `json:"agent_launch_prefix"`
	StartupDelay       int                   `json:"startup_delay"`
	ReadyPattern       string                `json:"ready_pattern"`
	SubmitSequence     string                `json:"submit_sequence"`
	TmuxConf           string                `json:"tmux_conf"`
	WorkingDir         string                `json:"working_dir"`
	StateDirName       string                `json:"state_dir_name"`
	Debug              bool                  `json:"debug,omitempty"`
	NetworkIsolated    bool                  `json:"network_isolated,omitempty"`
	AllowedDomains     []string              `json:"allowed_domains,omitempty"`
	Passthrough        []string              `json:"passthrough,omitempty"`
	OverlayMounts      []OverlayMountConfig  `json:"overlay_mounts,omitempty"`
	SetupCommands      []string              `json:"setup_commands,omitempty"`
	AutoCommitInterval int                   `json:"auto_commit_interval,omitempty"`
	CopyDirs           []string              `json:"copy_dirs,omitempty"`
	HookIdle           bool                  `json:"hook_idle,omitempty"`
	Idle               agent.IdleSupport     `json:"idle"`
	Detectors          []string              `json:"detectors,omitempty"`
	SandboxName        string                `json:"sandbox_name"`
	TmuxSocket         string                `json:"tmux_socket,omitempty"`
	Isolation          runtime.IsolationMode `json:"isolation,omitempty"`
	VscodeTunnel       bool                  `json:"vscode_tunnel,omitempty"`
	VscodeTunnelName   string                `json:"vscode_tunnel_name,omitempty"`
	Lifecycle          *LifecycleConfig      `json:"lifecycle,omitempty"`
}

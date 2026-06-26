// ABOUTME: The Go↔Python runtime-config.json contract: the serializable
// ABOUTME: ContainerConfig written by create/ and read back by lifecycle/, plus
// ABOUTME: its versioned schema. Pure data — no behavior.
package runtimeconfig

import (
	"github.com/kstenerud/yoloai/internal/runtime"
)

// IdleSupport is the wire form of an agent's idle-detection capabilities as
// serialized into runtime-config.json for the Python status-monitor. Field
// names are the JSON keys (no tags) and MUST match what the monitor reads. It
// mirrors agent.IdleSupport but is owned here so this serialization layer does
// not import the agent package — the orchestration layer maps between them.
type IdleSupport struct {
	Hook            bool
	ReadyPattern    string
	ContextSignal   bool
	WchanApplicable bool
}

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
	SchemaVersion      int                  `json:"schema_version"`
	HostUID            int                  `json:"host_uid"`
	HostGID            int                  `json:"host_gid"`
	AgentCommand       string               `json:"agent_command"`
	AgentLaunchPrefix  string               `json:"agent_launch_prefix"`
	StartupDelay       int                  `json:"startup_delay"`
	ReadyPattern       string               `json:"ready_pattern"`
	SubmitSequence     string               `json:"submit_sequence"`
	TmuxConf           string               `json:"tmux_conf"`
	WorkingDir         string               `json:"working_dir"`
	StateDirName       string               `json:"state_dir_name"`
	Debug              bool                 `json:"debug,omitempty"`
	NetworkIsolated    bool                 `json:"network_isolated,omitempty"`
	AllowedDomains     []string             `json:"allowed_domains,omitempty"`
	Passthrough        []string             `json:"passthrough,omitempty"`
	OverlayMounts      []OverlayMountConfig `json:"overlay_mounts,omitempty"`
	SetupCommands      []string             `json:"setup_commands,omitempty"`
	AutoCommitInterval int                  `json:"auto_commit_interval,omitempty"`
	CopyDirs           []string             `json:"copy_dirs,omitempty"`
	HookIdle           bool                 `json:"hook_idle,omitempty"`
	Idle               IdleSupport          `json:"idle"`
	// IdleMode selects how the status monitor determines active/idle: the
	// per-agent mode selector (session-layer.md §Tier-2). "hook-authoritative" =
	// the agent's hook is the sole idle authority (no heuristics, no startup
	// blip); "heuristic-only" = the detector stack. Absent → heuristic-only
	// (back-compat for sandboxes created before the selector). Additive optional
	// field → no SchemaVersion bump.
	IdleMode  string   `json:"idle_mode,omitempty"`
	Detectors []string `json:"detectors,omitempty"`
	// FallToShell, when true, launches the agent under the fall-to-shell wrapper
	// (agent-run.sh, D96): on agent exit the wrapper records an authoritative
	// `done` and keeps the pane alive as an interactive shell instead of letting
	// it die. The persistent-PTY gate (invocation.ResolveFallToShell): on for every
	// agent today, eventually driven off for one-shot sessions by the session-layer
	// `lifetime` axis. Absent → off (back-compat for sandboxes created before the
	// wrapper). Additive optional field → no SchemaVersion bump.
	FallToShell bool `json:"fall_to_shell,omitempty"`
	// ResumeCmd is the fall-to-shell resume command (D96 DD4): the agent's launch
	// command plus its native resume flag (e.g. Claude "… --continue"), read by
	// the in-sandbox yoloai-resume script to continue the prior conversation. ""
	// when the agent has no native resume → yoloai-resume relaunches fresh and
	// says so. Additive optional field → no SchemaVersion bump.
	ResumeCmd        string                `json:"resume_cmd,omitempty"`
	SandboxName      string                `json:"sandbox_name"`
	TmuxSocket       string                `json:"tmux_socket,omitempty"`
	Isolation        runtime.IsolationMode `json:"isolation,omitempty"`
	VscodeTunnel     bool                  `json:"vscode_tunnel,omitempty"`
	VscodeTunnelName string                `json:"vscode_tunnel_name,omitempty"`
	Lifecycle        *LifecycleConfig      `json:"lifecycle,omitempty"`
	// KeepaliveOnly, when true, brings the box up on a neutral agent-free
	// keep-alive (`sleep infinity`) after the root setup, instead of launching
	// the agent session — the carve's agent-free substrate bring-up. The
	// orchestrator sets it when routing the agent through Launch over a
	// keepalive box (launch.startViaLaunch → patchKeepaliveOnly); the entrypoint
	// honors it.
	KeepaliveOnly bool `json:"keepalive_only,omitempty"`
}

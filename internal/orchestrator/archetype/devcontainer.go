// ABOUTME: Parses the subset of devcontainer.json fields that yoloAI uses.
// ABOUTME: Provides filtering, port extraction, env merging, and run-args parsing.

package archetype

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
)

// LifecycleCmd holds one devcontainer lifecycle command in any of the three
// spec-defined forms: string, []string, or map[string]any (parallel named commands).
type LifecycleCmd struct {
	raw any
}

// UnmarshalJSON accepts string, []string, or object forms.
func (c *LifecycleCmd) UnmarshalJSON(b []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.raw = s
		return nil
	}
	// Try array
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		c.raw = arr
		return nil
	}
	// Try object (parallel named commands)
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err == nil {
		c.raw = obj
		return nil
	}
	return fmt.Errorf("lifecycle command must be string, array, or object")
}

// MarshalJSON serializes the lifecycle command back to JSON.
func (c LifecycleCmd) MarshalJSON() ([]byte, error) {
	if c.raw == nil {
		return []byte("null"), nil
	}
	return json.Marshal(c.raw)
}

// IsZero reports whether no command was specified.
func (c *LifecycleCmd) IsZero() bool {
	return c.raw == nil
}

// Raw returns the underlying value (string, []string, or map[string]any).
func (c *LifecycleCmd) Raw() any {
	return c.raw
}

// DevcontainerConfig holds the parsed subset of devcontainer.json that yoloAI uses.
type DevcontainerConfig struct {
	// Image resolution (stream 3 — not used yet, parsed for future)
	Image        string `json:"image,omitempty"`
	BuildPresent bool   `json:"-"`

	ForwardPorts []int             `json:"forwardPorts,omitempty"`
	AppPort      []int             `json:"appPort,omitempty"`
	RemoteEnv    map[string]string `json:"remoteEnv,omitempty"`
	ContainerEnv map[string]string `json:"containerEnv,omitempty"`

	OnCreateCommand      LifecycleCmd `json:"onCreateCommand"`
	UpdateContentCommand LifecycleCmd `json:"updateContentCommand"`
	PostCreateCommand    LifecycleCmd `json:"postCreateCommand"`
	PostStartCommand     LifecycleCmd `json:"postStartCommand"`

	Mounts          []string `json:"mounts,omitempty"`
	WorkspaceFolder string   `json:"workspaceFolder,omitempty"`

	// Not used yet (stream 3)
	RemoteUser    string `json:"remoteUser,omitempty"`
	ContainerUser string `json:"containerUser,omitempty"`

	Features          map[string]any `json:"features,omitempty"`
	RunArgs           []string       `json:"runArgs,omitempty"`
	InitializeCommand LifecycleCmd   `json:"initializeCommand"`
	PostAttachCommand LifecycleCmd   `json:"postAttachCommand"`
	DockerComposeFile any            `json:"dockerComposeFile,omitempty"` // string or []string

	Customizations struct {
		VSCode struct {
			Extensions []string       `json:"extensions,omitempty"`
			Settings   map[string]any `json:"settings,omitempty"`
		} `json:"vscode"`
	} `json:"customizations"`

	Name             string `json:"name,omitempty"`
	WaitFor          string `json:"waitFor,omitempty"`
	HostRequirements any    `json:"hostRequirements,omitempty"`
	ShutdownAction   string `json:"shutdownAction,omitempty"`
}

// LoadDevcontainer reads and JSON-decodes the devcontainer.json at path.
// Sets BuildPresent=true if a "build" key is present without fully parsing it.
func LoadDevcontainer(path string) (*DevcontainerConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is derived from user workdir
	if err != nil {
		return nil, fmt.Errorf("read devcontainer.json: %w", err)
	}

	// Detect "build" key presence before unmarshaling to the typed struct.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return nil, fmt.Errorf("parse devcontainer.json: %w", err)
	}
	_, hasBuild := rawMap["build"]

	var dc DevcontainerConfig
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("decode devcontainer.json: %w", err)
	}
	dc.BuildPresent = hasBuild

	return &dc, nil
}

// ExtractPorts returns the union of forwardPorts and appPort as "port:port" strings
// compatible with parsePortBindings.
func (dc *DevcontainerConfig) ExtractPorts() []string {
	seen := make(map[int]bool)
	var result []string
	for _, p := range dc.ForwardPorts {
		if !seen[p] {
			seen[p] = true
			result = append(result, fmt.Sprintf("%d:%d", p, p))
		}
	}
	for _, p := range dc.AppPort {
		if !seen[p] {
			seen[p] = true
			result = append(result, fmt.Sprintf("%d:%d", p, p))
		}
	}
	return result
}

// FilterMounts evaluates each devcontainer mount entry, strips dangerous mounts,
// and returns safe mounts plus warning strings for stripped entries.
// workdirMountPath is the target path of the sandbox workdir mount.
// homeDir is used for ${localEnv:HOME} expansion; callers derive it from layout.HomeDir.
func (dc *DevcontainerConfig) FilterMounts(workdirMountPath, homeDir string) (mounts []string, warnings []string) {

	for _, m := range dc.Mounts {
		// Expand ${localEnv:HOME}
		expanded := strings.ReplaceAll(m, "${localEnv:HOME}", homeDir)

		// Extract source from mount spec (handle both formats)
		src := extractMountSource(expanded)

		// Strip docker socket (complete sandbox escape)
		if src == "/var/run/docker.sock" || src == "//./pipe/docker_engine" {
			warnings = append(warnings, fmt.Sprintf("Warning: stripped devcontainer mount %q — docker socket mount is a sandbox escape", m))
			continue
		}

		// Strip agent credential dirs
		if isCredentialDir(src) {
			warnings = append(warnings, fmt.Sprintf("Warning: stripped devcontainer mount %q — agent credential directory conflicts with yoloAI secret injection", m))
			continue
		}

		// Strip mounts whose source path does not exist on the host
		if _, err := os.Stat(src); os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("Warning: stripped devcontainer mount %q — source path does not exist on host", m))
			continue
		}

		// Strip mounts whose target conflicts with workdir mount path
		target := extractMountTarget(expanded)
		if workdirMountPath != "" && target == workdirMountPath {
			warnings = append(warnings, fmt.Sprintf("Warning: stripped devcontainer mount %q — target conflicts with sandbox workdir mount", m))
			continue
		}

		// Normalize to host:container[:ro] so downstream parseConfigMount works
		// regardless of whether the devcontainer used Docker --mount syntax.
		normalized := src + ":" + target
		if extractMountReadOnly(expanded) {
			normalized += ":ro"
		}
		mounts = append(mounts, normalized)
	}
	return mounts, warnings
}

// isMountKeyValueFormat returns true if the mount string uses Docker --mount
// key=value syntax (e.g. "source=/path,target=/path,type=bind"), regardless of
// key order. Normal host:container paths start with "/" and contain no "=".
func isMountKeyValueFormat(m string) bool {
	for part := range strings.SplitSeq(m, ",") {
		k, _, ok := strings.Cut(part, "=")
		if ok {
			switch k {
			case "type", "source", "src", "target", "dst", "destination", "readonly", "ro":
				return true
			}
		}
	}
	return false
}

// extractMountSource extracts the source/host path from a mount string.
// Handles: key=value Docker --mount syntax  OR  /host:/container[:ro]
func extractMountSource(m string) string {
	if isMountKeyValueFormat(m) {
		for part := range strings.SplitSeq(m, ",") {
			if k, v, ok := strings.Cut(part, "="); ok && (k == "source" || k == "src") {
				return v
			}
		}
		return ""
	}
	// /host:/container[:ro] form
	parts := strings.SplitN(m, ":", 3)
	if len(parts) >= 2 {
		return parts[0]
	}
	return m
}

// extractMountTarget extracts the target/container path from a mount string.
func extractMountTarget(m string) string {
	if isMountKeyValueFormat(m) {
		for part := range strings.SplitSeq(m, ",") {
			if k, v, ok := strings.Cut(part, "="); ok && (k == "target" || k == "dst" || k == "destination") {
				return v
			}
		}
		return ""
	}
	// /host:/container[:ro] form
	parts := strings.SplitN(m, ":", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// extractMountReadOnly returns true if the mount string marks the mount read-only.
func extractMountReadOnly(m string) bool {
	if isMountKeyValueFormat(m) {
		for part := range strings.SplitSeq(m, ",") {
			part = strings.TrimSpace(part)
			if part == "readonly" || part == "ro" {
				return true
			}
			if k, v, ok := strings.Cut(part, "="); ok && (k == "readonly" || k == "ro") {
				return v == "true" || v == "1"
			}
		}
		return false
	}
	// /host:/container[:ro] form
	parts := strings.SplitN(m, ":", 3)
	return len(parts) == 3 && parts[2] == "ro"
}

// isCredentialDir returns true if the path is an agent credential directory.
func isCredentialDir(path string) bool {
	credentialDirs := []string{
		"/.claude",
		"/.gemini",
		"/.codex",
		"/.local/share/opencode",
	}
	for _, dir := range credentialDirs {
		if strings.Contains(path, dir) {
			return true
		}
	}
	return false
}

// MergedEnv merges remoteEnv and containerEnv. remoteEnv takes precedence on conflict.
func (dc *DevcontainerConfig) MergedEnv() map[string]string {
	result := make(map[string]string)
	maps.Copy(result, dc.ContainerEnv)
	maps.Copy(result, dc.RemoteEnv)
	return result
}

// dangerousRunArgCaps are Linux capabilities that a repo-supplied
// devcontainer.json must NOT be able to grant itself. The workdir is untrusted
// input and the archetype is auto-detected, so an attacker-controlled
// devcontainer.json requesting one of these (combined with the in-container
// passwordless sudo and Docker rootful's lack of user-namespace remapping)
// would be a host escape. A user who genuinely needs one of these adds it
// through yoloAI's own config/profile (a trusted, explicit channel), never via
// the repo. Names are normalized (CAP_ prefix stripped, upper-cased) before the
// check. This is a denylist of the well-known escape-enabling caps rather than
// an allowlist so that benign, non-escalating caps in a devcontainer still work.
var dangerousRunArgCaps = map[string]bool{
	"SYS_ADMIN":          true, // mount(2), cgroup release_agent, etc.
	"SYS_MODULE":         true, // load kernel modules
	"SYS_RAWIO":          true, // raw I/O / physical memory
	"SYS_PTRACE":         true, // ptrace across the (shared) init namespace
	"SYS_BOOT":           true,
	"SYS_TIME":           true,
	"DAC_READ_SEARCH":    true, // bypass file read perms (open_by_handle_at escape)
	"DAC_OVERRIDE":       true, // bypass file perms
	"MKNOD":              true, // create device nodes → raw disk access
	"NET_ADMIN":          true, // tamper with the egress firewall
	"BPF":                true,
	"PERFMON":            true,
	"CHECKPOINT_RESTORE": true,
	"SYSLOG":             true,
	"AUDIT_CONTROL":      true,
	"LINUX_IMMUTABLE":    true,
	"SYS_CHROOT":         true,
}

// normalizeCapName strips an optional CAP_ prefix and upper-cases, so
// "CAP_sys_admin" and "SYS_ADMIN" compare equal.
func normalizeCapName(c string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
}

// ParsedRunArgs parses --cpus, --memory, --cap-add from runArgs.
// Unknown flags are collected into unknownWarnings. Privileged-equivalent caps
// (dangerousRunArgCaps) are rejected with a warning rather than granted, since
// runArgs come from the untrusted, auto-detected workdir.
func (dc *DevcontainerConfig) ParsedRunArgs() (cpus string, memory string, capAdd []string, unknownWarnings []string) {
	args := dc.RunArgs
	addCap := func(c string) {
		if dangerousRunArgCaps[normalizeCapName(c)] {
			unknownWarnings = append(unknownWarnings, fmt.Sprintf("Warning: refusing dangerous capability %q from devcontainer.json runArgs (would enable a host escape; add it via yoloAI config if you truly need it)", c))
			return
		}
		capAdd = append(capAdd, c)
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--cpus" && i+1 < len(args):
			i++
			cpus = args[i]
		case strings.HasPrefix(arg, "--cpus="):
			cpus = strings.TrimPrefix(arg, "--cpus=")
		case arg == "--memory" && i+1 < len(args):
			i++
			memory = args[i]
		case strings.HasPrefix(arg, "--memory="):
			memory = strings.TrimPrefix(arg, "--memory=")
		case arg == "--cap-add" && i+1 < len(args):
			i++
			addCap(args[i])
		case strings.HasPrefix(arg, "--cap-add="):
			addCap(strings.TrimPrefix(arg, "--cap-add="))
		default:
			unknownWarnings = append(unknownWarnings, fmt.Sprintf("Warning: ignoring unknown runArg %q (not supported by yoloAI)", arg))
		}
	}
	return cpus, memory, capAdd, unknownWarnings
}

// PostStartCommandUsesCompose returns true if any string form of postStartCommand
// contains "docker compose" or "docker-compose".
func (dc *DevcontainerConfig) PostStartCommandUsesCompose() bool {
	return lifecycleCmdUsesCompose(dc.PostStartCommand)
}

func lifecycleCmdUsesCompose(cmd LifecycleCmd) bool {
	if cmd.IsZero() {
		return false
	}
	switch v := cmd.raw.(type) {
	case string:
		return strings.Contains(v, "docker compose") || strings.Contains(v, "docker-compose")
	case []string:
		for _, s := range v {
			if strings.Contains(s, "docker compose") || strings.Contains(s, "docker-compose") {
				return true
			}
		}
	case map[string]any:
		for _, val := range v {
			if s, ok := val.(string); ok {
				if strings.Contains(s, "docker compose") || strings.Contains(s, "docker-compose") {
					return true
				}
			}
		}
	}
	return false
}

// WarnIgnoredFields prints warnings for devcontainer.json fields that yoloAI ignores.
func (dc *DevcontainerConfig) WarnIgnoredFields(w io.Writer) {
	if len(dc.Features) > 0 {
		fmt.Fprintln(w, "Warning: devcontainer.json features: are not supported — use a profile Dockerfile to install equivalent packages") //nolint:errcheck // best-effort warning
	}
	if !dc.InitializeCommand.IsZero() {
		fmt.Fprintln(w, "Warning: devcontainer.json initializeCommand is ignored (runs on host before container creation — not supported)") //nolint:errcheck // best-effort warning
	}
	if !dc.PostAttachCommand.IsZero() {
		fmt.Fprintln(w, "Warning: devcontainer.json postAttachCommand is ignored (no equivalent sandbox lifecycle event)") //nolint:errcheck // best-effort warning
	}
	if dc.WaitFor != "" {
		fmt.Fprintln(w, "Warning: devcontainer.json waitFor is ignored (yoloAI always waits for all setup commands)") //nolint:errcheck // best-effort warning
	}
	if dc.HostRequirements != nil {
		fmt.Fprintln(w, "Warning: devcontainer.json hostRequirements is ignored (Codespaces sizing hints not applicable)") //nolint:errcheck // best-effort warning
	}
	if dc.ShutdownAction != "" {
		fmt.Fprintln(w, "Warning: devcontainer.json shutdownAction is ignored (yoloAI manages sandbox lifecycle)") //nolint:errcheck // best-effort warning
	}
	if dc.Name != "" {
		fmt.Fprintln(w, "Warning: devcontainer.json name is ignored (yoloAI uses the sandbox name)") //nolint:errcheck // best-effort warning
	}
}

// DockerComposeFilePresent returns true if dockerComposeFile is non-nil/non-empty.
func (dc *DevcontainerConfig) DockerComposeFilePresent() bool {
	if dc.DockerComposeFile == nil {
		return false
	}
	switch v := dc.DockerComposeFile.(type) {
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	}
	return true
}

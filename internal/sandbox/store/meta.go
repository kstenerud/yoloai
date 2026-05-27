// ABOUTME: Sandbox environment metadata (environment.json) load/save.
// ABOUTME: Tracks agent, model, directories, and creation-time settings.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// metaVersion is the current schema version for Meta. Bump when adding or
// changing fields that require migration from older sandboxes.
const metaVersion = 1

// Meta holds sandbox configuration captured at creation time.
type Meta struct {
	Version       int                 `json:"version"` // schema version; 0 = legacy (pre-versioning)
	YoloaiVersion string              `json:"yoloai_version"`
	Name          string              `json:"name"`
	CreatedAt     time.Time           `json:"created_at"`
	Backend       runtime.BackendName `json:"backend"` // typed string; serializes as "docker"/"tart"/etc.
	Profile       string              `json:"profile,omitempty"`
	ImageRef      string              `json:"image_ref,omitempty"`

	Agent agent.AgentName `json:"agent"`
	Model string          `json:"model,omitempty"`

	Workdir     WorkdirMeta `json:"workdir"`
	Directories []DirMeta   `json:"directories,omitempty"`

	HasPrompt          bool                   `json:"has_prompt"`
	NetworkMode        string                 `json:"network_mode,omitempty"`
	NetworkAllow       []string               `json:"network_allow,omitempty"`
	Ports              []string               `json:"ports,omitempty"`
	Resources          *config.ResourceLimits `json:"resources,omitempty"`
	Mounts             []string               `json:"mounts,omitempty"`
	CapAdd             []string               `json:"cap_add,omitempty"`
	Devices            []string               `json:"devices,omitempty"`
	Setup              []string               `json:"setup,omitempty"`
	AutoCommitInterval int                    `json:"auto_commit_interval,omitempty"`
	Debug              bool                   `json:"debug,omitempty"`
	UsernsMode         string                 `json:"userns_mode,omitempty"`     // "keep-id" for Podman rootless keep-id; "" otherwise
	Isolation          string                 `json:"isolation,omitempty"`       // isolation mode: container, container-enhanced, vm, vm-enhanced
	HostFilesystem     bool                   `json:"host_filesystem,omitempty"` // true when sandbox state lives on the host (seatbelt)
	VscodeTunnel       bool                   `json:"vscode_tunnel,omitempty"`   // true when VS Code Remote Tunnel is enabled
	Archetype          string                 `json:"archetype,omitempty"`       // resolved environment archetype (simple, compose, devcontainer, apple)
}

// WorkdirMeta stores the resolved workdir state at creation time.
type WorkdirMeta struct {
	HostPath     string  `json:"host_path"`
	MountPath    string  `json:"mount_path"`
	Mode         DirMode `json:"mode"` // typed; serializes as "copy"/"overlay"/"rw"/"ro"
	BaselineSHA  string  `json:"baseline_sha,omitempty"`
	InceptionSHA string  `json:"inception_sha,omitempty"`
}

// DirMeta stores resolved directory state at creation time.
// Used for both workdir and auxiliary directories.
type DirMeta struct {
	HostPath    string  `json:"host_path"`
	MountPath   string  `json:"mount_path"`
	Mode        DirMode `json:"mode"`
	BaselineSHA string  `json:"baseline_sha,omitempty"`
}

// migrate applies forward migrations to meta loaded from disk.
// Missing Version (old files) deserialises as 0 and is migrated to current.
// A version higher than the binary knows is a hard error — the user should not
// silently run an old binary against a sandbox created by a newer one.
func migrate(meta *Meta) error {
	if meta.Version > metaVersion {
		return fmt.Errorf("sandbox was created with a newer version of yoloai "+
			"(meta version %d, this binary knows %d); upgrade yoloai to use it",
			meta.Version, metaVersion)
	}
	if meta.Version < 1 {
		// v0 → v1: bootstrap HostFilesystem from the backend's descriptor
		// capability. The descriptor is the single source of truth; backends
		// declare their own HostFilesystem flag (see runtime.BackendCaps).
		//
		// If the named backend isn't registered on this platform we default
		// to false — a meta whose backend can't be instantiated here will
		// fail downstream anyway, and false is the conservative answer.
		if desc, ok := runtime.Descriptor(meta.Backend); ok {
			meta.HostFilesystem = desc.Capabilities.HostFilesystem
		}
		meta.Version = 1
	}
	return nil
}

// SaveMeta writes environment.json to the given directory path.
func SaveMeta(dir string, meta *Meta) error {
	meta.Version = metaVersion
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", EnvironmentFile, err)
	}

	path := filepath.Join(dir, EnvironmentFile)
	if err := fileutil.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", EnvironmentFile, err)
	}

	return nil
}

// ContainerUser returns the appropriate user string for docker exec
// operations in the given sandbox. Under container-enhanced (gVisor),
// docker exec resolves usernames from the OCI image manifest (the
// placeholder UID used at build time), not the container's live
// /etc/passwd (updated by the entrypoint's uid-remap step). Use the
// numeric host UID instead to match the remapped container user.
func ContainerUser(meta *Meta) string {
	if meta == nil {
		return "yoloai"
	}
	if meta.UsernsMode == "keep-id" {
		return ""
	}
	if meta.Isolation == "container-enhanced" {
		return fmt.Sprintf("%d", os.Getuid())
	}
	return "yoloai"
}

// LoadMeta reads environment.json from the given directory path.
func LoadMeta(dir string) (*Meta, error) {
	path := filepath.Join(dir, EnvironmentFile)

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir, not user input
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", EnvironmentFile, err)
	}

	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvironmentFile, err)
	}

	if err := migrate(&meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

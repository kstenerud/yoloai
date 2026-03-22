package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// metaVersion is the current schema version for Meta. Bump when adding or
// changing fields that require migration from older sandboxes.
const metaVersion = 1

// Meta holds sandbox configuration captured at creation time.
type Meta struct {
	Version       int       `json:"version"` // schema version; 0 = legacy (pre-versioning)
	YoloaiVersion string    `json:"yoloai_version"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	Backend       string    `json:"backend"` // "docker" or "tart"
	Profile       string    `json:"profile,omitempty"`
	ImageRef      string    `json:"image_ref,omitempty"`

	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`

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
}

// WorkdirMeta stores the resolved workdir state at creation time.
type WorkdirMeta struct {
	HostPath    string `json:"host_path"`
	MountPath   string `json:"mount_path"`
	Mode        string `json:"mode"`
	BaselineSHA string `json:"baseline_sha,omitempty"`
}

// DirMeta stores resolved directory state at creation time.
// Used for both workdir and auxiliary directories.
type DirMeta struct {
	HostPath    string `json:"host_path"`
	MountPath   string `json:"mount_path"`
	Mode        string `json:"mode"` // "copy", "rw", "ro"
	BaselineSHA string `json:"baseline_sha,omitempty"`
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
		// v0 → v1: bootstrap HostFilesystem from the backend name.
		// Seatbelt is the only backend where sandbox state lives on the host.
		meta.HostFilesystem = (meta.Backend == "seatbelt")
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

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/config"
)

// Meta holds sandbox configuration captured at creation time.
type Meta struct {
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

// SaveMeta writes environment.json to the given directory path.
func SaveMeta(dir string, meta *Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", EnvironmentFile, err)
	}

	path := filepath.Join(dir, EnvironmentFile)
	if err := os.WriteFile(path, data, 0600); err != nil {
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

	return &meta, nil
}

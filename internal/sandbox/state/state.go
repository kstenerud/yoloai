// ABOUTME: Shared value types for the sandbox-creation pipeline: DirSpec (a
// ABOUTME: directory mount spec) and State (resolved per-operation state). This
// ABOUTME: is a leaf package so create/, mounts/, and lifecycle/ can all depend
// ABOUTME: on it without importing the sandbox façade (avoids an import cycle).
package state

import (
	"io"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/archetype"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// DirSpec describes a directory to mount in the sandbox.
// Use this instead of raw ":copy"/":rw" string syntax.
type DirSpec struct {
	Path               string        // absolute host path; required
	Mode               store.DirMode // mount mode; required for workdir
	MountPath          string        // custom container mount path; empty = mirror host path
	AllowDirty         bool          // proceed even if this directory has uncommitted git changes
	AllowDangerousPath bool          // mount even if this is a dangerous path (e.g. $HOME); the :force suffix
}

// ResolvedMountPath returns the container mount path. If MountPath is
// set, it is returned; otherwise Path (mirroring the host path).
func (d *DirSpec) ResolvedMountPath() string {
	if d.MountPath != "" {
		return d.MountPath
	}
	return d.Path
}

// State holds resolved state computed during preparation.
type State struct {
	Name              string
	SandboxDir        string
	Workdir           *DirSpec
	WorkCopyDir       string
	AuxDirs           []*DirSpec
	Agent             *agent.Definition
	Model             string
	Profile           string
	ImageRef          string
	Env               map[string]string // merged env (base + profile chain)
	CredOverrides     map[string]string // sudo-recovered credential defaults (keys absent from os.Environ)
	HasPrompt         bool
	PromptSourcePath  string // overrides default prompt.txt path for /yoloai/prompt.txt mount
	NetworkMode       string
	NetworkAllow      []string
	Ports             []string
	ConfigMounts      []string // extra bind mounts from config/profile (host:container[:ro])
	TmuxConf          string
	Resources         *config.ResourceLimits
	CapAdd            []string              // Linux capabilities from config/profile
	Devices           []string              // host devices from config/profile
	Setup             []string              // setup commands from config/profile
	Isolation         runtime.IsolationMode // isolation mode from config/profile
	IsolationExplicit bool                  // true when isolation was set via --isolation flag
	VscodeTunnel      bool                  // true when VS Code Remote Tunnel is enabled
	Environment       *store.Environment
	ConfigJSON        []byte
	// Archetype fields
	Archetype                 archetype.Archetype
	DockerdRequired           bool
	Devcontainer              *archetype.DevcontainerConfig
	DevcontainerMounts        []string
	DevcontainerMountWarnings []string
	WorkdirMode               string        // resolved workdir mode ("copy", "overlay", "rw")
	Layout                    config.Layout // Q-W.3: DataDir-rooted Layout propagated from the Engine
	HomeDir                   string        // Q-W.6: host home dir (layout.HomeDir); used for ~ expansion
	Output                    io.Writer     // create-pipeline progress writer (CreateOptions.Output); F8
}

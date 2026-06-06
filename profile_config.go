// ABOUTME: Public read-model mirror of the internal merged profile config,
// ABOUTME: returned by ProfileAdmin.Info() so embedders can name every field.

package yoloai

import "github.com/kstenerud/yoloai/internal/config"

// ResolvedProfileConfig is the fully merged configuration of a profile:
// baked-in defaults overlaid with the profile's own settings (and, in
// future, its inheritance chain). It is an output-only read model produced
// by ProfileAdmin.Info(); no public API consumes it as input.
type ResolvedProfileConfig struct {
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
	OS    string `json:"os,omitempty"`
	// Backend is the optional backend constraint a profile pins (config key
	// "backend"); empty means unconstrained. Distinct from ContainerBackend.
	Backend string `json:"backend,omitempty"`
	// ContainerBackend names the runtime engine — "docker", "podman", or
	// "containerd" (config key "container_backend").
	ContainerBackend   string             `json:"container_backend,omitempty"`
	TartImage          string             `json:"tart_image,omitempty"`
	Env                map[string]string  `json:"env,omitempty"`
	Ports              []string           `json:"ports,omitempty"`
	Workdir            *ProfileWorkdir    `json:"workdir,omitempty"`
	Directories        []ProfileAuxDir    `json:"directories,omitempty"`
	Resources          *ProfileResources  `json:"resources,omitempty"`
	Network            *ProfileNetwork    `json:"network,omitempty"`
	Mounts             []string           `json:"mounts,omitempty"`
	AgentArgs          map[string]string  `json:"agent_args,omitempty"`
	AgentFiles         *ProfileAgentFiles `json:"agent_files,omitempty"`
	CapAdd             []string           `json:"cap_add,omitempty"`
	Devices            []string           `json:"devices,omitempty"`
	Setup              []string           `json:"setup,omitempty"`
	AutoCommitInterval int                `json:"auto_commit_interval,omitempty"`
	Isolation          string             `json:"isolation,omitempty"`
}

// ProfileWorkdir is the resolved primary working directory of a profile.
type ProfileWorkdir struct {
	Path  string `json:"path"`
	Mode  string `json:"mode,omitempty"`  // "copy" or "rw"
	Mount string `json:"mount,omitempty"` // optional custom mount point
}

// ProfileAuxDir is a resolved auxiliary directory of a profile.
type ProfileAuxDir struct {
	Path  string `json:"path"`
	Mode  string `json:"mode,omitempty"`  // "rw", "copy", or "" (read-only)
	Mount string `json:"mount,omitempty"` // optional custom mount point
}

// ProfileResources holds a profile's container resource limits.
type ProfileResources struct {
	CPULimit    string `json:"cpus,omitempty"`
	MemoryLimit string `json:"memory,omitempty"`
}

// ProfileNetwork holds a profile's network isolation settings.
type ProfileNetwork struct {
	Isolated bool     `json:"isolated,omitempty"`
	Allow    []string `json:"allow,omitempty"`
}

// ProfileAgentFiles holds a profile's agent_files setting. Exactly one form
// is populated: BaseDir for the string (base directory) form, or Files for
// the explicit-list form.
type ProfileAgentFiles struct {
	BaseDir string   `json:"base_dir,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// profileConfigFromMerged converts the internal merged config into the public
// read model. It is nil-safe and one-directional; nested pointers are
// allocated only when their internal counterpart is non-nil.
func resolvedProfileConfigFromMerged(m *config.MergedConfig) *ResolvedProfileConfig {
	if m == nil {
		return nil
	}
	pc := &ResolvedProfileConfig{
		Agent:              m.Agent,
		Model:              m.Model,
		OS:                 m.OS,
		Backend:            m.Backend,
		ContainerBackend:   m.ContainerBackend,
		TartImage:          m.TartImage,
		Env:                m.Env,
		Ports:              m.Ports,
		Mounts:             m.Mounts,
		AgentArgs:          m.AgentArgs,
		CapAdd:             m.CapAdd,
		Devices:            m.Devices,
		Setup:              m.Setup,
		AutoCommitInterval: m.AutoCommitInterval,
		Isolation:          m.Isolation,
	}
	if m.Workdir != nil {
		pc.Workdir = &ProfileWorkdir{
			Path:  m.Workdir.Path,
			Mode:  m.Workdir.Mode,
			Mount: m.Workdir.Mount,
		}
	}
	if len(m.Directories) > 0 {
		pc.Directories = make([]ProfileAuxDir, len(m.Directories))
		for i, d := range m.Directories {
			pc.Directories[i] = ProfileAuxDir{Path: d.Path, Mode: d.Mode, Mount: d.Mount}
		}
	}
	if m.Resources != nil {
		pc.Resources = &ProfileResources{
			CPULimit:    m.Resources.CPUs,
			MemoryLimit: m.Resources.Memory,
		}
	}
	if m.Network != nil {
		pc.Network = &ProfileNetwork{
			Isolated: m.Network.Isolated,
			Allow:    m.Network.Allow,
		}
	}
	if m.AgentFiles != nil {
		pc.AgentFiles = &ProfileAgentFiles{
			BaseDir: m.AgentFiles.BaseDir,
			Files:   m.AgentFiles.Files,
		}
	}
	return pc
}

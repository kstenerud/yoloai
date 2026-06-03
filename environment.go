// ABOUTME: Public read-model for a sandbox's creation-time environment: a
// ABOUTME: curated view of the sandbox's identity, posture, and resolved config.

package yoloai

import (
	"time"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Environment is the curated read-model of a sandbox captured at creation time,
// carried on Info.Environment. It exposes the sandbox's identity and security
// posture, its as-built workdir/aux-dir provenance, and an echo of the resolved
// configuration an embedder would render or decide from. Internal mechanism
// fields (on-disk schema version, image ref, prompt/debug/userns/vscode flags)
// are deliberately omitted — they describe *how* containment is achieved, not
// the sandbox a consumer reasons about. Typed fields reuse the public aliases
// (BackendType, AgentType, IsolationMode, DirMode, NetworkMode).
type Environment struct {
	// Identity & posture.
	Name           string        `json:"name"`
	CreatedAt      time.Time     `json:"created_at"`
	Backend        BackendType   `json:"backend"`
	Profile        string        `json:"profile,omitempty"`
	Agent          AgentType     `json:"agent"`
	Model          string        `json:"model,omitempty"`
	Isolation      IsolationMode `json:"isolation,omitempty"`
	HostFilesystem bool          `json:"host_filesystem,omitempty"`

	// As-built provenance.
	Workdir     WorkdirInfo `json:"workdir"`
	Directories []DirInfo   `json:"directories,omitempty"`

	// Resolved-config echo.
	NetworkMode        NetworkMode       `json:"network_mode,omitempty"`
	NetworkAllow       []string          `json:"network_allow,omitempty"`
	Ports              []string          `json:"ports,omitempty"`
	Resources          *ProfileResources `json:"resources,omitempty"`
	Mounts             []string          `json:"mounts,omitempty"`
	Setup              []string          `json:"setup,omitempty"`
	CapAdd             []string          `json:"cap_add,omitempty"`
	Devices            []string          `json:"devices,omitempty"`
	AutoCommitInterval int               `json:"auto_commit_interval,omitempty"`
}

// HasOverlayDirs reports whether the workdir or any auxiliary directory uses
// :overlay mode. Overlay sandboxes keep their git state inside the container,
// so callers route diff/apply through container exec rather than the host work
// copy.
func (e *Environment) HasOverlayDirs() bool {
	if e.Workdir.Mode == DirModeOverlay {
		return true
	}
	for _, d := range e.Directories {
		if d.Mode == DirModeOverlay {
			return true
		}
	}
	return false
}

// WorkdirInfo is the resolved workdir state captured at creation time. Mirror
// of the internal store.WorkdirEnvironment.
type WorkdirInfo struct {
	HostPath    string  `json:"host_path"`
	MountPath   string  `json:"mount_path"`
	Mode        DirMode `json:"mode"`
	BaselineSHA string  `json:"baseline_sha,omitempty"`
}

// DirInfo is the resolved state of an auxiliary directory captured at creation
// time. Mirror of the internal store.DirEnvironment.
type DirInfo struct {
	HostPath    string  `json:"host_path"`
	MountPath   string  `json:"mount_path"`
	Mode        DirMode `json:"mode"`
	BaselineSHA string  `json:"baseline_sha,omitempty"`
}

// environmentFromStore builds the public read-model from the internal metadata.
// Nil-safe (returns nil for nil input); nested pointers are allocated only when
// the source pointer is non-nil so omitempty JSON output is preserved. Internal
// mechanism fields on store.Environment are intentionally not copied across.
func environmentFromStore(m *store.Environment) *Environment {
	if m == nil {
		return nil
	}
	env := &Environment{
		Name:               m.Name,
		CreatedAt:          m.CreatedAt,
		Backend:            m.Backend,
		Profile:            m.Profile,
		Agent:              m.Agent,
		Model:              m.Model,
		Isolation:          m.Isolation,
		HostFilesystem:     m.HostFilesystem,
		Workdir:            workdirInfoFromStore(m.Workdir),
		NetworkMode:        NetworkMode(m.NetworkMode),
		NetworkAllow:       m.NetworkAllow,
		Ports:              m.Ports,
		Mounts:             m.Mounts,
		Setup:              m.Setup,
		CapAdd:             m.CapAdd,
		Devices:            m.Devices,
		AutoCommitInterval: m.AutoCommitInterval,
	}
	if len(m.Directories) > 0 {
		env.Directories = make([]DirInfo, len(m.Directories))
		for i, d := range m.Directories {
			env.Directories[i] = DirInfo{
				HostPath:    d.HostPath,
				MountPath:   d.MountPath,
				Mode:        d.Mode,
				BaselineSHA: d.BaselineSHA,
			}
		}
	}
	if m.Resources != nil {
		env.Resources = &ProfileResources{
			CPULimit:    m.Resources.CPUs,
			MemoryLimit: m.Resources.Memory,
		}
	}
	return env
}

func workdirInfoFromStore(w store.WorkdirEnvironment) WorkdirInfo {
	return WorkdirInfo{
		HostPath:    w.HostPath,
		MountPath:   w.MountPath,
		Mode:        w.Mode,
		BaselineSHA: w.BaselineSHA,
	}
}

// infoFromStatus converts the internal read-model (sandbox.Info, an alias of
// status.Info) into the public Info at the library boundary. Nil-safe.
func infoFromStatus(si *sandbox.Info) *Info {
	if si == nil {
		return nil
	}
	return &Info{
		Environment:    environmentFromStore(si.Environment),
		Status:         si.Status,
		AgentStatus:    si.AgentStatus,
		HasChanges:     si.HasChanges,
		DiskUsageBytes: si.DiskUsageBytes,
	}
}

// infosFromStatus maps a slice of internal read-models to public Info values.
func infosFromStatus(sis []*sandbox.Info) []*Info {
	out := make([]*Info, len(sis))
	for i, si := range sis {
		out[i] = infoFromStatus(si)
	}
	return out
}

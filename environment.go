// ABOUTME: Public read-model for a sandbox's creation-time environment: a
// ABOUTME: curated view of the sandbox's identity, posture, and resolved config.

package yoloai

import (
	"time"

	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/store"
)

// Environment is the curated read-model of a sandbox captured at creation time,
// carried on SandboxInfo.Environment. It exposes the sandbox's identity and security
// posture, its as-built workdir/aux-dir provenance, and an echo of the resolved
// configuration an embedder would render or decide from. Internal mechanism
// fields (on-disk schema version, image ref, prompt/debug/userns/vscode flags)
// are deliberately omitted — they describe *how* containment is achieved, not
// the sandbox a consumer reasons about. The inside-process config (agent type,
// model) is likewise not here — it is not a substrate fact; it rides on
// SandboxInfo (the aggregated read-model) and Sandbox.Agent() instead (Q104).
// Typed fields reuse the public aliases
// (BackendType, IsolationMode, DirMode, NetworkMode).
type Environment struct {
	// Identity & posture.
	Name        string      `json:"name"`
	CreatedAt   time.Time   `json:"created_at"`
	BackendType BackendType `json:"backend"`
	Profile     string      `json:"profile,omitempty"`
	// Headless is the effective launch mode: true when the agent runs in its own
	// headless mode (prompt baked in, pane-death = done), false for the
	// interactive TTY flow. `yoloai run` requests headless but it may be
	// downgraded when the agent's headless mode is unsafe without an API key (D101).
	Headless       bool          `json:"headless,omitempty"`
	Isolation      IsolationMode `json:"isolation,omitempty"`
	HostFilesystem bool          `json:"host_filesystem,omitempty"`

	// As-built provenance. Dirs is the ordered directory list; element 0 is the
	// workdir (use the Workdir()/AuxDirs()/TrackedDirs() accessors).
	Dirs []DirInfo `json:"dirs,omitempty"`

	// Resolved-config echo.
	Ports              []string          `json:"ports,omitempty"`
	Resources          *ProfileResources `json:"resources,omitempty"`
	Mounts             []string          `json:"mounts,omitempty"`
	Setup              []string          `json:"setup,omitempty"`
	CapAdd             []string          `json:"cap_add,omitempty"`
	Devices            []string          `json:"devices,omitempty"`
	AutoCommitInterval int               `json:"auto_commit_interval,omitempty"`
}

// Workdir returns the primary directory — Dirs[0], the agent's cwd. Returns the
// zero DirInfo for a malformed environment with no directories.
func (e *Environment) Workdir() DirInfo {
	if len(e.Dirs) == 0 {
		return DirInfo{}
	}
	return e.Dirs[0]
}

// AuxDirs returns the non-workdir directories (Dirs[1:]).
func (e *Environment) AuxDirs() []DirInfo {
	if len(e.Dirs) <= 1 {
		return nil
	}
	return e.Dirs[1:]
}

// TrackedDirs returns the directories diff/apply operates on — those in
// :copy or :overlay mode (the workdir is included when tracked).
func (e *Environment) TrackedDirs() []DirInfo {
	var out []DirInfo
	for _, d := range e.Dirs {
		if d.Mode == DirModeCopy || d.Mode == DirModeOverlay {
			out = append(out, d)
		}
	}
	return out
}

// HasOverlayDirs reports whether any directory uses :overlay mode. Overlay
// sandboxes keep their git state inside the container, so callers route
// diff/apply through container exec rather than the host work copy.
func (e *Environment) HasOverlayDirs() bool {
	for _, d := range e.Dirs {
		if d.Mode == DirModeOverlay {
			return true
		}
	}
	return false
}

// DirInfo is the resolved state of one directory captured at creation time
// (workdir and auxiliary alike — the workdir is Dirs[0]). Mirror of the
// internal store.DirEnvironment.
type DirInfo struct {
	HostPath     string  `json:"host_path"`
	MountPath    string  `json:"mount_path"`
	Mode         DirMode `json:"mode"`
	BaselineSHA  string  `json:"baseline_sha,omitempty"`
	InceptionSHA string  `json:"inception_sha,omitempty"`
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
		BackendType:        m.BackendType,
		Profile:            m.Profile,
		Headless:           m.Headless,
		Isolation:          m.Isolation,
		HostFilesystem:     m.HostFilesystem,
		Ports:              m.Ports,
		Mounts:             m.Mounts,
		Setup:              m.Setup,
		CapAdd:             m.CapAdd,
		Devices:            m.Devices,
		AutoCommitInterval: m.AutoCommitInterval,
	}
	if len(m.Dirs) > 0 {
		env.Dirs = make([]DirInfo, len(m.Dirs))
		for i, d := range m.Dirs {
			env.Dirs[i] = dirInfoFromStore(d)
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

func dirInfoFromStore(d store.DirEnvironment) DirInfo {
	return DirInfo{
		HostPath:     d.HostPath,
		MountPath:    d.MountPath,
		Mode:         d.Mode,
		BaselineSHA:  d.BaselineSHA,
		InceptionSHA: d.InceptionSHA,
	}
}

// sandboxInfoFromStatus converts the internal read-model (orchestrator.Info, an alias
// of status.Info) into the public SandboxInfo at the library boundary. Nil-safe.
func sandboxInfoFromStatus(si *orchestrator.Info) *SandboxInfo {
	if si == nil {
		return nil
	}
	return &SandboxInfo{
		Environment:    environmentFromStore(si.Environment),
		AgentType:      AgentType(si.AgentType),
		Model:          si.Model,
		NetworkMode:    NetworkMode(si.NetworkMode),
		NetworkAllow:   si.NetworkAllow,
		Status:         si.Status,
		AgentStatus:    si.AgentStatus,
		Changes:        ChangeState(si.HasChanges),
		DiskUsageBytes: si.DiskUsageBytes,
	}
}

// sandboxInfosFromStatus maps a slice of internal read-models to public
// SandboxInfo values.
func sandboxInfosFromStatus(sis []*orchestrator.Info) []*SandboxInfo {
	out := make([]*SandboxInfo, len(sis))
	for i, si := range sis {
		out[i] = sandboxInfoFromStatus(si)
	}
	return out
}

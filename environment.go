// ABOUTME: Public read-model for a sandbox's creation-time environment — a
// ABOUTME: hand-written mirror of internal/sandbox/store.Meta that decouples the
// ABOUTME: public API from the on-disk environment.json schema.

package yoloai

import (
	"time"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Environment is the configuration captured for a sandbox at creation time,
// carried on Info.Meta. It mirrors the internal store.Meta (the on-disk
// environment.json schema) field-for-field so embedders can read a sandbox's
// settings without importing internal packages; the JSON tags match the
// on-disk schema exactly, so serialized output is byte-stable. Typed fields
// reuse the public aliases (BackendName, AgentName, IsolationMode, DirMode).
type Environment struct {
	Version       int         `json:"version"`
	YoloaiVersion string      `json:"yoloai_version"`
	Name          string      `json:"name"`
	CreatedAt     time.Time   `json:"created_at"`
	Backend       BackendName `json:"backend"`
	Profile       string      `json:"profile,omitempty"`
	ImageRef      string      `json:"image_ref,omitempty"`

	Agent AgentName `json:"agent"`
	Model string    `json:"model,omitempty"`

	Workdir     WorkdirInfo `json:"workdir"`
	Directories []DirInfo   `json:"directories,omitempty"`

	HasPrompt          bool              `json:"has_prompt"`
	NetworkMode        string            `json:"network_mode,omitempty"`
	NetworkAllow       []string          `json:"network_allow,omitempty"`
	Ports              []string          `json:"ports,omitempty"`
	Resources          *ProfileResources `json:"resources,omitempty"`
	Mounts             []string          `json:"mounts,omitempty"`
	CapAdd             []string          `json:"cap_add,omitempty"`
	Devices            []string          `json:"devices,omitempty"`
	Setup              []string          `json:"setup,omitempty"`
	AutoCommitInterval int               `json:"auto_commit_interval,omitempty"`
	Debug              bool              `json:"debug,omitempty"`
	UsernsMode         string            `json:"userns_mode,omitempty"`
	Isolation          IsolationMode     `json:"isolation,omitempty"`
	HostFilesystem     bool              `json:"host_filesystem,omitempty"`
	VscodeTunnel       bool              `json:"vscode_tunnel,omitempty"`
	Archetype          string            `json:"archetype,omitempty"`
}

// WorkdirInfo is the resolved workdir state captured at creation time. Mirror
// of the internal store.WorkdirMeta.
type WorkdirInfo struct {
	HostPath     string  `json:"host_path"`
	MountPath    string  `json:"mount_path"`
	Mode         DirMode `json:"mode"`
	BaselineSHA  string  `json:"baseline_sha,omitempty"`
	InceptionSHA string  `json:"inception_sha,omitempty"`
}

// DirInfo is the resolved state of an auxiliary directory captured at creation
// time. Mirror of the internal store.DirMeta.
type DirInfo struct {
	HostPath    string  `json:"host_path"`
	MountPath   string  `json:"mount_path"`
	Mode        DirMode `json:"mode"`
	BaselineSHA string  `json:"baseline_sha,omitempty"`
}

// environmentFromMeta builds the public read-model from the internal metadata.
// Nil-safe (returns nil for nil input); nested pointers are allocated only when
// the source pointer is non-nil so omitempty JSON output is preserved.
func environmentFromMeta(m *store.Meta) *Environment {
	if m == nil {
		return nil
	}
	env := &Environment{
		Version:            m.Version,
		YoloaiVersion:      m.YoloaiVersion,
		Name:               m.Name,
		CreatedAt:          m.CreatedAt,
		Backend:            m.Backend,
		Profile:            m.Profile,
		ImageRef:           m.ImageRef,
		Agent:              m.Agent,
		Model:              m.Model,
		Workdir:            workdirInfoFromMeta(m.Workdir),
		HasPrompt:          m.HasPrompt,
		NetworkMode:        m.NetworkMode,
		NetworkAllow:       m.NetworkAllow,
		Ports:              m.Ports,
		Mounts:             m.Mounts,
		CapAdd:             m.CapAdd,
		Devices:            m.Devices,
		Setup:              m.Setup,
		AutoCommitInterval: m.AutoCommitInterval,
		Debug:              m.Debug,
		UsernsMode:         m.UsernsMode,
		Isolation:          m.Isolation,
		HostFilesystem:     m.HostFilesystem,
		VscodeTunnel:       m.VscodeTunnel,
		Archetype:          m.Archetype,
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

func workdirInfoFromMeta(w store.WorkdirMeta) WorkdirInfo {
	return WorkdirInfo{
		HostPath:     w.HostPath,
		MountPath:    w.MountPath,
		Mode:         w.Mode,
		BaselineSHA:  w.BaselineSHA,
		InceptionSHA: w.InceptionSHA,
	}
}

// infoFromStatus converts the internal read-model (sandbox.Info, an alias of
// status.Info) into the public Info at the library boundary. Nil-safe.
func infoFromStatus(si *sandbox.Info) *Info {
	if si == nil {
		return nil
	}
	return &Info{
		Meta:           environmentFromMeta(si.Meta),
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

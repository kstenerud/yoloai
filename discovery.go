// ABOUTME: Public discovery surface — static catalogs of the agents and backends
// ABOUTME: yoloai ships, with an opt-in per-host availability probe for backends.

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// AgentInfo is the public, serializable view of a shipped agent definition. It
// exposes only the user-facing fields; the agent's internal launch machinery
// (commands, seed files, idle handling, settings patches) stays internal.
type AgentInfo struct {
	Name          string
	Description   string
	PromptMode    string // "interactive" or "headless"
	APIKeyEnvVars []string
	StateDir      string
	ModelFlag     string
	ModelAliases  map[string]string
}

// BackendInfo is the public, serializable view of a registered runtime backend.
// The descriptor fields (Name…InstallHint) are static metadata, always
// populated. Available/Note are populated only when the query requested a probe
// (see BackendQuery.ProbeAvailability); otherwise Available is false and Note is
// empty regardless of whether the backend would actually run.
type BackendInfo struct {
	Name        BackendName
	Description string
	Platforms   []string // host GOOS values this backend runs on ("linux", "darwin", …)
	Requires    string   // human-readable prerequisites
	InstallHint string   // install URL or command; "" when nothing to install

	Available bool   // set only when probed; whether the backend is usable on this host now
	Note      string // probe failure reason when Available is false; "" otherwise
}

// AgentQuery filters the agent catalog. It is currently empty (every shipped
// agent is returned); it exists as a stable seam so filters can be added without
// changing the Agents signature.
type AgentQuery struct{}

// BackendQuery filters and shapes the backend catalog.
type BackendQuery struct {
	// ProbeAvailability, when true, constructs and immediately closes each
	// backend to determine whether it is usable on this host, populating
	// BackendInfo.Available/Note. This is slower and needs a context. When
	// false, only static descriptor metadata is returned.
	ProbeAvailability bool
}

// Agents returns the static catalog of every shipped agent, in stable
// (sorted-by-name) order. No host state is consulted; the query has no effect
// today beyond reserving the filtering seam.
func (s *SystemClient) Agents(_ AgentQuery) []AgentInfo {
	names := agent.AllAgentNames()
	out := make([]AgentInfo, 0, len(names))
	for _, name := range names {
		out = append(out, agentInfoFromDefinition(agent.GetAgent(name)))
	}
	return out
}

// Backends returns the catalog of every registered backend in registration
// order. With BackendQuery.ProbeAvailability set, each entry's Available/Note
// reflects whether the backend can run on this host now; otherwise only static
// descriptor metadata is filled in.
func (s *SystemClient) Backends(ctx context.Context, q BackendQuery) []BackendInfo {
	descs := runtime.Descriptors()
	out := make([]BackendInfo, 0, len(descs))
	for _, desc := range descs {
		info := backendInfoFromDescriptor(desc)
		if q.ProbeAvailability {
			rt, err := newRuntime(ctx, desc.Name, s.layout)
			if err != nil {
				info.Note = err.Error()
			} else {
				info.Available = true
				_ = rt.Close() //nolint:errcheck // best-effort close after a probe
			}
		}
		out = append(out, info)
	}
	return out
}

func agentInfoFromDefinition(def *agent.Definition) AgentInfo {
	return AgentInfo{
		Name:          def.Name,
		Description:   def.Description,
		PromptMode:    string(def.PromptMode),
		APIKeyEnvVars: def.APIKeyEnvVars,
		StateDir:      def.StateDir,
		ModelFlag:     def.ModelFlag,
		ModelAliases:  def.ModelAliases,
	}
}

func backendInfoFromDescriptor(desc runtime.BackendDescriptor) BackendInfo {
	return BackendInfo{
		Name:        desc.Name,
		Description: desc.Description,
		Platforms:   desc.Platforms,
		Requires:    desc.Requires,
		InstallHint: desc.InstallHint,
	}
}

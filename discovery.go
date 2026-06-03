// ABOUTME: Public discovery surface — static catalogs of the agents, backends,
// ABOUTME: and archetypes yoloai ships, plus an opt-in backend availability probe.

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/archetype"
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
	Name          BackendType
	Description   string
	Platforms     []string // host GOOS values this backend runs on ("linux", "darwin", …)
	Architectures []string // host GOARCH values this backend supports ("amd64", "arm64"); nil/empty = any arch
	// IsolationTargetOnly is true when the backend is reached only via isolation
	// routing (e.g. --isolation vm), never picked directly as a user default. A
	// setup wizard or default picker should skip these.
	IsolationTargetOnly bool
	Requires            string // human-readable prerequisites
	InstallHint         string // install URL or command; "" when nothing to install
	// HostFromContainer is the hostname inside the sandbox that resolves to the
	// host's network stack ("host.docker.internal" for docker/podman); "" for
	// backends without a special hostname.
	HostFromContainer string

	Available bool   // set only when probed; whether the backend is usable on this host now
	Note      string // probe failure reason when Available is false; "" otherwise
}

// AgentQuery filters the agent catalog.
type AgentQuery struct {
	// RealOnly excludes the pseudo-agents (test, shell, idle) that exist for
	// internal and testing purposes, returning only user-selectable agents.
	RealOnly bool
}

// BackendQuery filters and shapes the backend catalog.
type BackendQuery struct {
	// ProbeAvailability, when true, constructs and immediately closes each
	// backend to determine whether it is usable on this host, populating
	// BackendInfo.Available/Note. This is slower and needs a context. When
	// false, only static descriptor metadata is returned.
	ProbeAvailability bool
}

// Agents returns the static catalog of shipped agents, in stable
// (sorted-by-name) order. No host state is consulted. With
// AgentQuery.RealOnly set, the internal/testing pseudo-agents (test, shell,
// idle) are excluded.
func (s *System) Agents(q AgentQuery) []AgentInfo {
	names := agent.AllAgentTypes()
	if q.RealOnly {
		names = agent.RealAgents()
	}
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
func (s *System) Backends(ctx context.Context, q BackendQuery) []BackendInfo {
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

// CheckBackend probes a single backend for availability by constructing a
// runtime and closing it. Returns whether the backend is reachable and a short
// note explaining the failure when it is not. This is the single-backend
// counterpart to Backends(ctx, BackendQuery{ProbeAvailability: true}); both use
// the identical construct-and-close probe.
func (s *System) CheckBackend(ctx context.Context, name BackendType) (available bool, note string) {
	rt, err := newRuntime(ctx, name, s.layout)
	if err != nil {
		return false, err.Error()
	}
	_ = rt.Close() //nolint:errcheck // best-effort close after a probe
	return true, ""
}

// Archetypes returns the sorted list of valid environment-archetype names
// yoloai ships (used to auto-shape a sandbox's setup). Static metadata; no host
// state is consulted.
func (s *System) Archetypes() []string {
	return archetype.ValidArchetypes()
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
		Name:                desc.Name,
		Description:         desc.Description,
		Platforms:           desc.Platforms,
		Architectures:       desc.Architectures,
		IsolationTargetOnly: desc.IsolationTargetOnly,
		Requires:            desc.Requires,
		InstallHint:         desc.InstallHint,
		HostFromContainer:   desc.HostFromContainer,
	}
}

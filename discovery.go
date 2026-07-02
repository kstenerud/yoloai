// ABOUTME: Public discovery surface — static catalogs of the agents, backends,
// ABOUTME: and archetypes yoloai ships, plus an opt-in backend availability probe.

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/orchestrator/archetype"
	"github.com/kstenerud/yoloai/runtime"
)

// AgentInfo is the public, serializable view of a shipped agent definition. It
// exposes only the user-facing fields; the agent's internal launch machinery
// (commands, seed files, idle handling, settings patches) stays internal.
type AgentInfo struct {
	Type        AgentType
	Description string
	// PromptMode is the agent's default prompt-delivery mode: "interactive"
	// (typed into the session) or "headless" (passed as a CLI arg).
	PromptMode string
	// SupportsHeadless reports whether the agent has a headless / one-shot launch
	// form (a `-p`-style non-interactive run) — what an embedder checks to decide
	// whether it can drive the agent without an interactive session.
	SupportsHeadless bool
	// SupportsResume reports whether the agent has a native conversation-resume
	// form (e.g. Claude's `--continue`) — what a caller checks to decide whether a
	// restart can pick up the prior session rather than starting fresh.
	SupportsResume bool
	// IdleHook reports whether the agent emits an authoritative turn hook
	// (tier-2 idle detection); false means idle is detected heuristically only.
	IdleHook      bool
	APIKeyEnvVars []string
	StateDir      string
	ModelFlag     string
	ModelAliases  map[string]string
	// NetworkFloor is the set of domains the agent itself requires (the
	// agent-requirement floor that's always allowed under isolated networking).
	NetworkFloor []string
}

// BackendInfo is the public, serializable view of a registered runtime backend.
// The descriptor fields (Type…InstallHint) are static metadata, always
// populated. Available/Note are populated only when the query requested a probe
// (see BackendQuery.ProbeAvailability); otherwise Available is false and Note is
// empty regardless of whether the backend would actually run.
type BackendInfo struct {
	Type          BackendType
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

// AgentTypes returns the static catalog of agent types yoloai ships, in stable
// (sorted-by-name) order. These are descriptions to choose from, not runnable
// handles — to drive an agent, name its type when creating a sandbox and use
// Sandbox.Agent(). No host state is consulted. With AgentQuery.RealOnly set,
// the internal/testing pseudo-agents (test, shell, idle) are excluded.
func (s *System) AgentTypes(q AgentQuery) []AgentInfo {
	return AgentTypes(q)
}

// AgentTypes returns the catalog of shipped agent types. Static metadata with
// no host state, so it needs no System handle — callers that only want the
// catalog (e.g. CLI help text) can use it without constructing anything fallible.
func AgentTypes(q AgentQuery) []AgentInfo {
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

// BackendTypes returns the catalog of every registered backend type, in
// registration order. These are descriptions to choose from, not runnable
// handles — select one by setting ClientCreateOptions.BackendType. With
// BackendQuery.ProbeAvailability set, each entry's Available/Note reflects
// whether the backend can run on this host now; otherwise only static
// descriptor metadata is filled in.
func (s *System) BackendTypes(ctx context.Context, q BackendQuery) []BackendInfo {
	out := BackendTypes()
	if q.ProbeAvailability {
		for i := range out {
			rt, err := runtime.New(ctx, out[i].Type, s.layout)
			if err != nil {
				out[i].Note = err.Error()
			} else {
				out[i].Available = true
				_ = rt.Close()
			}
		}
	}
	return out
}

// BackendTypes returns the static catalog of every registered backend type, in
// registration order, with no availability probe. Pure descriptor metadata and
// no host state, so it needs no System handle. Use the System method when you
// also want each entry's live availability (BackendQuery.ProbeAvailability).
func BackendTypes() []BackendInfo {
	descs := runtime.Descriptors()
	out := make([]BackendInfo, 0, len(descs))
	for _, desc := range descs {
		out = append(out, backendInfoFromDescriptor(desc))
	}
	return out
}

// BackendInstalled reports whether the named backend's tool is present on the
// host — the cheaper "installed" tier (binary exists), distinct from "running"
// (daemon reachable, what CheckBackend/Available report). The setup wizard uses
// it to tag presets the user can pick but hasn't installed yet, without paying a
// daemon dial per option.
func (s *System) BackendInstalled(ctx context.Context, name BackendType) bool {
	installed, _ := runtime.Installed(ctx, name, s.layout.Env().EnvForDaemonDiscovery())
	return installed
}

// CheckBackend probes a single backend for availability by constructing a
// runtime and closing it. Returns whether the backend is reachable and a short
// note explaining the failure when it is not. This is the single-backend
// counterpart to BackendTypes(ctx, BackendQuery{ProbeAvailability: true}); both use
// the identical construct-and-close probe.
func (s *System) CheckBackend(ctx context.Context, name BackendType) (available bool, note string) {
	rt, err := runtime.New(ctx, name, s.layout)
	if err != nil {
		return false, err.Error()
	}
	_ = rt.Close()
	return true, ""
}

// Archetypes returns the sorted list of valid environment-archetype names
// yoloai ships (used to auto-shape a sandbox's setup). Static metadata; no host
// state is consulted.
func (s *System) Archetypes() []string {
	return Archetypes()
}

// Archetypes returns the sorted list of valid environment-archetype names
// yoloai ships. Static metadata with no host state, so it needs no Client or
// System handle — callers that only want the catalog (e.g. CLI flag help) can
// use it without constructing anything that can fail.
func Archetypes() []string {
	return archetype.ValidArchetypes()
}

func agentInfoFromDefinition(def *agent.Definition) AgentInfo {
	return AgentInfo{
		Type:             def.Type,
		Description:      def.Description,
		PromptMode:       string(def.PromptMode),
		SupportsHeadless: def.HeadlessCmd != "",
		SupportsResume:   def.ResumeFlag != "",
		IdleHook:         def.Idle.Hook,
		APIKeyEnvVars:    def.APIKeyEnvVars,
		StateDir:         def.StateDir,
		ModelFlag:        def.ModelFlag,
		ModelAliases:     def.ModelAliases,
		NetworkFloor:     def.NetworkAllowlist,
	}
}

func backendInfoFromDescriptor(desc runtime.BackendDescriptor) BackendInfo {
	return BackendInfo{
		Type:                desc.Type,
		Description:         desc.Description,
		Platforms:           desc.Platforms,
		Architectures:       desc.Architectures,
		IsolationTargetOnly: desc.IsolationTargetOnly,
		Requires:            desc.Requires,
		InstallHint:         desc.InstallHint,
		HostFromContainer:   desc.HostFromContainer,
	}
}

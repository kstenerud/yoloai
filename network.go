// ABOUTME: Network is the per-sandbox network-allowlist sub-handle. Exposes
// ABOUTME: typed AllowedDomain with provenance (agent-requirement vs user-added).

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/netpolicy"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// DomainSource identifies where an allowed domain came from.
// See netpolicy.DomainSource for the authoritative definition.
type DomainSource = netpolicy.DomainSource

// AllowedDomain is one entry in Network.Allowed().
// See netpolicy.AllowedDomain for the authoritative definition.
type AllowedDomain = netpolicy.AllowedDomain

const (
	// AllowedFromAgentRequirement means the agent's built-in
	// NetworkAllowlist requires this domain. Removing it may break
	// the agent.
	AllowedFromAgentRequirement DomainSource = netpolicy.AllowedFromAgentRequirement

	// AllowedFromUser means the domain was added by the user.
	AllowedFromUser DomainSource = netpolicy.AllowedFromUser
)

// Network is the per-sandbox network-allowlist sub-handle.
//
// Q-V resolution (2026-05-25): provenance is RECOVERABLE at read
// time because the agent's default allowlist is shipped data
// reachable via agent.GetAgent(name).NetworkAllowlist. The on-disk
// store (meta.NetworkAllow) flattens agent + user entries together,
// but Allowed() splits them back out at read time:
//
//	agent-required = meta.NetworkAllow ∩ agentDef.NetworkAllowlist
//	user-added     = meta.NetworkAllow \ agentDef.NetworkAllowlist
type Network struct {
	engine *orchestrator.Engine
	name   string
}

// Allowed returns the sandbox's network allowlist with each entry
// tagged by its source. Order matches meta.NetworkAllow's on-disk
// order. Returns an empty (non-nil) slice when the sandbox is not
// in :isolated network mode or has no entries, so JSON callers
// render `[]` rather than `null`.
func (n *Network) Allowed(_ context.Context) ([]AllowedDomain, error) {
	meta, err := n.loadEnvironment()
	if err != nil {
		return nil, err
	}
	agentType, err := n.agentType()
	if err != nil {
		return nil, err
	}
	return computeAllowedDomains(meta, agentType), nil
}

// agentType resolves the sandbox's configured agent type from agent.json, the
// inside-process config the substrate record no longer carries (Q104). It feeds
// the network-floor provenance; a sandbox loaded successfully has been migrated,
// so agent.json is present.
func (n *Network) agentType() (string, error) {
	acfg, err := n.engine.LoadAgentConfig(n.name)
	if err != nil {
		return "", err
	}
	return acfg.AgentType, nil
}

// Allow adds domains to the user-source portion of the allowlist.
// De-duplicates against existing entries (regardless of source) and
// returns only the domains that were newly added.
//
// When the sandbox container is running, this also live-patches the
// in-container ipset rules so the changes take effect immediately;
// AllowResult.Live signals whether that live-patch succeeded.
// Either way, the on-disk allowlist is updated and will take effect
// on the next start.
//
// Returns a *UsageError if the sandbox isn't using :isolated network
// mode (only isolated mode has an enforceable allowlist).
func (n *Network) Allow(ctx context.Context, domains ...string) (*AllowResult, error) {
	if len(domains) == 0 {
		return nil, yoerrors.NewUsageError("at least one domain is required")
	}

	meta, err := n.requireIsolated()
	if err != nil {
		return nil, err
	}

	existing := make(map[string]bool, len(meta.NetworkAllow))
	for _, d := range meta.NetworkAllow {
		existing[d] = true
	}
	added := make([]string, 0, len(domains))
	for _, d := range domains {
		if existing[d] {
			continue
		}
		existing[d] = true // dedupe within the input slice too
		added = append(added, d)
	}

	if len(added) == 0 {
		return &AllowResult{Added: []string{}, Live: false}, nil
	}

	meta.NetworkAllow = append(meta.NetworkAllow, added...)
	if err := n.engine.SaveNetworkAllowlist(n.name, meta); err != nil {
		return nil, err
	}

	live, _ := n.engine.LivePatchNetwork(ctx, n.name, ipsetResolveDomainsScript, added)
	return &AllowResult{Added: added, Live: live}, nil
}

// Deny removes domains from the allowlist. Returns *UsageError if any
// of the requested domains is not currently in the list (no partial
// failures — caller has a typo, not a race).
//
// Removed entries are returned with their source so callers can warn
// when an agent-required domain was removed (the library doesn't
// block; that's a UI policy decision per Q-V).
//
// Live-patching flushes the in-container ipset and re-adds the
// remaining domains. DenyResult.Live signals whether that succeeded.
func (n *Network) Deny(ctx context.Context, domains ...string) (*DenyResult, error) {
	if len(domains) == 0 {
		return nil, yoerrors.NewUsageError("at least one domain is required")
	}

	meta, err := n.requireIsolated()
	if err != nil {
		return nil, err
	}

	existing := make(map[string]bool, len(meta.NetworkAllow))
	for _, d := range meta.NetworkAllow {
		existing[d] = true
	}
	for _, d := range domains {
		if !existing[d] {
			return nil, yoerrors.NewUsageError("domain %q is not in the allowlist", d)
		}
	}

	agentType, err := n.agentType()
	if err != nil {
		return nil, err
	}
	// Provenance of removed entries — computed before we mutate meta.
	removed := netpolicy.WithProvenance(domains, agentNetworkFloor(agentType))
	toRemove := make(map[string]bool, len(domains))
	for _, d := range domains {
		toRemove[d] = true
	}

	remaining := make([]string, 0, len(meta.NetworkAllow))
	for _, d := range meta.NetworkAllow {
		if !toRemove[d] {
			remaining = append(remaining, d)
		}
	}

	meta.NetworkAllow = remaining
	if err := n.engine.SaveNetworkAllowlist(n.name, meta); err != nil {
		return nil, err
	}

	// Flush + re-add the remaining domains so the live ipset matches.
	// Empty remaining list still flushes (clears all live rules).
	script := "ipset flush allowed-domains 2>/dev/null || true"
	if len(remaining) > 0 {
		script += "\n" + ipsetResolveDomainsScript
	}
	live, _ := n.engine.LivePatchNetwork(ctx, n.name, script, remaining)
	return &DenyResult{Removed: removed, Live: live}, nil
}

// AllowResult is returned by Network.Allow.
type AllowResult struct {
	// Added lists the domains that were newly added (input
	// deduplicated against existing entries). Always non-nil so
	// JSON callers render [] when nothing was added.
	Added []string `json:"added"`
	// Live is true if the in-container ipset was patched
	// successfully; false if the sandbox isn't running, the runtime
	// is unreachable, or the exec failed. Either way, the on-disk
	// allowlist was updated.
	Live bool `json:"live"`
}

// DenyResult is returned by Network.Deny.
type DenyResult struct {
	// Removed lists the domains that were removed, with each
	// entry's source. Always non-nil.
	Removed []AllowedDomain `json:"removed"`
	// Live mirrors AllowResult.Live.
	Live bool `json:"live"`
}

// --- helpers ---

// loadEnvironment reads the sandbox's environment.json. The Network handle's
// methods all start with this read, so it's centralized here.
func (n *Network) loadEnvironment() (*store.Environment, error) {
	return n.engine.LoadEnvironment(n.name)
}

// requireIsolated loads meta and rejects sandboxes that aren't in
// :isolated network mode.
func (n *Network) requireIsolated() (*store.Environment, error) {
	meta, err := n.engine.LoadEnvironment(n.name)
	if err != nil {
		return nil, err
	}
	switch meta.NetworkMode {
	case "isolated":
		return meta, nil
	case "none":
		return nil, yoerrors.NewUsageError("sandbox %q uses --network-none; cannot modify network access", n.name)
	default:
		return nil, yoerrors.NewUsageError("sandbox %q is not using network isolation", n.name)
	}
}

// computeAllowedDomains turns flat meta.NetworkAllow into typed
// entries with provenance computed from the bound agent's
// definition. Order matches meta order; unknown agent name produces
// all-user entries (no agent → no known requirements).
func computeAllowedDomains(meta *store.Environment, agentType string) []AllowedDomain {
	return netpolicy.WithProvenance(meta.NetworkAllow, agentNetworkFloor(agentType))
}

// agentNetworkFloor returns the domains the named agent's definition requires
// (its network floor). Returns nil for an unknown/empty agent — provenance then
// degrades gracefully to "everything looks user-added".
func agentNetworkFloor(agentName string) []string {
	def := agent.GetAgent(agentName)
	if def == nil {
		return nil
	}
	return def.NetworkAllowlist
}

// ipsetResolveDomainsScript is the shell fragment that resolves
// domains to IPs and adds them to the ipset. Kept identical to the
// previous CLI-side script so live-patch behavior doesn't change.
//
// Args are positional: $1 onward are domain names.
const ipsetResolveDomainsScript = `for domain in "$@"; do
  for ip in $(dig +short A "$domain" 2>/dev/null); do
    echo "$ip" | grep -qE "^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$" && \
      ipset add allowed-domains "$ip" 2>/dev/null || true
  done
done`

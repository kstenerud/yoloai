// ABOUTME: Network is the per-sandbox network-allowlist sub-handle. Exposes
// ABOUTME: typed AllowedDomain with provenance (agent-requirement vs user-added).

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// DomainSource identifies where an allowed domain came from. Used by
// callers (UIs, automation) that want to distinguish agent-required
// domains from user-added ones — e.g. to warn before removing a
// domain the agent itself needs to function.
type DomainSource string

const (
	// AllowedFromAgentRequirement means the agent's definition
	// (agent.Definition.NetworkAllowlist) requires this domain.
	// Removing it will break the agent itself; embedders should
	// surface the consequence rather than silently accept the
	// removal.
	AllowedFromAgentRequirement DomainSource = "agent-requirement"

	// AllowedFromUser means the user explicitly added this domain —
	// either via RunOptions.AllowDomains at create time or via
	// `yoloai sandbox <name> allow` at runtime. The on-disk store
	// can't distinguish between those two sub-cases today (Q-V); if
	// a use case justifies it later a third source can be added
	// without breaking this contract.
	AllowedFromUser DomainSource = "user"
)

// AllowedDomain is one entry in Network.Allowed(). Domain is the
// host pattern (e.g. "api.anthropic.com"); Source identifies why
// it's on the list.
type AllowedDomain struct {
	Domain string       `json:"domain"`
	Source DomainSource `json:"source"`
}

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
	s *Sandbox
}

// Network returns the sandbox's network-management sub-handle.
func (s *Sandbox) Network() *Network {
	return &Network{s: s}
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
	return computeAllowedDomains(meta), nil
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

	sandboxDir, meta, err := n.requireIsolated()
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
	if err := saveNetworkAllowlist(sandboxDir, meta); err != nil {
		return nil, err
	}

	live, _ := n.tryLivePatch(ctx, ipsetResolveDomainsScript, added)
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

	sandboxDir, meta, err := n.requireIsolated()
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

	// Provenance of removed entries — computed before we mutate meta.
	agentSet := agentDomainSet(string(meta.Agent))
	toRemove := make(map[string]bool, len(domains))
	removed := make([]AllowedDomain, 0, len(domains))
	for _, d := range domains {
		toRemove[d] = true
		source := AllowedFromUser
		if agentSet[d] {
			source = AllowedFromAgentRequirement
		}
		removed = append(removed, AllowedDomain{Domain: d, Source: source})
	}

	remaining := make([]string, 0, len(meta.NetworkAllow))
	for _, d := range meta.NetworkAllow {
		if !toRemove[d] {
			remaining = append(remaining, d)
		}
	}

	meta.NetworkAllow = remaining
	if err := saveNetworkAllowlist(sandboxDir, meta); err != nil {
		return nil, err
	}

	// Flush + re-add the remaining domains so the live ipset matches.
	// Empty remaining list still flushes (clears all live rules).
	script := "ipset flush allowed-domains 2>/dev/null || true"
	if len(remaining) > 0 {
		script += "\n" + ipsetResolveDomainsScript
	}
	live, _ := n.tryLivePatch(ctx, script, remaining)
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
	sandboxDir := n.s.c.layout.SandboxDir(n.s.name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}
	return store.LoadEnvironment(sandboxDir)
}

// requireIsolated loads meta and rejects sandboxes that aren't in
// :isolated network mode. Returns the sandbox directory path along
// with the loaded meta so callers don't redo path resolution.
func (n *Network) requireIsolated() (string, *store.Environment, error) {
	sandboxDir := n.s.c.layout.SandboxDir(n.s.name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return "", nil, err
	}
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return "", nil, err
	}
	switch meta.NetworkMode {
	case "isolated":
		return sandboxDir, meta, nil
	case "none":
		return "", nil, yoerrors.NewUsageError("sandbox %q uses --network-none; cannot modify network access", n.s.name)
	default:
		return "", nil, yoerrors.NewUsageError("sandbox %q is not using network isolation", n.s.name)
	}
}

// tryLivePatch attempts to exec a shell script inside the running
// sandbox container to live-update ipset rules. Returns (live, err)
// where live is true iff the exec succeeded; err is the runtime
// error if the exec failed (caller can surface; not fatal because
// the on-disk update is the source of truth).
//
// Soft-fails (sandbox not running, runtime not constructible, or a
// Client that wasn't built with a runtime+manager at all) return
// (false, nil) so the caller treats them the same as a successful
// "no-op": the change is queued for the next start.
func (n *Network) tryLivePatch(ctx context.Context, script string, scriptArgs []string) (bool, error) {
	// A Client constructed without a runtime (e.g. for tests that
	// only exercise the on-disk allowlist) has no way to live-patch.
	// Treat that as "soft-fail; persisted-only" rather than panicking.
	if n.s.c.manager == nil || n.s.c.rt == nil {
		return false, nil
	}

	info, err := n.s.c.manager.Inspect(ctx, n.s.name)
	if err != nil {
		return false, nil //nolint:nilerr // soft-fail: not running, can't live-patch
	}
	if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		return false, nil
	}

	execArgs := []string{"sh", "-c", script, "_"}
	execArgs = append(execArgs, scriptArgs...)
	if _, err := n.s.c.rt.Exec(ctx, store.InstanceName(n.s.c.layout.Principal, n.s.name), execArgs, "0"); err != nil {
		return false, err
	}
	return true, nil
}

// saveNetworkAllowlist persists meta + the matching runtime-config
// patch. Lives outside Network so per-handle tests can stub it; it's
// a thin wrapper over the existing sandbox-side primitives.
func saveNetworkAllowlist(sandboxDir string, meta *store.Environment) error {
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return err
	}
	return sandbox.PatchConfigAllowedDomains(sandboxDir, meta.NetworkAllow)
}

// computeAllowedDomains turns flat meta.NetworkAllow into typed
// entries with provenance computed from the bound agent's
// definition. Order matches meta order; unknown agent name produces
// all-user entries (no agent → no known requirements).
func computeAllowedDomains(meta *store.Environment) []AllowedDomain {
	agentSet := agentDomainSet(string(meta.Agent))
	out := make([]AllowedDomain, 0, len(meta.NetworkAllow))
	for _, d := range meta.NetworkAllow {
		source := AllowedFromUser
		if agentSet[d] {
			source = AllowedFromAgentRequirement
		}
		out = append(out, AllowedDomain{Domain: d, Source: source})
	}
	return out
}

// agentDomainSet returns the set of domains the named agent's
// definition requires. Returns an empty (non-nil) map for unknown
// agents — provenance derivation degrades gracefully to "everything
// looks user-added" rather than blowing up.
func agentDomainSet(agentName string) map[string]bool {
	out := make(map[string]bool)
	def := agent.GetAgent(agentName)
	if def == nil {
		return out
	}
	for _, d := range def.NetworkAllowlist {
		out[d] = true
	}
	return out
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

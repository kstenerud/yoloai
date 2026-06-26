// ABOUTME: compose.go implements network-policy composition: the domain-allowlist
// ABOUTME: construction logic (provenance tagging, mode resolution).
// ABOUTME: Enforcement (iptables/ipset) and transport live in separate layers.

package netpolicy

// DomainSource identifies where an allowed domain came from. Used by
// callers that want to distinguish agent-required domains from
// user-added ones — e.g. to warn before removing a domain the agent
// itself needs to function.
type DomainSource string

const (
	// AllowedFromAgentRequirement means the agent's built-in
	// NetworkAllowlist requires this domain. Removing it may break
	// the agent.
	AllowedFromAgentRequirement DomainSource = "agent-requirement"

	// AllowedFromUser means the domain was added by the user.
	AllowedFromUser DomainSource = "user"
)

// AllowedDomain is one entry in a composed network allowlist.
type AllowedDomain struct {
	Domain string       `json:"domain"`
	Source DomainSource `json:"source"`
}

// WithProvenance tags each domain in allow with its source: domains
// present in the caller-supplied agentFloor slice are tagged
// AllowedFromAgentRequirement; all others are AllowedFromUser. The
// caller supplies agentDef.NetworkAllowlist (or nil for an unknown
// agent — provenance degrades gracefully to "everything looks
// user-added"). Order matches allow; returns an empty non-nil slice
// when allow is empty.
func WithProvenance(allow []string, agentFloor []string) []AllowedDomain {
	agentSet := make(map[string]bool, len(agentFloor))
	for _, d := range agentFloor {
		agentSet[d] = true
	}
	out := make([]AllowedDomain, 0, len(allow))
	for _, d := range allow {
		source := AllowedFromUser
		if agentSet[d] {
			source = AllowedFromAgentRequirement
		}
		out = append(out, AllowedDomain{Domain: d, Source: source})
	}
	return out
}

// Compose resolves the effective network mode and allowlist from a raw
// mode string and the agent/user domain lists. mode values match the
// NetworkMode constants ("none", "isolated", ""). agentFloor is the
// agent's built-in allowlist (caller passes agentDef.NetworkAllowlist);
// userAllow is the user-supplied list.
func Compose(mode string, agentFloor []string, userAllow []string) (effectiveMode string, allow []string) {
	switch mode {
	case "none":
		return "none", nil
	case "isolated":
		var combined []string
		combined = append(combined, agentFloor...)
		combined = append(combined, userAllow...)
		return "isolated", combined
	default:
		return "", nil
	}
}

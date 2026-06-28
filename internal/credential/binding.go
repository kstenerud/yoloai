// ABOUTME: CredentialBinding ties a Destination to an Apply+Source, and ApplyTo
// ABOUTME: injects every matching binding into a request (signers run last).
package credential

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Destination identifies which outbound requests a binding authenticates. It is
// matched against a request's host case-insensitively and port-insensitively
// (e.g. "api.anthropic.com"). It is the upstream the broker injects credentials
// for; an empty Destination matches nothing — a binding must name its target.
type Destination string

// Matches reports whether d applies to a request bound for host (which may carry
// a ":port" suffix).
func (d Destination) Matches(host string) bool {
	if d == "" {
		return false
	}
	return strings.EqualFold(string(d), hostWithoutPort(host))
}

// CredentialBinding declares how to authenticate outbound requests to one
// Destination: resolve Source to the current secret, then Apply it to the
// request. This is the general broker shape (D105) — tool-agnostic, covering LLM
// API keys, git, and package registries alike — with reserved room (the
// request-signer Apply and the minting Source) so the interface need not break to
// add request-signing or minted credentials later.
type CredentialBinding struct {
	Destination Destination
	Apply       Apply
	Source      CredentialSource
}

// ApplyTo injects credentials into req for every binding whose Destination
// matches req's host. Non-signing transforms run first, in declared order; then
// request-signer transforms run, so they sign the fully assembled request (D105).
// A binding with a nil Apply or Source is skipped as not-yet-configured.
func ApplyTo(ctx context.Context, req *http.Request, bindings []CredentialBinding) error {
	host := requestHost(req)

	matched := make([]CredentialBinding, 0, len(bindings))
	for _, b := range bindings {
		if b.Apply == nil || b.Source == nil {
			continue
		}
		if b.Destination.Matches(host) {
			matched = append(matched, b)
		}
	}

	// Two passes keep the "signer sees the final request" invariant explicit:
	// every non-signer applies before any signer.
	if err := applyPass(ctx, req, matched, false); err != nil {
		return err
	}
	return applyPass(ctx, req, matched, true)
}

// applyPass applies the subset of bindings whose runsLast matches signers.
func applyPass(ctx context.Context, req *http.Request, bindings []CredentialBinding, signers bool) error {
	for _, b := range bindings {
		if b.Apply.runsLast() != signers {
			continue
		}
		value, err := b.Source.Value(ctx)
		if err != nil {
			return fmt.Errorf("credential: resolve %q: %w", b.Destination, err)
		}
		if err := b.Apply.applyTo(req, value); err != nil {
			return fmt.Errorf("credential: apply to %q: %w", b.Destination, err)
		}
	}
	return nil
}

// requestHost returns the host a request is bound for, preferring the URL host
// (set on a client/forwarded request) over the Host header (set on a received
// server request).
func requestHost(req *http.Request) string {
	if req.URL != nil && req.URL.Host != "" {
		return req.URL.Host
	}
	return req.Host
}

// hostWithoutPort strips a ":port" suffix if present.
func hostWithoutPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

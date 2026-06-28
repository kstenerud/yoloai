// ABOUTME: Apply — the inject half of the broker: a closed set of request
// ABOUTME: transforms (header-set | basic-auth | request-signer) over egress.
package credential

import (
	"fmt"
	"net/http"
)

// Apply injects a resolved credential value into an outbound request. The set is
// closed — header-set | basic-auth | request-signer — and sealed via the
// unexported marker method. request-signer runs LAST: it signs the fully
// assembled request, so it must see every other transform's output (D105
// validation addendum).
type Apply interface {
	// applyTo injects value (the secret resolved from the binding's Source) into
	// req, mutating it in place.
	applyTo(req *http.Request, value string) error

	// runsLast reports whether this transform must run after all others in a
	// binding set. Only request-signer returns true.
	runsLast() bool

	isApply()
}

// HeaderSet sets one request header to Prefix+value — the common bearer/api-key
// shape. Prefix is "" for a raw header (e.g. "x-api-key") or "Bearer " for an
// Authorization bearer. (D105's "header-template", reduced to a prefix, covers
// every brokered header surveyed: Anthropic x-api-key, OpenAI/Google/GitHub-App
// bearer, npm/Azure tokens.)
type HeaderSet struct {
	Header string
	Prefix string
}

func (h HeaderSet) applyTo(req *http.Request, value string) error {
	if h.Header == "" {
		return fmt.Errorf("credential: HeaderSet has an empty Header")
	}
	req.Header.Set(h.Header, h.Prefix+value)
	return nil
}

func (HeaderSet) runsLast() bool { return false }
func (HeaderSet) isApply()       {}

// BasicAuth sets Authorization: Basic base64(Username:value), where the resolved
// value is the password/token (git, npm, PyPI basic-auth).
type BasicAuth struct {
	Username string
}

func (b BasicAuth) applyTo(req *http.Request, value string) error {
	req.SetBasicAuth(b.Username, value)
	return nil
}

func (BasicAuth) runsLast() bool { return false }
func (BasicAuth) isApply()       {}

// RequestSigner is reserved for schemes that sign the whole final request rather
// than set a static header: AWS SigV4, Azure SharedKey. It runs last because it
// hashes the final method, path, headers, and body; the signing key still flows
// from the binding's Source. Reserved now to keep the Apply set closed without a
// later breaking change; applyTo returns ErrNotImplemented until the signer phase
// lands. Per D105, request-signing is NOT solved in the proxy yet.
type RequestSigner struct {
	// Scheme names the signing algorithm, e.g. "aws-sigv4", "azure-sharedkey".
	Scheme string
}

func (RequestSigner) applyTo(*http.Request, string) error { return ErrNotImplemented }
func (RequestSigner) runsLast() bool                      { return true }
func (RequestSigner) isApply()                            {}

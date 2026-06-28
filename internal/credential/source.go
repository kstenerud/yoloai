// ABOUTME: CredentialSource — the resolve half of the broker: yields the current
// ABOUTME: secret (static | refreshing | minting), refreshing short-lived tokens.
package credential

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CredentialSource yields the current secret value for a CredentialBinding,
// refreshing short-lived tokens transparently. It is the D95/D105 abstraction:
// the embedder supplies the source (minting, scoping, and revoking real
// credentials is the embedder's job — yoloai performs zero ambient credential
// reads, D63); yoloai owns delivery, injection, and refresh orchestration.
//
// The set is closed — static | refreshing | minting — and sealed via the
// unexported marker method, so no out-of-package type can satisfy it and callers
// can switch over the variants exhaustively.
type CredentialSource interface {
	// Value returns the current secret, refreshing it first if it has expired.
	// It is safe for concurrent use: the broker resolves it per request.
	Value(ctx context.Context) (string, error)

	isCredentialSource()
}

// StaticSource is a fixed secret that never expires — the degenerate
// CredentialSource (a personal API key, a static registry token).
type StaticSource struct {
	secret string
}

// Static returns a CredentialSource that always yields secret.
func Static(secret string) StaticSource { return StaticSource{secret: secret} }

// Value returns the fixed secret.
func (s StaticSource) Value(context.Context) (string, error) { return s.secret, nil }

func (StaticSource) isCredentialSource() {}

// FetchFunc obtains a fresh secret and the instant it stops being valid. The
// embedder supplies it (e.g. a WIF or GitHub-App token exchange, a Vault read).
// A zero expiresAt means "never expires" and makes the source behave statically.
type FetchFunc func(ctx context.Context) (value string, expiresAt time.Time, err error)

// defaultRefreshSkew is how long before expiresAt a RefreshingSource re-fetches,
// so a token is never handed out moments before it lapses mid-flight.
const defaultRefreshSkew = 60 * time.Second

// RefreshingSource caches a short-lived secret and re-fetches it (via FetchFunc)
// before it expires. It is concurrency-safe; the clock is injectable for tests.
type RefreshingSource struct {
	fetch FetchFunc
	skew  time.Duration
	clock func() time.Time

	mu        sync.Mutex
	value     string
	expiresAt time.Time
	fetched   bool
}

// RefreshOption configures a RefreshingSource.
type RefreshOption func(*RefreshingSource)

// WithRefreshSkew sets how long before expiry the source re-fetches (default 60s).
func WithRefreshSkew(d time.Duration) RefreshOption {
	return func(s *RefreshingSource) { s.skew = d }
}

// Refreshing returns a CredentialSource that calls fetch on first use and again
// before each expiry. fetch must be non-nil.
func Refreshing(fetch FetchFunc, opts ...RefreshOption) *RefreshingSource {
	s := &RefreshingSource{fetch: fetch, skew: defaultRefreshSkew, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Value returns the cached secret, re-fetching first if it is unset or expired.
func (s *RefreshingSource) Value(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fetched && !s.expired() {
		return s.value, nil
	}

	value, expiresAt, err := s.fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("credential: refresh: %w", err)
	}
	s.value = value
	s.expiresAt = expiresAt
	s.fetched = true
	return s.value, nil
}

// expired reports whether the cached value is within skew of (or past) expiry. A
// zero expiresAt is treated as never-expiring.
func (s *RefreshingSource) expired() bool {
	if s.expiresAt.IsZero() {
		return false
	}
	return !s.clock().Before(s.expiresAt.Add(-s.skew))
}

func (*RefreshingSource) isCredentialSource() {}

// MintingSource is reserved for credentials produced by a multi-step exchange
// rather than a single fetch: a GitHub-App JWT minted into a 1h installation
// token, or a Docker/OCI 401->token-exchange dance. D105 routes Docker/OCI to an
// in-sandbox credential helper (not the proxy); the GitHub-App path mints
// host-side. Reserved now to keep the Source set closed without a later breaking
// change; Value returns ErrNotImplemented until the minting phase lands.
type MintingSource struct {
	// Scheme names the minting mechanism, e.g. "github-app", "oci-token-exchange".
	Scheme string
}

// Value reports that minting is not yet built.
func (MintingSource) Value(context.Context) (string, error) { return "", ErrNotImplemented }

func (MintingSource) isCredentialSource() {}

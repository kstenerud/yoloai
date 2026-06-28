// ABOUTME: Sentinel errors for the credential broker, notably the marker the
// ABOUTME: reserved-but-unbuilt Apply/Source variants return until they land.
package credential

import "errors"

// ErrNotImplemented is returned by the reserved variants whose interface shape is
// fixed now but whose behavior is built in a later phase: the request-signer
// Apply (AWS SigV4 / Azure SharedKey) and the minting Source (GitHub-App,
// Docker/OCI). They exist so the closed Apply/Source sets need not break to add
// them (D105 validation addendum: retrofitting the shape later is a breaking
// interface change).
var ErrNotImplemented = errors.New("credential: not implemented")

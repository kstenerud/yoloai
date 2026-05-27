package runtime

// ABOUTME: Helpers that recognize specific SDK error conditions across
// ABOUTME: backends. Text-match fallbacks are documented as irreducible.

import (
	"errors"
	"io/fs"
	"strings"
	"syscall"
)

// IsPermissionDenied reports whether err represents a "permission denied"
// failure, checking both typed (fs.ErrPermission / syscall.EACCES) and text-
// match paths.
//
// The text-match fallback exists because the Docker and containerd SDK errors
// — which surface from gRPC transports and HTTP clients — do NOT always wrap
// the underlying syscall error in a form that errors.Is can detect. The
// strings the SDKs emit are protocol-level identifiers, not localized
// human-facing messages, so the text match is robust in practice. W8 of the
// architecture remediation plan documented this as irreducible at a
// chokepoint.
func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES) {
		return true
	}
	return strings.Contains(err.Error(), "permission denied")
}

// IsAddressInUse reports whether err represents an EADDRINUSE failure,
// checking both typed (syscall.EADDRINUSE) and text-match paths.
//
// As with IsPermissionDenied, the text fallback exists because containerd's
// shim errors come through TTRPC and don't reliably unwrap to the syscall
// error. The "address in use" / "address already in use" strings are
// protocol-stable identifiers, not localized messages.
func IsAddressInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address in use") || strings.Contains(msg, "address already in use")
}

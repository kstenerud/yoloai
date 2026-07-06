// ABOUTME: Non-macOS stub for the seatbelt host-process census — seatbelt's host
// ABOUTME: processes only run on macOS, so there is nothing to enumerate elsewhere.
//go:build !darwin

package seatbelt

// platformSandboxProcs has no seatbelt host processes to enumerate off macOS
// (seatbelt is a macOS-only backend), so it reports none.
func platformSandboxProcs(_ string) ([]sandboxProc, error) { return nil, nil }

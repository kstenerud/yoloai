// ABOUTME: Non-macOS stub for the host tmux-server census — seatbelt's host tmux
// ABOUTME: only runs on macOS, so there is nothing to enumerate elsewhere.
//go:build !darwin

package seatbelt

// platformTmuxServers has no host tmux to enumerate off macOS (seatbelt is a
// macOS-only backend), so it reports none.
func platformTmuxServers() ([]tmuxServer, error) { return nil, nil }

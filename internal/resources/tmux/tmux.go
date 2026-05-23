// ABOUTME: Embeds the yoloai default tmux.conf shared by setup and every
// ABOUTME: backend that runs tmux inside the sandbox.

// Package tmux exposes the embedded default tmux.conf used by yoloai's
// setup wizard and by backends that ship the config inside their image.
// The file is neutral — not owned by any particular backend.
package tmux

import _ "embed"

//go:embed tmux.conf
var embedded []byte

// Embedded returns the embedded default tmux.conf bytes.
func Embedded() []byte {
	return embedded
}

package seatbelt

// ABOUTME: Embeds the tmux config for seatbelt sandboxes.

import _ "embed"

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

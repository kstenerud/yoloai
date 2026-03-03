package seatbelt

// ABOUTME: Embeds the entrypoint script and tmux config for seatbelt sandboxes.

import _ "embed"

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

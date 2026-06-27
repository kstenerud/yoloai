package tart

// ABOUTME: Embeds the tmux config for Tart VMs.

import _ "embed"

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

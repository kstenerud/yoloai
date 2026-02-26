package tart

// ABOUTME: Embeds the post-boot setup script and tmux config for Tart VMs.

import _ "embed"

//go:embed resources/setup.sh
var embeddedSetupScript []byte

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

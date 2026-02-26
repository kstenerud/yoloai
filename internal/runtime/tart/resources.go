package tart

// ABOUTME: Embeds the post-boot setup script for Tart VMs.

import _ "embed"

//go:embed resources/setup.sh
var embeddedSetupScript []byte

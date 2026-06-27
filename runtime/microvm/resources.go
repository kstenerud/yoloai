//go:build linux

package microvm

// ABOUTME: Embeds the microvm image-layer build resources (the FROM yoloai-base
// ABOUTME: Dockerfile and the in-container rootfs-conversion script).

import _ "embed"

//go:embed resources/Dockerfile.microvm
var embeddedDockerfile []byte

//go:embed resources/microvm-convert.sh
var embeddedConvertScript []byte

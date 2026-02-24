package docker

import _ "embed"

//go:embed resources/Dockerfile.base
var embeddedDockerfile []byte

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte

package docker

import _ "embed"

//go:embed resources/Dockerfile.base
var embeddedDockerfile []byte

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

// EmbeddedTmuxConf returns the embedded tmux.conf content.
func EmbeddedTmuxConf() []byte {
	return embeddedTmuxConf
}

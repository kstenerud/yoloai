// Package docker embeds Docker build resources (Dockerfile, entrypoint, tmux config).
package docker

import (
	_ "embed"

	"github.com/kstenerud/yoloai/runtime/monitor"
)

//go:embed resources/Dockerfile
var embeddedDockerfile []byte

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte

//go:embed resources/entrypoint-user.sh
var embeddedEntrypointUser []byte

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

// embeddedStatusMonitor provides the shared Python status monitor script
// from the runtime/monitor package for inclusion in Docker image builds.
var embeddedStatusMonitor = monitor.Script()

// EmbeddedTmuxConf returns the embedded tmux.conf content.
func EmbeddedTmuxConf() []byte {
	return embeddedTmuxConf
}

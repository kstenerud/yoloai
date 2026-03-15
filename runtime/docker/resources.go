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

//go:embed resources/entrypoint.py
var embeddedEntrypointPy []byte

//go:embed resources/tmux.conf
var embeddedTmuxConf []byte

// embeddedSandboxSetup provides the consolidated Python sandbox setup script
// from the runtime/monitor package for inclusion in Docker image builds.
var embeddedSandboxSetup = monitor.SetupScript()

// embeddedStatusMonitor provides the shared Python status monitor script
// from the runtime/monitor package for inclusion in Docker image builds.
var embeddedStatusMonitor = monitor.Script()

// embeddedDiagnoseIdle provides the idle detection diagnostic script.
var embeddedDiagnoseIdle = monitor.DiagnoseScript()

// EmbeddedTmuxConf returns the embedded tmux.conf content.
func EmbeddedTmuxConf() []byte {
	return embeddedTmuxConf
}

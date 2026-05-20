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

// embeddedSetupHelpers provides the typed pure-function helpers module
// imported by sandbox-setup.py at runtime. Must ship alongside it.
var embeddedSetupHelpers = monitor.SetupHelpers()

// embeddedTmuxIO provides the injectable tmux/subprocess wrappers module
// imported by sandbox-setup.py at runtime. Must ship alongside it.
var embeddedTmuxIO = monitor.TmuxIO()

// embeddedStatusMonitor provides the shared Python status monitor script
// from the runtime/monitor package for inclusion in Docker image builds.
var embeddedStatusMonitor = monitor.Script()

// embeddedDiagnoseIdle provides the idle detection diagnostic script.
var embeddedDiagnoseIdle = monitor.DiagnoseScript()

// EmbeddedTmuxConf returns the embedded tmux.conf content.
func EmbeddedTmuxConf() []byte {
	return embeddedTmuxConf
}

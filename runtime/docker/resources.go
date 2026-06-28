// ABOUTME: Embeds Docker build resources (Dockerfile, entrypoints, Python
// ABOUTME: monitor scripts) and exposes them for the Docker backend image builder.
// Package docker embeds Docker build resources (Dockerfile, entrypoints, scripts).
// The shared tmux.conf lives in internal/resources/tmux (neutral location).
package docker

import (
	_ "embed"

	tmuxres "github.com/kstenerud/yoloai/internal/resources/tmux"
	"github.com/kstenerud/yoloai/runtime/monitor"
)

//go:embed resources/Dockerfile
var embeddedDockerfile []byte

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte

//go:embed resources/entrypoint.py
var embeddedEntrypointPy []byte

//go:embed resources/firewall.py
var embeddedFirewallPy []byte

//go:embed resources/install-firewall.py
var embeddedInstallFirewallPy []byte

// embeddedTmuxConf is the shared default tmux.conf, sourced from the neutral
// internal/resources/tmux package rather than re-embedded here.
var embeddedTmuxConf = tmuxres.Embedded()

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

// embeddedAgentRun provides the fall-to-shell agent launch wrapper (D96),
// installed executable in /yoloai/bin and invoked by the launch command for
// hook-authoritative agents.
var embeddedAgentRun = monitor.AgentRunScript()

// embeddedYoloaiResume provides the in-sandbox resume command (D96 DD4),
// installed executable in /yoloai/bin as `yoloai-resume`.
var embeddedYoloaiResume = monitor.YoloaiResumeScript()

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

// BaseDockerfile returns the embedded base-image Dockerfile — exactly the bytes
// handed to the builder, except on the apple backend, which blanks the prose
// comments out of its materialized copy first because its builder rejects a
// Dockerfile past an effective size ceiling with no diagnostic at all (DF229).
// Instructions are what the budget buys; see `TestBaseDockerfile_FitsTheAppleBuilder`
// (runtime/apple) and standards/dockerfile.md.
func BaseDockerfile() []byte { return embeddedDockerfile }

// referenceHeader is prepended by ReferenceDockerfile. It is deliberately NOT in
// the Dockerfile itself, for two reasons that point the same way.
//
// It would be false there: in the repo, editing that file is precisely how the
// image changes, so a "no effect" warning is only true of the generated copy.
// And a user-facing warning has no business sitting in the build context at all.
// (It would no longer be charged against apple's size budget — the apple backend
// blanks prose before building, DF229 — but that is a recent accident of the
// remedy, not the reason.)
const referenceHeader = `# ============================================================================
# yoloAI base image — REFERENCE COPY. EDITING THIS FILE HAS NO EFFECT.
#
# yoloAI wrote this so you can see what is in the sandbox image. The Dockerfile
# that actually builds it is compiled into the yoloai binary, so the build never
# reads this file, and yoloAI overwrites it on every setup — edits vanish with
# no warning.
#
# To change what a sandbox contains, write a profile Dockerfile, which IS read
# from disk:
#
#     mkdir -p ~/.yoloai/profiles/mine
#     printf 'FROM yoloai-base:latest\nRUN apt-get update && apt-get install -y cowsay\n' \
#         > ~/.yoloai/profiles/mine/Dockerfile
#     yoloai new my-sandbox ~/code/project --profile mine
# ============================================================================

`

// ReferenceDockerfile returns the base Dockerfile with a header explaining that
// it is a copy and that editing it does nothing. This is what gets written to
// disk for a human to read; BaseDockerfile is what gets built.
func ReferenceDockerfile() []byte {
	return append([]byte(referenceHeader), embeddedDockerfile...)
}

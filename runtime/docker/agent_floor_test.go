// ABOUTME: Guards the minimum-version floors on the agent CLIs the host drives
// ABOUTME: by hardcoded flags — specifically that each floor is QUOTED, since an
// ABOUTME: unquoted one is consumed by the shell and enforces nothing (DF158).

package docker

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDockerfile_AgentInstallsCarryAQuotedFloor pins the one way this fence
// fails silently.
//
// yoloAI drives these CLIs by flags hardcoded in internal/agent/agent.go, so the
// image has to be able to state which versions those flags exist in. `>=X.Y.Z`
// does that while leaving the maximum open, which the surrounding comment
// requires — the clients must keep tracking their servers' APIs.
//
// The quotes are the whole risk. Written the obvious way, `sh` reads `>=2.1.197`
// as a redirection: it creates a file named `=2.1.197`, passes npm the bare
// package name, and SUCCEEDS. The build goes green, latest gets installed, and
// the floor enforces nothing — the only trace is a stray file in that layer.
// Verified directly against the registry, 2026-07-30. Nothing else in the
// toolchain would catch it: hadolint accepts both forms, and the build cannot
// fail because there is nothing left to fail on.
func TestDockerfile_AgentInstallsCarryAQuotedFloor(t *testing.T) {
	dockerfile := string(embeddedDockerfile)

	t.Run("npm globals", func(t *testing.T) {
		// Capture the spec argument exactly as written, quotes included.
		re := regexp.MustCompile(`npm install -g (\S+)`)
		matches := re.FindAllStringSubmatch(dockerfile, -1)
		require.NotEmpty(t, matches, "found no `npm install -g` lines — has the Dockerfile changed shape?")

		for _, m := range matches {
			spec := m[1]
			assert.True(t, strings.HasPrefix(spec, `"`) && strings.HasSuffix(spec, `"`),
				"%s must be double-quoted: unquoted, the shell eats `>=` as a redirect and the floor silently enforces nothing", spec)
			assert.Contains(t, spec, "@>=",
				"%s must carry a minimum version: yoloAI drives this CLI by hardcoded flags and needs to state which versions have them", spec)
		}
	})

	t.Run("uv tools", func(t *testing.T) {
		// The package spec is the last token on the `uv tool install` line, which
		// is easier to read than a regex that has to cope with the env-var prefix
		// and the preceding line continuation.
		var specs []string
		for _, line := range strings.Split(dockerfile, "\n") {
			if !strings.Contains(line, "uv tool install") {
				continue
			}
			fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(line), `\`))
			specs = append(specs, fields[len(fields)-1])
		}
		require.NotEmpty(t, specs, "found no `uv tool install` line — has the Dockerfile changed shape?")

		for _, spec := range specs {
			assert.True(t, strings.HasPrefix(spec, `"`) && strings.HasSuffix(spec, `"`),
				"%s must be double-quoted for the same reason as the npm specs", spec)
			assert.Contains(t, spec, ">=",
				"%s must carry a minimum version", spec)
		}
	})
}

// TestDockerfile_FitsAppleBuilderLimit gates the one build constraint no build on
// this platform can discover.
//
// Apple's `container build` refuses a Dockerfile over 16384 bytes outright
// (apple/container#735, open as of 2026-07-30) — `invalidArgument: "Dockerfile
// size (N bytes) exceeds the maximum allowed size"`. docker, podman and
// containerd have no such limit, so a green Linux `make check` AND a green full
// Linux smoke run both coexist with a completely broken apple backend. It shows
// up only on a Mac, which is exactly the class of defect that deserves a cheap
// portable assertion instead.
//
// It is easy to trip because comments are the majority of this file: it sat at
// 14012 bytes, and a comment block plus a version bump plus a header took it to
// 17645 — with the version bump alone landing 33 bytes under the cap unnoticed.
// So this asserts real headroom rather than the bare limit, and when it fires the
// fix is to relocate prose (standards/dockerfile.md, the cited finding, a
// docstring), not to shave words until it passes.
func TestDockerfile_FitsAppleBuilderLimit(t *testing.T) {
	const appleMax = 16384
	// Enough slack that an ordinary comment edit cannot silently consume the last
	// of it, and small enough to leave the file usable.
	const wantHeadroom = 1024

	size := len(BaseDockerfile())
	assert.LessOrEqual(t, size, appleMax-wantHeadroom,
		"the embedded Dockerfile is %d bytes; apple/container rejects anything over %d, and this "+
			"keeps %d bytes of headroom so the next comment edit is not the one that breaks the "+
			"apple backend on a machine you cannot test. Relocate prose rather than trimming to fit.",
		size, appleMax, wantHeadroom)
}

// TestReferenceDockerfile_HeaderIsNotChargedToTheBuild pins the split. The
// "editing this does nothing" header belongs on the generated copy only: it is
// false in the repo, where editing IS how the image changes, and it must not
// consume build-context bytes against apple's cap. Prepending it to the built
// file would be wrong on both counts, and the second one breaks a backend.
func TestReferenceDockerfile_HeaderIsNotChargedToTheBuild(t *testing.T) {
	built, reference := string(BaseDockerfile()), string(ReferenceDockerfile())

	assert.NotContains(t, built, "EDITING THIS FILE HAS NO EFFECT",
		"the header must not be in the bytes handed to the builder")
	assert.Contains(t, reference, "EDITING THIS FILE HAS NO EFFECT",
		"the generated copy is the one that needs the warning")
	assert.True(t, strings.HasSuffix(reference, built),
		"the reference copy must be the built file verbatim plus a header, or it documents something else")
}

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

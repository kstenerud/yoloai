// ABOUTME: Guards that the base image ships the two linters `make check` treats
// ABOUTME: as required (D112) — and that hadolint, being a downloaded binary
// ABOUTME: rather than an apt package, stays version-pinned and checksummed.

package docker

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDockerfile_ShipsTheLintersMakeCheckRequires fences a gap that is invisible
// from inside the sandbox until you try to finish a change.
//
// `make check` treats hadolint and shellcheck as required, not optional (D112,
// D117): both lint artifacts that ship inside the binary, so their absence fails
// loudly instead of skipping. The Makefile's fallback is to run each via Docker,
// which needs a daemon the sandbox only has under container-privileged. So an
// agent working on this repo in an ordinary yoloAI sandbox could not run the
// project's own gate — it failed at hadolint, and then at shellcheck, with the
// change already written.
//
// What this test can and cannot do: it reads the Dockerfile text, so it proves
// the install is still *written*, not that the built image *has* the tools. Only
// a real build shows that, and that belongs to the smoke suite. Reverting either
// install turns this red, which is what it is for; treat it as a fence around
// the decision, not as evidence the image works.
func TestDockerfile_ShipsTheLintersMakeCheckRequires(t *testing.T) {
	dockerfile := string(embeddedDockerfile)

	t.Run("shellcheck comes from apt", func(t *testing.T) {
		assert.Regexp(t, `(?m)^\s+shellcheck \\$`, dockerfile,
			"shellcheck must stay in the apt install list — `make check` requires it (D112) "+
				"and the Docker fallback needs a daemon the sandbox lacks")
	})

	t.Run("hadolint is pinned and checksummed", func(t *testing.T) {
		require.Regexp(t, `ARG HADOLINT_VERSION=\d+\.\d+\.\d+`, dockerfile,
			"hadolint is not in Debian, so it is a downloaded binary and must carry an explicit version")

		// The URL must interpolate that ARG rather than hardcoding a version, or
		// the pin and the download drift apart silently.
		assert.Regexp(t, `hadolint/releases/download/v\$\{HADOLINT_VERSION\}/`, dockerfile,
			"the download URL must use ${HADOLINT_VERSION} so the pin is the single source of truth")

		// Both arches need a hash. standards/dockerfile.md asks downloaded
		// binaries to be pinned by version *and* checksum where possible, and
		// hadolint publishes checksums.sha256 per release, so it is possible.
		sums := regexp.MustCompile(`sum=[0-9a-f]{64}`).FindAllString(dockerfile, -1)
		assert.Len(t, sums, 2,
			"expected a sha256 for each supported arch (amd64, arm64); "+
				"an unverified download into every sandbox image is exactly the supply-chain "+
				"shape this project exists to contain")

		assert.Contains(t, dockerfile, "sha256sum -c -",
			"the checksums must actually be verified, not merely recorded")
	})
}

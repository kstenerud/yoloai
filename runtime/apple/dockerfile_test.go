// ABOUTME: Unit tests for the apple-only Dockerfile preflight: which comment
// ABOUTME: lines are prose (blanked) versus directives (kept), the two forms of
// ABOUTME: `#` line that are shell rather than comment, and the size refusal
// ABOUTME: that replaces the builder's opaque stream error.

package apple

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/feedback"

	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

func TestStripDockerfileProse_BlanksProseAndKeepsLineNumbers(t *testing.T) {
	src := "FROM alpine\n# a prose comment\nRUN true\n"
	got := string(stripDockerfileProse([]byte(src)))

	assert.Equal(t, "FROM alpine\n\nRUN true\n", got)
	assert.Equal(t, strings.Count(src, "\n"), strings.Count(got, "\n"),
		"the line count must not move: BuildKit reports errors by line number against the file it was handed, "+
			"so deleting a comment would make every diagnostic point at the wrong line of the repo's Dockerfile")
}

func TestStripDockerfileProse_KeepsDirectives(t *testing.T) {
	// Each of these changes how the file is built or linted; only the prose goes.
	src := "# syntax=docker/dockerfile:1\n" +
		"# escape=`\n" +
		"# just prose\n" +
		"# hadolint ignore=DL3008\n" +
		"RUN apt-get install -y curl\n" +
		"# shellcheck disable=SC2016\n" +
		"RUN true\n"
	got := string(stripDockerfileProse([]byte(src)))

	assert.Contains(t, got, "# syntax=docker/dockerfile:1",
		"dropping the syntax directive silently swaps the BuildKit frontend")
	assert.Contains(t, got, "# escape=`")
	assert.Contains(t, got, "# hadolint ignore=DL3008")
	assert.Contains(t, got, "# shellcheck disable=SC2016")
	assert.NotContains(t, got, "just prose")
}

// TestStripDockerfileProse_LeavesShellCommentsAlone covers the two `#` lines that
// are not Dockerfile comments at all. Blanking either one silently rewrites the
// author's script — the failure would be a build that runs and does the wrong
// thing, which is worse than the one this file exists to prevent.
func TestStripDockerfileProse_LeavesShellCommentsAlone(t *testing.T) {
	t.Run("inside a backslash continuation", func(t *testing.T) {
		src := "RUN echo one \\\n# this is shell, not a comment\n    && echo two\n"
		assert.Equal(t, src, string(stripDockerfileProse([]byte(src))))
	})

	t.Run("inside a heredoc body", func(t *testing.T) {
		src := "RUN <<EOF\n# shell comment in the script\necho hi\nEOF\n# real prose\nRUN true\n"
		got := string(stripDockerfileProse([]byte(src)))
		assert.Contains(t, got, "# shell comment in the script", "the heredoc body is the author's script")
		assert.NotContains(t, got, "# real prose", "but a comment after the delimiter is prose again")
	})

	t.Run("heredoc declared before a continuation", func(t *testing.T) {
		// The body starts after the whole logical line, not after the physical one.
		src := "RUN <<EOF \\\n    --flag\n# still inside the script\nEOF\n"
		assert.Equal(t, src, string(stripDockerfileProse([]byte(src))))
	})

	t.Run("quoted and dash-prefixed delimiters", func(t *testing.T) {
		src := "RUN <<-'EOF'\n# shell\nEOF\n"
		assert.Equal(t, src, string(stripDockerfileProse([]byte(src))))
	})
}

// TestPrepareDockerfile_RefusesAnOversizeDockerfile is the point of the preflight:
// past the ceiling the builder emits `Stream unexpectedly closed` and nothing
// else — no step list, no line, no file name — so the refusal has to happen here
// or not at all.
func TestPrepareDockerfile_RefusesAnOversizeDockerfile(t *testing.T) {
	dir := t.TempDir()
	oversize := "FROM alpine\n" + strings.Repeat("RUN echo "+strings.Repeat("x", 100)+"\n", 200)
	require.Greater(t, len(oversize), maxDockerfileBytes, "fixture must actually exceed the limit")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(oversize), 0o600))

	err := prepareDockerfile(dir)
	require.Error(t, err, "an oversize Dockerfile must be refused before the builder swallows it")
	assert.Contains(t, err.Error(), "Stream unexpectedly closed",
		"the error names the symptom the user would otherwise have to search for")
	assert.Contains(t, err.Error(), "apple builder accepts")
}

// TestPrepareDockerfile_CountsTheStrippedSize pins that the limit is measured
// after stripping, not before. A Dockerfile of mostly prose is fine — that is
// the whole reason the stripper exists — and charging it for bytes the builder
// never receives would reject files that build.
func TestPrepareDockerfile_CountsTheStrippedSize(t *testing.T) {
	dir := t.TempDir()
	prose := strings.Repeat("# "+strings.Repeat("p", 100)+"\n", 200)
	src := "FROM alpine\n" + prose + "RUN true\n"
	require.Greater(t, len(src), maxDockerfileBytes, "fixture must exceed the limit before stripping")
	path := filepath.Join(dir, "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	require.NoError(t, prepareDockerfile(dir), "prose is not charged against the limit")

	written, err := os.ReadFile(path) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, err)
	assert.LessOrEqual(t, len(written), maxDockerfileBytes)
	assert.Contains(t, string(written), "FROM alpine", "the instructions survive")
	assert.NotContains(t, string(written), "ppp", "the prose does not")
}

// noProseInContext is a fake `container` binary that fails the build if the
// Dockerfile it was handed still carries a prose comment. It asserts against the
// context on disk, so it goes red when the preflight is not *wired into* a build
// path — which the unit tests above cannot detect, since they call it directly.
const noProseInContext = "#!/bin/sh\n" +
	"for a in \"$@\"; do [ -f \"$a/Dockerfile\" ] && df=\"$a/Dockerfile\"; done\n" +
	"[ -n \"$df\" ] || { echo 'no context directory in argv' >&2; exit 9; }\n" +
	"if grep -E '^[[:space:]]*#' \"$df\" | grep -vE '^[[:space:]]*#[[:space:]]*(syntax|escape|check)=' " +
	"| grep -vqE '^[[:space:]]*#[[:space:]]*(hadolint|shellcheck) '; then\n" +
	"  echo 'prose comment reached the builder' >&2; exit 8\n" +
	"fi\n" +
	"exit 0\n"

func TestBuildBaseImage_StripsProseBeforeBuilding(t *testing.T) {
	r := newFakeContainerRuntime(t, noProseInContext)

	require.NoError(t, r.buildBaseImage(context.Background(), r.layout, feedback.DiscardProgress, slog.New(slog.DiscardHandler)),
		"the base Dockerfile is ~58%% prose; it must be stripped in the build path, not merely strippable")
}

func TestBuildProfileImage_StripsProseBeforeBuilding(t *testing.T) {
	r := newFakeContainerRuntime(t, noProseInContext)
	sourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "Dockerfile"),
		[]byte("# a profile's own prose\nFROM yoloai-base\n"), 0o600))

	var output strings.Builder
	require.NoError(t, r.BuildProfileImage(context.Background(), sourceDir, "yoloai-cli-dev", "", nil, r.layout,
		feedback.ProgressToWriter(&output), feedback.WriterSink(&output), slog.New(slog.DiscardHandler)),
		"a profile Dockerfile goes through the same builder and the same ceiling: %s", output.String())
}

func TestPrepareDockerfile_NoDockerfileIsNotAnError(t *testing.T) {
	assert.NoError(t, prepareDockerfile(t.TempDir()),
		"`container build` reports a missing Dockerfile better than a preflight guess could")
}

// TestBaseDockerfile_FitsTheAppleBuilder is the portable gate that
// TestDockerfile_FitsAppleBuilderLimit used to be, moved here and re-pointed at
// the number that actually reaches the builder. It asserts the *stripped* size,
// because that is what `container build` is handed, and against the measured
// ceiling rather than apple/container#735's documented 16384 — which is a real
// limit, just not the one that bites first. This test compiles and runs on every
// platform, which is the point: a Linux-only `make check` must still fail when
// the Dockerfile outgrows the one backend that cannot be tested there.
func TestBaseDockerfile_FitsTheAppleBuilder(t *testing.T) {
	src := dockerrt.BaseDockerfile()
	stripped := stripDockerfileProse(src)

	assert.LessOrEqual(t, len(stripped), maxDockerfileBytes,
		"the base Dockerfile is %d bytes (%d stripped) against a %d-byte limit. Past it the apple builder "+
			"fails with `Stream unexpectedly closed` and no output. Prose is free here — the stripper blanks it — "+
			"so this can only fire on instructions, and the fix is fewer or shorter ones, not shorter comments.",
		len(src), len(stripped), maxDockerfileBytes)
}

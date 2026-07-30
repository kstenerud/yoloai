// ABOUTME: Tests the shared embedded-file table and the launch-time
// ABOUTME: materialisation of /yoloai/bin, including the guard that the table and
// ABOUTME: the Dockerfile's COPY list cannot drift apart (DF156).

package docker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBaseBuildInputs_OrderIsStable pins the hash order, which is load-bearing in
// a way nothing else would reveal: buildInputsChecksum hashes name and content in
// slice order, and that value is the base image's identity label. Reordering the
// table silently marks every existing yoloai-base stale and rebuilds it once on
// every host — a slow, confusing, entirely invisible regression.
//
// The assertion is on names rather than a golden checksum on purpose: a golden
// hash changes whenever any script's content changes, so it would be updated
// reflexively and would stop meaning anything.
func TestBaseBuildInputs_OrderIsStable(t *testing.T) {
	var got []string
	for _, f := range baseBuildInputs() {
		got = append(got, f.name)
	}
	assert.Equal(t, []string{
		"Dockerfile",
		"entrypoint.sh",
		"entrypoint.py",
		"firewall.py",
		"install-firewall.py",
		"sandbox-setup.py",
		"setup_helpers.py",
		"tmux_io.py",
		"status-monitor.py",
		"diagnose-idle.sh",
		"agent-run.sh",
		"yoloai-resume",
		"tmux.conf",
	}, got, "append only — reordering rebuilds every yoloai-base in existence")
}

// TestBaseBuildInputs_MatchTheDockerfilesBinCopies closes the drift that produced
// DF156's sharp case. The Dockerfile's COPY list and the Go table are two
// hand-maintained descriptions of one set, and nothing typechecks a COPY line: a
// script can enter the image without entering the set delivered at launch, or the
// reverse, and either way the mount and the bake disagree about what /yoloai/bin
// contains. install-firewall.py entering the embed set is exactly how the finding
// started.
func TestBaseBuildInputs_MatchTheDockerfilesBinCopies(t *testing.T) {
	copyRe := regexp.MustCompile(`(?m)^COPY\s+(\S+)\s+/yoloai/bin/`)
	var fromDockerfile []string
	for _, m := range copyRe.FindAllStringSubmatch(string(embeddedDockerfile), -1) {
		fromDockerfile = append(fromDockerfile, m[1])
	}
	require.NotEmpty(t, fromDockerfile, "found no COPY … /yoloai/bin/ lines — has the Dockerfile changed shape?")

	var fromTable []string
	for _, f := range baseBuildInputs() {
		if f.runtimeBin {
			fromTable = append(fromTable, f.name)
		}
	}

	sort.Strings(fromDockerfile)
	sort.Strings(fromTable)
	assert.Equal(t, fromDockerfile, fromTable,
		"every file the Dockerfile puts in /yoloai/bin must be delivered at launch, and nothing else")
}

// TestBaseBuildInputs_ExecutableSetMatchesTheDockerfile guards the other half of
// the same contract. The bake gets its modes from the Dockerfile's `chmod +x`;
// the mount gets them from guestMode, with no Dockerfile to fix them up. A script
// that loses its exec bit only under the mount is a launch failure on one path
// and not the other.
func TestBaseBuildInputs_ExecutableSetMatchesTheDockerfile(t *testing.T) {
	chmodRe := regexp.MustCompile(`(?s)RUN chmod \+x (.*?)\\\n`)
	m := chmodRe.FindStringSubmatch(string(embeddedDockerfile))
	require.Len(t, m, 2, "could not find the Dockerfile's `RUN chmod +x` line")

	binRe := regexp.MustCompile(`/yoloai/bin/(\S+)`)
	var execInDockerfile []string
	for _, mm := range binRe.FindAllStringSubmatch(m[1], -1) {
		execInDockerfile = append(execInDockerfile, mm[1])
	}

	var execInTable []string
	for _, f := range baseBuildInputs() {
		if f.runtimeBin && f.guestMode&0o111 != 0 {
			execInTable = append(execInTable, f.name)
		}
	}

	sort.Strings(execInDockerfile)
	sort.Strings(execInTable)
	assert.Equal(t, execInDockerfile, execInTable,
		"guestMode's exec bit must match the Dockerfile's chmod +x, or bake and mount disagree")
}

func TestWriteRuntimeScripts_WritesEveryBinScriptWithGuestModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin") // not pre-created: the call must mkdir
	require.NoError(t, WriteRuntimeScripts(dir))

	wrote := 0
	for _, f := range baseBuildInputs() {
		path := filepath.Join(dir, f.name)
		if !f.runtimeBin {
			_, err := os.Stat(path)
			assert.Error(t, err, "%s does not live in /yoloai/bin and must not be delivered there", f.name)
			continue
		}
		wrote++
		info, err := os.Stat(path)
		require.NoError(t, err, "%s must be delivered: the host drives it by absolute path", f.name)
		assert.Equal(t, f.guestMode, info.Mode().Perm(), "%s has the wrong mode", f.name)

		content, err := os.ReadFile(path) //nolint:gosec // test-controlled path
		require.NoError(t, err)
		assert.Equal(t, f.content, content, "%s must be this binary's copy, byte for byte", f.name)
	}
	assert.Greater(t, wrote, 1, "sanity: the delivered set should not be empty or a single file")
}

// TestWriteRuntimeScripts_RepairsAModeLeftByAnOlderBinary covers why the chmod
// follows the write. os.WriteFile applies its mode only when creating the file,
// so a copy left by an earlier yoloAI keeps whatever mode it had — and an
// executable that lost its bit fails the launch rather than degrading.
func TestWriteRuntimeScripts_RepairsAModeLeftByAnOlderBinary(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "install-firewall.py")
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o600))

	require.NoError(t, WriteRuntimeScripts(dir))

	info, err := os.Stat(stale)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "a pre-existing file's mode must be repaired, not inherited")
}

func TestSidecarBinds_RendersReadOnly(t *testing.T) {
	got := sidecarBinds([]runtime.MountSpec{
		{HostPath: "/h/bin", ContainerPath: "/yoloai/bin", ReadOnly: true},
		{HostPath: "/h/rw", ContainerPath: "/yoloai/rw"},
	})
	assert.Equal(t, []string{"/h/bin:/yoloai/bin:ro", "/h/rw:/yoloai/rw"}, got)
}

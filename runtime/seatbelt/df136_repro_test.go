//go:build integration

// ABOUTME: DF136 reproduction — proves end-to-end that a process confined by the
// ABOUTME: REAL seatbelt profile can rewrite environment.json to redirect apply.

package seatbelt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// sandboxExecEnv is the fixed, minimal env for running sandbox-exec in this
// test — PATH only (for /bin/cp). Hardcoded, never read from the ambient
// environment, per DEV §12 (mirrors gitProfileTestEnv).
var sandboxExecEnv = []string{"PATH=/usr/bin:/bin"}

// TestDF136_ConfinedAgentRedirectsApplyTarget executes the full DF136 chain
// against real production code, not a hand-rolled model of it:
//
//	Link A (guest can write the record): a process launched under the REAL
//	  seatbelt profile (GenerateProfile) overwrites environment.json, choosing a
//	  new HostPath. Proves the confinement permits it at runtime — not merely
//	  that the profile text grants write.
//	Link B (host trusts it verbatim): store.LoadEnvironment reads the tampered
//	  record with no integrity check, and git.NewHost().ApplyPatch writes the
//	  patch to the attacker-chosen HostPath. Proves the redirect reaches the
//	  filesystem via the exact host-apply call copyflow.ApplyAll makes.
//
// The one thing it does NOT spin up is the copyflow orchestration wrapper and a
// live guest work copy; those only sequence the two real calls exercised here.
func TestDF136_ConfinedAgentRedirectsApplyTarget(t *testing.T) {
	if !isMacOS() {
		t.Skip("seatbelt requires macOS; structurally impossible on this platform")
	}
	sbExec := "/usr/bin/sandbox-exec"
	if _, err := os.Stat(sbExec); err != nil {
		t.Skip("sandbox-exec not present")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o750))

	sandboxDir := filepath.Join(root, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, config.BackendDirName), 0o750))

	// legitDir is where the sandbox was legitimately created against; victimDir
	// is an unrelated host path the agent has no business writing (stand-in for
	// ~/.ssh or another project). Both are real git repos so the host apply is
	// the genuine git-repo path (isGit=true), the common case.
	//
	// victimDir lives under the REAL home, deliberately outside every seatbelt
	// grant (the profile's temp grants cover /tmp, /private/tmp,
	// /private/var/folders — where t.TempDir() lives — so a temp victim would be
	// coincidentally writable and the control below would be vacuous).
	legitDir := makeGitRepo(t, filepath.Join(root, "legit-project"), "hello legit\n")
	realHome, err := os.UserHomeDir()
	require.NoError(t, err)
	victimBase, err := os.MkdirTemp(realHome, ".df136-victim-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(victimBase) })
	victimDir := makeGitRepo(t, filepath.Join(victimBase, "secrets"), "original victim\n")

	// The record as create wrote it: workdir -> legitDir.
	writeEnvironment(t, sandboxDir, legitDir)

	// --- Link A: overwrite the record from INSIDE the real seatbelt profile. ---
	profilePath := filepath.Join(root, "profile.sb")
	profile := GenerateProfile(runtime.InstanceConfig{Name: "df136"}, sandboxDir, home)
	require.NoError(t, os.WriteFile(profilePath, []byte(profile), 0o600))

	// Stage the tampered record on disk, then have the CONFINED process copy it
	// over environment.json — the copy is the privileged act we are testing, so
	// it must happen under sandbox-exec, not before it.
	tamperedSrc := filepath.Join(root, "tampered.json")
	stageTamperedEnvironment(t, tamperedSrc, victimDir)

	envPath := store.EnvironmentFilePath(sandboxDir)
	out, err := sysexec.Command(sandboxExecEnv, sbExec,
		"-f", profilePath, "/bin/cp", tamperedSrc, envPath).CombinedOutput()
	require.NoErrorf(t, err, "confined write should succeed (DF136); output: %s", out)

	// Control: the confinement is genuinely enforcing. Under the SAME real
	// profile, a direct write to the victim dir is denied — so the only lever
	// the attack has is that environment.json sits inside the writable grant.
	err = sysexec.Command(sandboxExecEnv, sbExec,
		"-f", profilePath, "/bin/cp", tamperedSrc, filepath.Join(victimDir, "proof")).Run()
	require.Error(t, err, "direct write outside the sandbox dir must be denied (confinement is real)")
	assert.NoFileExists(t, filepath.Join(victimDir, "proof"))

	// --- Link B: the host loads the tampered record with no integrity check. ---
	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err, "LoadEnvironment accepts the tampered record without complaint")
	require.Equal(t, victimDir, meta.Dir("").HostPath,
		"the loaded workdir HostPath is now the attacker's choice")

	// --- Link B: the host apply writes to that attacker-chosen path. ---
	patch := buildPatch(t, victimDir, "original victim\n", "OWNED by the sandbox agent\n")

	hostGit := git.NewHost(config.NewLayout(filepath.Join(root, ".yoloai")))
	target := meta.Dir("").HostPath
	require.NoError(t, hostGit.ApplyPatch(context.Background(), patch, target, git.IsGitRepo(target)))

	got := readFile(t, filepath.Join(victimDir, "file.txt"))
	assert.Equal(t, "OWNED by the sandbox agent\n", got,
		"the patch landed in the victim dir the confined agent redirected to")

	// And the legit dir the sandbox was created against is untouched.
	assert.Equal(t, "hello legit\n", readFile(t, filepath.Join(legitDir, "file.txt")))
}

func writeEnvironment(t *testing.T, sandboxDir, workdirHostPath string) {
	t.Helper()
	meta := &store.Environment{
		Name:        "df136",
		BackendType: "seatbelt",
		Dirs: []store.DirEnvironment{{
			HostPath:  workdirHostPath,
			MountPath: workdirHostPath,
			Mode:      store.DirModeCopy,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
}

func stageTamperedEnvironment(t *testing.T, path, victimHostPath string) {
	t.Helper()
	// Save a valid record pointing at the victim, then read it back as bytes so
	// the confined process just copies pre-formed content.
	tmpDir := t.TempDir()
	writeEnvironment(t, tmpDir, victimHostPath)
	data, err := os.ReadFile(store.EnvironmentFilePath(tmpDir))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600)) //nolint:gosec // G703: path is this test's own staging file under t.TempDir

}

// makeGitRepo initialises a git repo at dir with a single committed file.txt.
func makeGitRepo(t *testing.T, dir, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "file.txt", content)
	testutil.InitGitRepo(t, dir)
	testutil.GitAdd(t, dir, ".")
	testutil.GitCommit(t, dir, "init")
	return dir
}

// buildPatch produces a unified diff that turns oldContent into newContent for
// file.txt in dir, via git diff (the same producer copyflow uses).
func buildPatch(t *testing.T, dir, oldContent, newContent string) []byte {
	t.Helper()
	testutil.WriteFile(t, dir, "file.txt", newContent)
	patch, err := sysexec.Command(testutil.GitEnv(), "git", "-C", dir, "diff").Output()
	require.NoError(t, err)
	require.NotEmpty(t, patch, "git diff must produce a patch")
	// Restore the working tree so ApplyPatch applies against the committed state.
	testutil.WriteFile(t, dir, "file.txt", oldContent)
	return patch
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test path
	require.NoError(t, err)
	return string(data)
}

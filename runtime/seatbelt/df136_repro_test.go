//go:build integration

// ABOUTME: DF136 regression guard — proves end-to-end that a process confined by
// ABOUTME: the REAL seatbelt profile can no longer rewrite the environment record.

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

// TestDF136_ConfinedAgentCannotRedirectApplyTarget was the DF136 reproduction and
// is now its regression guard: same chain, same real production code, opposite
// verdict at link A.
//
//	Link A (was: guest can write the record): a process under the REAL seatbelt
//	  profile tries to overwrite the environment record and is now DENIED by the
//	  host-tier deny (writeProfileHostTierDeny). Until 2026-07-30 this write
//	  succeeded, which is what the finding was.
//	Link B (host trusts the record verbatim): unchanged and still true —
//	  store.LoadEnvironment performs no integrity check. That is deliberately
//	  still exercised, because it is the reason link A has to hold: the host will
//	  act on whatever the record says, so the whole defence is that the record
//	  cannot be edited. The apply now lands in the legit dir and the victim dir is
//	  untouched.
//
// Two controls keep the pass honest, since "everything is denied" would satisfy
// link A on its own: a write outside the sandbox dir must still be denied
// (confinement is real), and a write to a non-host-tier path inside the sandbox
// dir must still succeed (the deny is scoped to the tier, and sandbox-exec is
// not simply broken).
//
// The one thing it does NOT spin up is the copyflow orchestration wrapper and a
// live guest work copy; those only sequence the two real calls exercised here.
func TestDF136_ConfinedAgentCannotRedirectApplyTarget(t *testing.T) {
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
	before := readFile(t, envPath)
	out, err := sysexec.Command(sandboxExecEnv, sbExec,
		"-f", profilePath, "/bin/cp", tamperedSrc, envPath).CombinedOutput()
	require.Errorf(t, err, "the confined write into the host tier must be DENIED (DF136); output: %s", out)
	assert.Equal(t, before, readFile(t, envPath), "the record must be byte-identical after the denied write")

	// Control 1: the confinement is genuinely enforcing. Under the SAME real
	// profile, a direct write to the victim dir is denied.
	err = sysexec.Command(sandboxExecEnv, sbExec,
		"-f", profilePath, "/bin/cp", tamperedSrc, filepath.Join(victimDir, "proof")).Run()
	require.Error(t, err, "direct write outside the sandbox dir must be denied (confinement is real)")
	assert.NoFileExists(t, filepath.Join(victimDir, "proof"))

	// Control 2: the deny is scoped to the tier, not to the sandbox dir. Without
	// this, a profile that denied everything would satisfy the assertion above.
	allowed := filepath.Join(sandboxDir, "agent-status.json")
	err = sysexec.Command(sandboxExecEnv, sbExec,
		"-f", profilePath, "/bin/cp", tamperedSrc, allowed).Run()
	require.NoError(t, err, "the rest of the sandbox dir must stay writable — the deny covers host/ only")

	// --- Link B: the host still trusts the record verbatim, which is WHY link A
	// has to hold. LoadEnvironment has no integrity check; it simply reads what
	// is there — and what is there is still the legitimate path.
	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	require.Equal(t, legitDir, meta.Dir("").HostPath,
		"the loaded workdir HostPath must be the one create wrote, not the attacker's")

	// --- Link B: the host apply therefore lands where it should. ---
	patch := buildPatch(t, legitDir, "hello legit\n", "applied to the legit dir\n")

	hostGit := git.NewHost(config.NewLayout(filepath.Join(root, ".yoloai")))
	target := meta.Dir("").HostPath
	require.NoError(t, hostGit.ApplyPatch(context.Background(), patch, target, git.IsGitRepo(target)))

	assert.Equal(t, "applied to the legit dir\n", readFile(t, filepath.Join(legitDir, "file.txt")),
		"the patch landed in the dir the sandbox was created against")
	assert.Equal(t, "original victim\n", readFile(t, filepath.Join(victimDir, "file.txt")),
		"the victim dir the attack aimed at is untouched")
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

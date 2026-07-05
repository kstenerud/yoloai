package seatbelt

// ABOUTME: Tests for the dedicated tight git SBPL profile (audit C1 / confine-host-side-git):
// ABOUTME: pure content assertions + a macOS behavioral battery proving containment.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// TestGenerateGitProfile asserts the security-critical structure of the git
// profile without executing anything, so it runs on every platform in `make
// check`. The behavioral guarantees are exercised by TestGitProfile_Containment.
func TestGenerateGitProfile(t *testing.T) {
	const workCopy = "/Users/x/.yoloai/sandboxes/box/work/enc"
	const home = "/Users/x"
	profile := GenerateGitProfile(workCopy, home, "/Applications/Xcode.app/Contents/Developer")

	must := func(needle, why string) {
		if !strings.Contains(profile, needle) {
			t.Errorf("git profile missing %q (%s)\n---\n%s", needle, why, profile)
		}
	}
	mustNot := func(needle, why string) {
		if strings.Contains(profile, needle) {
			t.Errorf("git profile must NOT contain %q (%s)\n---\n%s", needle, why, profile)
		}
	}

	must("(deny default)", "fail-closed baseline")
	// mach-lookup is the primary escape vector — it must stay denied (no allow).
	mustNot("(allow mach-lookup", "mach-lookup must remain denied — the primary escape vector")
	mustNot("(allow network", "the git profile grants no network (netpolicy governs egress)")

	// Write is scoped to the work copy ONLY — the containment boundary. No broad
	// temp write (that would let a malicious filter drop a marker off-tree).
	must(`(allow file-read* file-write* (subpath "`+workCopy+`"))`, "work copy must be writable")
	mustNot(`file-write* (subpath "/tmp")`, "no broad /tmp write")
	mustNot(`file-write* (subpath "/private/tmp")`, "no broad temp write")
	mustNot(`file-write* (subpath "/private/var/folders")`, "no per-user temp write")

	// process-exec is confined to tool dirs; the work copy must NOT be among them
	// (else a payload dropped in-tree could run).
	must("(allow process-exec", "filters need to exec tool binaries")
	_, afterExec, _ := strings.Cut(profile, "(allow process-exec")
	execBlock, _, _ := strings.Cut(afterExec, ")\n\n")
	if strings.Contains(execBlock, workCopy) {
		t.Error("process-exec must not include the work copy (a dropped payload could run)")
	}
	must(`(subpath "/opt/homebrew")`, "git-lfs / filter tools live under the Homebrew prefix")
	must(`(subpath "/Applications/Xcode.app/Contents/Developer")`, "toolchain git-core exec dir")
}

// gitProfileTestEnv is a fixed, minimal env for the behavioral battery: a PATH
// covering the toolchain (/usr/bin: git, xcrun, sed), the shell (/bin), and the
// arm64 Homebrew prefix (git-lfs), plus a HOME. Hardcoded rather than read from
// the ambient environment (which the repo forbids in tests, §12).
func gitProfileTestEnv(home string) []string {
	return []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin", "HOME=" + home}
}

// gitOutcome captures one sandbox-exec'd git op.
type gitOutcome struct {
	stdout string
	code   int
}

// runConfinedGit runs `sandbox-exec -f <profile> <realGit> -C <repo> <args>` the
// same way Runtime.GitExec does, returning stdout + exit code.
func runConfinedGit(t *testing.T, env []string, profilePath, gitBin, repo string, args ...string) gitOutcome {
	t.Helper()
	full := []string{"-f", profilePath, gitBin, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", repo}
	full = append(full, args...)
	out, err := sysexec.Command(env, "/usr/bin/sandbox-exec", full...).Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // concrete type
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("sandbox-exec git %v: %v", args, err)
	}
	return gitOutcome{stdout: string(out), code: code}
}

// setupGit runs an unconfined git command during test setup, failing on error.
func setupGit(t *testing.T, gitBin string, env []string, dir string, args ...string) {
	t.Helper()
	if out, err := sysexec.Command(env, gitBin, append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("setup git %v: %v\n%s", args, err, out)
	}
}

// TestGitProfile_Containment is the behavioral battery from confine-host-side-git-macos-build.md:
// under the dedicated git profile, legitimate filters (a clean filter and, when
// available, git-lfs) round-trip correctly, while a malicious filter cannot write
// outside the work copy or exec a payload outside the tool dirs. macOS-only.
func TestGitProfile_Containment(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("git SBPL profile is macOS-only")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not present")
	}
	home := t.TempDir()
	env := gitProfileTestEnv(home)
	gitBin, toolchainDir := resolveGitBinary(context.Background(), env)
	if gitBin == "" || gitBin == "git" {
		t.Skip("could not resolve a real git binary")
	}

	repo := t.TempDir()
	setupGit(t, gitBin, env, repo, "init", "-q")
	setupGit(t, gitBin, env, repo, "config", "user.email", "t@t.co")
	setupGit(t, gitBin, env, repo, "config", "user.name", "t")
	setupGit(t, gitBin, env, repo, "config", "filter.redact.clean", "sed 's/SECRET/REDACTED/g'")
	writeFile(t, filepath.Join(repo, ".gitattributes"), "secret.txt filter=redact\n")
	writeFile(t, filepath.Join(repo, "secret.txt"), "hello\nSECRET\n")
	setupGit(t, gitBin, env, repo, "add", "-A")
	setupGit(t, gitBin, env, repo, "commit", "-qm", "baseline")

	profilePath := filepath.Join(t.TempDir(), "git.sb")
	writeFile(t, profilePath, GenerateGitProfile(repo, home, toolchainDir))

	t.Run("legit clean filter round-trips", func(t *testing.T) {
		assertLegitCleanFilter(t, env, profilePath, gitBin, repo)
	})
	t.Run("core ops succeed", func(t *testing.T) {
		assertCoreOps(t, env, profilePath, gitBin, repo)
	})
	t.Run("legit git-lfs round-trips", func(t *testing.T) {
		assertLFSRoundTrip(t, gitBin, env, home, toolchainDir)
	})
	t.Run("malicious filter is contained", func(t *testing.T) {
		assertMaliciousContained(t, gitBin, env, profilePath, repo)
	})
}

// assertLegitCleanFilter proves a normal clean filter runs under the profile: the
// staged side is normalized (SECRET → REDACTED), so the diff reflects it.
func assertLegitCleanFilter(t *testing.T, env []string, profilePath, gitBin, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "secret.txt"), "hello2\nSECRET\n")
	if o := runConfinedGit(t, env, profilePath, gitBin, repo, "add", "-A"); o.code != 0 {
		t.Fatalf("add under profile failed: exit %d", o.code)
	}
	o := runConfinedGit(t, env, profilePath, gitBin, repo, "diff", "--cached")
	if o.code != 0 {
		t.Fatalf("diff under profile failed: exit %d", o.code)
	}
	if !strings.Contains(o.stdout, "REDACTED") {
		t.Errorf("clean filter did not run under profile; diff:\n%s", o.stdout)
	}
}

// assertCoreOps proves the diff-path git ops all succeed under the profile.
func assertCoreOps(t *testing.T, env []string, profilePath, gitBin, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"status", "--porcelain"},
		{"format-patch", "--stdout", "-1"},
		{"diff", "--binary", "HEAD"},
	} {
		if o := runConfinedGit(t, env, profilePath, gitBin, repo, args...); o.code != 0 {
			t.Errorf("git %v under profile failed: exit %d", args, o.code)
		}
	}
}

// assertLFSRoundTrip proves a real Git LFS repo diffs correctly under the profile
// (the git-lfs clean filter runs and emits the LFS pointer). Skips without git-lfs.
func assertLFSRoundTrip(t *testing.T, gitBin string, env []string, home, toolchainDir string) {
	t.Helper()
	if _, err := exec.LookPath("git-lfs"); err != nil {
		t.Skip("git-lfs not installed")
	}
	lfs := t.TempDir()
	setupGit(t, gitBin, env, lfs, "init", "-q")
	setupGit(t, gitBin, env, lfs, "config", "user.email", "t@t.co")
	setupGit(t, gitBin, env, lfs, "config", "user.name", "t")
	setupGit(t, gitBin, env, lfs, "lfs", "install", "--local")
	setupGit(t, gitBin, env, lfs, "lfs", "track", "*.bin")
	writeFile(t, filepath.Join(lfs, "big.bin"), strings.Repeat("A", 2048))
	setupGit(t, gitBin, env, lfs, "add", "-A")
	setupGit(t, gitBin, env, lfs, "commit", "-qm", "lfs baseline")

	profilePath := filepath.Join(t.TempDir(), "lfs.sb")
	writeFile(t, profilePath, GenerateGitProfile(lfs, home, toolchainDir))
	writeFile(t, filepath.Join(lfs, "big.bin"), strings.Repeat("B", 4096))

	if o := runConfinedGit(t, env, profilePath, gitBin, lfs, "add", "-A"); o.code != 0 {
		t.Fatalf("lfs add under profile failed: exit %d", o.code)
	}
	o := runConfinedGit(t, env, profilePath, gitBin, lfs, "diff", "--cached")
	if o.code != 0 {
		t.Fatalf("lfs diff under profile failed: exit %d", o.code)
	}
	if !strings.Contains(o.stdout, "git-lfs") || !strings.Contains(o.stdout, "size 4096") {
		t.Errorf("git-lfs clean filter did not run under profile; diff:\n%s", o.stdout)
	}
}

// assertMaliciousContained proves a malicious clean filter cannot escape: it may
// not write outside the work copy nor exec a dropped payload, though git itself
// still completes.
func assertMaliciousContained(t *testing.T, gitBin string, env []string, profilePath, repo string) {
	t.Helper()
	outside := t.TempDir()
	marker := filepath.Join(outside, "PWNED")
	execEscape := filepath.Join(outside, "EXEC_RAN")
	drop := filepath.Join(repo, "payload.sh")
	writeFile(t, drop, "#!/bin/sh\necho ran > "+execEscape+"\n")
	_ = os.Chmod(drop, 0700) //nolint:gosec // the test payload must be executable to prove exec is denied

	setupGit(t, gitBin, env, repo, "config", "filter.pwn.clean",
		"sh -c 'touch "+marker+" 2>/dev/null; "+drop+" 2>/dev/null; cat'")
	writeFile(t, filepath.Join(repo, ".gitattributes"), "secret.txt filter=redact\nevil.txt filter=pwn\n")
	writeFile(t, filepath.Join(repo, "evil.txt"), "payload\n")

	o := runConfinedGit(t, env, profilePath, gitBin, repo, "add", "-A")
	if o.code != 0 {
		t.Fatalf("git add should still complete: exit %d", o.code)
	}
	// Prove the pwn filter actually FIRED (so the marker's absence is containment,
	// not a no-op): evil.txt is tagged filter=pwn and must have been staged.
	if staged := runConfinedGit(t, env, profilePath, gitBin, repo, "diff", "--cached", "--name-only"); !strings.Contains(staged.stdout, "evil.txt") {
		t.Fatalf("evil.txt was not staged — the malicious filter never ran, so containment is untested; staged:\n%s", staged.stdout)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("(a) malicious filter wrote a marker OUTSIDE the work copy — containment breached")
	}
	if _, err := os.Stat(execEscape); err == nil {
		t.Error("(b) malicious filter exec'd a payload outside the tool dirs — containment breached")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

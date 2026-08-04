// ABOUTME: Tests for the guest-view refresh — host→guest path mapping, verdict parsing, and
// ABOUTME: the across-invocation retry that a single-process loop provably cannot substitute.

package tart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuestPathFor(t *testing.T) {
	sandboxDir := "/lib/sandboxes/box"
	tests := []struct {
		name     string
		hostPath string
		want     string
		wantErr  string
	}{
		{
			name:     "read-write tier maps under the share root",
			hostPath: "/lib/sandboxes/box/rw/files/payload.txt",
			want:     "/Volumes/My Shared Files/rw/files/payload.txt",
		},
		{
			name:     "read-only tier maps too — a :ro share is just as stale",
			hostPath: "/lib/sandboxes/box/ro/seed.json",
			want:     "/Volumes/My Shared Files/ro/seed.json",
		},
		{
			name:     "a name with spaces survives, because the share root has them anyway",
			hostPath: "/lib/sandboxes/box/rw/files/my notes.md",
			want:     "/Volumes/My Shared Files/rw/files/my notes.md",
		},
		{
			// The host-only part of the sandbox dir has no --dir and so has no
			// guest path at all (DF136). Silently inventing one would ask the
			// guest to refresh a path it cannot see and read the miss as staleness.
			name:     "the host-only tier has no guest path",
			hostPath: "/lib/sandboxes/box/host/environment.json",
			wantErr:  "not in a tier shared with the guest",
		},
		{
			name:     "a path outside the sandbox directory is refused",
			hostPath: "/etc/passwd",
			wantErr:  "not in a tier shared with the guest",
		},
		{
			name:     "traversal out of a tier is refused rather than normalised back in",
			hostPath: "/lib/sandboxes/box/rw/../../other/secret",
			wantErr:  "not in a tier shared with the guest",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := guestPathFor(sandboxDir, tc.hostPath)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseRefreshVerdicts(t *testing.T) {
	// Every guest path contains spaces ("/Volumes/My Shared Files/..."), so the
	// separator has to be the tab: splitting on whitespace truncates all of them
	// to "/Volumes/My", which would then match no pending entry and report every
	// file unrepairable.
	stdout := "OK\t/Volumes/My Shared Files/rw/files/a.txt\n" +
		"STALE\t/Volumes/My Shared Files/rw/files/b b.txt\r\n" +
		"ERR:[Errno 2] No such file\t/Volumes/My Shared Files/rw/files/c.txt\n" +
		"\n" +
		"garbage-with-no-tab\n"

	got := parseRefreshVerdicts(stdout)
	assert.Equal(t, "OK", got["/Volumes/My Shared Files/rw/files/a.txt"])
	assert.Equal(t, "STALE", got["/Volumes/My Shared Files/rw/files/b b.txt"])
	assert.Equal(t, "ERR:[Errno 2] No such file", got["/Volumes/My Shared Files/rw/files/c.txt"])
	assert.Len(t, got, 3, "blank and separator-less lines contribute nothing")
}

// fakeRefreshTart builds a fake `tart` whose exec of the guest program replies
// with verdictScript, and which records every guest-program invocation's argv.
// `tart exec <vm> true` (the isRunning probe) succeeds without being recorded.
func fakeRefreshTart(t *testing.T, verdictScript string) (rt *Runtime, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv.log")
	script := `#!/bin/sh
if [ "$1" = "exec" ] && [ "$3" = "true" ]; then exit 0; fi
if [ "$1" = "exec" ]; then
  shift 3            # drop: exec <vm> /usr/bin/python3
  shift              # drop: -c
  prog="$1"; shift   # drop: the program text
  { printf '%s\n' "INVOCATION"; for a in "$@"; do printf '%s\n' "$a"; done; } >> "` + argvLog + `"
  : "$prog"
` + verdictScript + `
  exit 0
fi
exit 99
`
	fake := filepath.Join(dir, "tart")
	require.NoError(t, os.WriteFile(fake, []byte(script), 0700)) //nolint:gosec // G306: test binary needs execute bit
	return &Runtime{
		tartBin: fake,
		execEnv: []string{"PATH=/usr/bin:/bin"},
		layout:  config.Layout{DataDir: "/lib"}.WithPrincipal(config.CLIPrincipal),
		// Waits are real seconds and there is no revalidation clock here to wait
		// for, so the default is a no-op. Tests that care about the wait install
		// their own recorder.
		settle: func(time.Duration) {},
	}, argvLog
}

func invocationCount(t *testing.T, argvLog string) int {
	t.Helper()
	data, err := os.ReadFile(argvLog) //nolint:gosec // test-controlled path
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(data), "INVOCATION\n")
}

func testDigests(rt *Runtime) []runtime.GuestFileDigest {
	return []runtime.GuestFileDigest{{
		HostPath: filepath.Join(rt.sandboxDirForName("yoloai-cli-box"), "rw", "files", "payload.txt"),
		SHA256:   "abc123",
	}}
}

// A guest that reports STALE once and OK afterwards must be retried in a SECOND
// process, not looped inside the first. Measured on real hardware: a 20-byte file
// grown to 256 KiB stayed wrong after four invalidate passes inside one guest
// process and came back correct after one pass in a fresh process. A refresh that
// only ever invoked the guest once would report that file unrepairable.
func TestRefreshGuestFiles_RetriesInASecondInvocation(t *testing.T) {
	rt, argvLog := fakeRefreshTart(t, `
  n=$(grep -c INVOCATION "`+"$argvlog"+`" 2>/dev/null || echo 0)
  if [ "$n" -le 1 ]; then printf 'STALE\t%s\n' "$2"; else printf 'OK\t%s\n' "$2"; fi`)
	// $argvlog is not expanded by the heredoc above; bind it inside the script.
	patchFakeScript(t, rt, argvLog)

	stale, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)
	assert.Empty(t, stale, "the second invocation repaired it")
	assert.Equal(t, 2, invocationCount(t, argvLog), "exactly one retry, not a single shot and not the full bound")
}

// Each pass invalidates and then re-hashes immediately, and the guest's
// revalidation is a ~1 s quantised tick — so undelayed passes can all land inside
// one interval, see the same pre-tick state, and report STALE for a file that is
// about to be correct. Measured: 2 of 3 replacements failed that way and every one
// read correctly seconds later with no further pass (DF181).
//
// This pins that a wait is requested, and how long for. It cannot show the wait is
// LONG ENOUGH — a fake guest has no revalidation clock to outlast, and asserting
// otherwise here would certify a dead path (D129). df181_timing.sh is what shows
// that, on hardware.
func TestRefreshGuestFiles_WaitsForTheTickBetweenPasses(t *testing.T) {
	rt, argvLog := fakeRefreshTart(t, `  printf 'STALE\t%s\n' "$2"`)
	var waits []time.Duration
	rt.settle = func(d time.Duration) { waits = append(waits, d) }

	_, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)

	assert.Equal(t, guestRefreshAttempts, invocationCount(t, argvLog))
	// One fewer wait than passes: the first costs nothing, so a path the guest
	// already agrees on stays free.
	require.Len(t, waits, guestRefreshAttempts-1, "waits go BETWEEN passes, never before the first")
	for _, d := range waits {
		assert.Equal(t, guestRefreshSettle, d)
		assert.Greater(t, d, time.Second, "a sub-tick wait can land inside the interval it must outlast")
	}
}

// A path the guest already agrees on must not pay the wait at all.
func TestRefreshGuestFiles_CurrentPathWaitsNotAtAll(t *testing.T) {
	rt, _ := fakeRefreshTart(t, `  printf 'OK\t%s\n' "$2"`)
	waited := false
	rt.settle = func(time.Duration) { waited = true }

	_, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)
	assert.False(t, waited, "one check, no repair, no delay")
}

func TestRefreshGuestFiles_GivesUpAfterTheBound(t *testing.T) {
	rt, argvLog := fakeRefreshTart(t, `  printf 'STALE\t%s\n' "$2"`)

	stale, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)
	require.Len(t, stale, 1, "a guest that never agrees must be reported, not retried forever")
	assert.Equal(t, "abc123", stale[0].SHA256)
	assert.Equal(t, guestRefreshAttempts, invocationCount(t, argvLog))
}

// Silence is the one answer that must never read as success: the entire defect is
// that wrong content arrives with no complaint from any layer.
func TestRefreshGuestFiles_NoVerdictIsNotSuccess(t *testing.T) {
	rt, _ := fakeRefreshTart(t, `  : # the guest program prints nothing at all`)

	stale, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)
	assert.Len(t, stale, 1, "a path with no verdict stays pending")
}

func TestRefreshGuestFiles_StopsAtOnceWhenTheGuestIsAlreadyCurrent(t *testing.T) {
	rt, argvLog := fakeRefreshTart(t, `  printf 'OK\t%s\n' "$2"`)

	stale, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)
	assert.Empty(t, stale)
	assert.Equal(t, 1, invocationCount(t, argvLog), "a fresh path costs one check and no repair")
}

// The guest is handed the host's digest and the GUEST path — not the host path,
// which does not exist in the VM. Getting this wrong fails open: every file reads
// as missing, and a bug that reports "nothing to repair" is invisible.
func TestRefreshGuestFiles_ArgvCarriesDigestAndGuestPath(t *testing.T) {
	rt, argvLog := fakeRefreshTart(t, `  printf 'OK\t%s\n' "$2"`)

	_, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.NoError(t, err)

	data, err := os.ReadFile(argvLog) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Equal(t, []string{
		"INVOCATION",
		"abc123",
		"/Volumes/My Shared Files/rw/files/payload.txt",
	}, lines)
}

func TestRefreshGuestFiles_NotRunning(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tart")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0700)) //nolint:gosec // G306: test binary needs execute bit
	rt := &Runtime{tartBin: fake, execEnv: []string{"PATH=/usr/bin:/bin"},
		layout: config.Layout{DataDir: "/lib"}.WithPrincipal(config.CLIPrincipal)}

	_, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", testDigests(rt))
	require.ErrorIs(t, err, runtime.ErrNotRunning)
}

func TestRefreshGuestFiles_NoFilesNeverTouchesTheGuest(t *testing.T) {
	rt, argvLog := fakeRefreshTart(t, `  printf 'OK\t%s\n' "$2"`)

	stale, err := rt.RefreshGuestFiles(context.Background(), "yoloai-cli-box", nil)
	require.NoError(t, err)
	assert.Empty(t, stale)
	assert.Equal(t, 0, invocationCount(t, argvLog))
}

// patchFakeScript substitutes the argv-log path into a fake whose verdict script
// needs to count its own prior invocations.
func patchFakeScript(t *testing.T, rt *Runtime, argvLog string) {
	t.Helper()
	data, err := os.ReadFile(rt.tartBin)
	require.NoError(t, err)
	patched := strings.ReplaceAll(string(data), "$argvlog", argvLog)
	require.NoError(t, os.WriteFile(rt.tartBin, []byte(patched), 0700)) //nolint:gosec // G306: test binary needs execute bit
}

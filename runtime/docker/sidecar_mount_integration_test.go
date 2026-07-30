//go:build integration

// ABOUTME: Integration test that NetnsSidecarSpec.Mounts actually reaches the
// ABOUTME: sidecar container, not merely that sidecarBinds renders it (DF156).
package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
)

// TestRunNetnsSidecar_AppliesSpecMounts pins the wiring, which the unit test on
// sidecarBinds deliberately does not.
//
// sidecarBinds is a pure function; a test on it stays green even if nothing hands
// its result to HostConfig.Binds. That is the exact shape of A15 — a test
// connected to the code but not to the program — and it matters here because the
// firewall installer is delivered to the sidecar by mount now, so a dropped Binds
// would mean the sidecar silently reads whatever the image baked. Only a real
// container can tell the difference.
//
// The sidecar's failure mode is also why this asserts on the error rather than on
// captured output: RunNetnsSidecar returns a non-nil error carrying the container
// logs on any non-zero exit, so `cat` of the mounted file succeeding IS the
// assertion, and its failing brings the reason with it.
func TestRunNetnsSidecar_AppliesSpecMounts(t *testing.T) {
	const name = "yoloai-sidecar-mount-test"
	rt := launchTestInstance(t, name)
	ctx := context.Background()

	// TempDir's 0700 is left as-is: what is under test is whether the bind reaches
	// the container at all, not whether a given uid can read it. The sidecar runs
	// as the image's default user (root), so permissions are not the variable here
	// — production's own 0750/0644 are asserted in WriteRuntimeScripts' unit tests.
	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "probe.txt"), []byte("from-the-mount"), 0o600))

	err := rt.RunNetnsSidecar(ctx, runtime.NetnsSidecarSpec{
		Target: name,
		Argv:   []string{"sh", "-c", `grep -q from-the-mount /mnt/probe/probe.txt`},
		Mounts: []runtime.MountSpec{{
			HostPath:      hostDir,
			ContainerPath: "/mnt/probe",
			ReadOnly:      true,
		}},
	})
	require.NoError(t, err, "spec.Mounts must reach the sidecar container; a dropped Binds leaves it reading only the image")

	// And read-only must actually be read-only, since that is how the mount is
	// declared for /yoloai/bin.
	err = rt.RunNetnsSidecar(ctx, runtime.NetnsSidecarSpec{
		Target: name,
		Argv:   []string{"sh", "-c", `touch /mnt/probe/should-fail`},
		Mounts: []runtime.MountSpec{{
			HostPath:      hostDir,
			ContainerPath: "/mnt/probe",
			ReadOnly:      true,
		}},
	})
	assert.Error(t, err, "a ReadOnly MountSpec must be mounted read-only in the sidecar")
}

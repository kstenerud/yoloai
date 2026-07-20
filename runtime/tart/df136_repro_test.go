//go:build integration

// ABOUTME: DF136 reproduction (tart half) — pins the yoloai-controlled decision
// ABOUTME: that the sandbox dir (home of environment.json) is shared read-WRITE.

package tart

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/runtime"
)

// TestDF136_YoloaiShareIsWritable pins the yoloai-controlled decision at the root
// of DF136 — buildRunArgs mounts the sandbox dir (where environment.json lives)
// into the guest read-WRITE. The contrast mount proves the absence of ":ro" is a
// real choice, not an oversight: buildRunArgs *does* emit ":ro" for a read-only
// mount, so the yoloai share omitting it means the guest may write the record.
//
// The full runtime chain (a booted guest rewriting the shared record, then the
// host applying to the redirected path) was reproduced end-to-end on a real
// yoloai-base VM during the DF136 macOS verification; it is not retained as a
// committed test because it clones/boots a multi-GB VM and needs the curated
// host-env allowlist. This deterministic check is the standing regression guard;
// the seatbelt reproduction exercises the identical (backend-independent)
// host-trust half in full.
func TestDF136_YoloaiShareIsWritable(t *testing.T) {
	if !isMacOS() {
		t.Skip("tart requires macOS")
	}
	r := &Runtime{tartBin: "/usr/local/bin/tart", execEnv: []string{"PATH=/usr/bin:/bin"}}
	sandboxPath := t.TempDir()
	roDir := t.TempDir()

	args := r.buildRunArgs("yoloai-df136", sandboxPath, []runtime.MountSpec{
		{HostPath: roDir, ContainerPath: "/ref", ReadOnly: true},
	})

	// The yoloai share of the record's directory carries NO :ro — writable.
	assert.Contains(t, args, "yoloai:"+sandboxPath,
		"the sandbox dir (home of environment.json) is shared read-write")
	assert.NotContains(t, args, "yoloai:"+sandboxPath+":ro")

	// Control: a read-only mount DOES carry :ro, so the omission above is a
	// deliberate rw grant, not buildRunArgs failing to ever emit :ro.
	assert.Contains(t, args, "m-"+filepath.Base(roDir)+":"+roDir+":ro")
}

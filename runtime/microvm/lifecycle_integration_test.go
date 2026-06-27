//go:build linux && integration

package microvm

// ABOUTME: Integration test for the microvm lifecycle — Create/Start/Exec/Inspect/Stop/Remove
// ABOUTME: against a real KVM VM booted from the golden rootfs. Run: go test -tags integration.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// TestLifecycle_RealVM boots a real microvm from the golden rootfs and exercises
// the full lifecycle. Requires `yoloai system build --backend microvm` to have
// run (golden present) plus /dev/kvm + virtiofsd. Skips otherwise.
func TestLifecycle_RealVM(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("no HOME")
	}
	layout := config.NewLayout(filepath.Join(home, ".yoloai", "library"))
	rt, err := New(context.Background(), layout)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close() //nolint:errcheck // best-effort
	if ready, _ := rt.IsReady(context.Background()); !ready {
		t.Skip("golden rootfs not built; run `yoloai system build --backend microvm`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "from-host.txt"), []byte("hi from host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "microvm-itest"
	inst := runtime.InstanceConfig{
		Name:       name,
		WorkingDir: workdir,
		Mounts:     []runtime.MountSpec{{HostPath: workdir, ContainerPath: workdir}},
	}

	if err := rt.Create(ctx, inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })

	if err := rt.Start(ctx, name); err != nil {
		t.Fatalf("Start: %v", err)
	}

	info, err := rt.Inspect(ctx, name)
	if err != nil || !info.Running {
		t.Fatalf("Inspect: running=%v err=%v", info.Running, err)
	}

	// Exec via QGA.
	res, err := rt.Exec(ctx, name, []string{"/bin/sh", "-c", "echo qga-ok && uname -r"}, "")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Exec: code=%d err=%v out=%q", res.ExitCode, err, res.Stdout)
	}
	if !contains(res.Stdout, "qga-ok") {
		t.Errorf("Exec stdout = %q, want it to contain qga-ok", res.Stdout)
	}

	// Workdir shared via virtiofs: the host file is visible in the guest, and a
	// guest write lands back on the host.
	res, err = rt.Exec(ctx, name, []string{"/bin/sh", "-c",
		"cat " + filepath.Join(workdir, "from-host.txt") + " && echo from-guest > " + filepath.Join(workdir, "from-guest.txt")}, "")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("workdir Exec: code=%d err=%v out=%q", res.ExitCode, err, res.Stdout)
	}
	if !contains(res.Stdout, "hi from host") {
		t.Errorf("guest could not read host workdir file; out=%q", res.Stdout)
	}
	if b, err := os.ReadFile(filepath.Join(workdir, "from-guest.txt")); err != nil || !contains(string(b), "from-guest") {
		t.Errorf("host did not see guest-written file: %q err=%v", b, err)
	}

	if err := rt.Stop(ctx, name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if info, _ := rt.Inspect(ctx, name); info.Running {
		t.Error("instance still running after Stop")
	}
	if err := rt.Remove(ctx, name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := rt.Inspect(ctx, name); err == nil {
		t.Error("Inspect should return ErrNotFound after Remove")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

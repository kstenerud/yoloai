// ABOUTME: White-box tests for SidecarHost's spawn/record/reconcile/reap machinery,
// ABOUTME: driven with a shell stand-in sidecar (no real injector, no Docker).
package broker

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSidecarHost returns a SidecarHost whose "sidecar" is a shell stand-in: it
// prints the handshake line and then blocks forever, so the host observes a live
// process to record, reconcile, and reap — without a real injector or Docker. It
// does not read stdin (the small config write fits the pipe buffer and EOFs on
// close).
func fakeSidecarHost(handshakeAddr string) *SidecarHost {
	script := fmt.Sprintf(`printf '{"addr":%q}\n'; exec sleep 300`, handshakeAddr)
	return &SidecarHost{
		command: func() (string, []string, error) {
			return "/bin/sh", []string{"-c", script}, nil
		},
		env: []string{"PATH=/usr/bin:/bin"},
	}
}

func testSpec(dir string) InjectorSpec {
	return InjectorSpec{
		SandboxDir:  dir,
		BindHost:    "127.0.0.1",
		UpstreamURL: "https://api.anthropic.com",
		Bindings: []BindingConfig{{
			Destination: "api.anthropic.com", Kind: KindHeaderSet, Header: "x-api-key", Secret: "sk-real",
		}},
	}
}

func waitUntilDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still alive", pid)
}

func TestSidecarHost_EnsureSpawnsRecordsAndIsAlive(t *testing.T) {
	dir := t.TempDir()
	host := fakeSidecarHost("172.17.0.1:45678")
	t.Cleanup(func() { _ = host.Stop(context.Background(), dir) })

	addr, err := host.Ensure(context.Background(), testSpec(dir))
	require.NoError(t, err)
	assert.Equal(t, "172.17.0.1:45678", addr)

	rec, err := loadRecord(dir)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, addr, rec.Addr)
	assert.True(t, processAlive(rec.PID), "recorded sidecar is running")
}

func TestSidecarHost_EnsureReusesLiveInjector(t *testing.T) {
	dir := t.TempDir()
	host := fakeSidecarHost("172.17.0.1:1111")
	t.Cleanup(func() { _ = host.Stop(context.Background(), dir) })

	_, err := host.Ensure(context.Background(), testSpec(dir))
	require.NoError(t, err)
	rec1, err := loadRecord(dir)
	require.NoError(t, err)

	_, err = host.Ensure(context.Background(), testSpec(dir))
	require.NoError(t, err)
	rec2, err := loadRecord(dir)
	require.NoError(t, err)

	assert.Equal(t, rec1.PID, rec2.PID, "a live injector is reused, not respawned")
}

func TestSidecarHost_EnsureRespawnsDeadInjector(t *testing.T) {
	dir := t.TempDir()
	host := fakeSidecarHost("172.17.0.1:2222")
	t.Cleanup(func() { _ = host.Stop(context.Background(), dir) })

	_, err := host.Ensure(context.Background(), testSpec(dir))
	require.NoError(t, err)
	rec1, err := loadRecord(dir)
	require.NoError(t, err)

	// Simulate a crash: kill the recorded process out from under the host.
	require.NoError(t, syscall.Kill(rec1.PID, syscall.SIGKILL))
	waitUntilDead(t, rec1.PID)

	_, err = host.Ensure(context.Background(), testSpec(dir))
	require.NoError(t, err)
	rec2, err := loadRecord(dir)
	require.NoError(t, err)

	assert.NotEqual(t, rec1.PID, rec2.PID, "a dead injector is respawned with a fresh PID")
	assert.True(t, processAlive(rec2.PID))
}

func TestSidecarHost_StopKillsAndClearsRecord(t *testing.T) {
	dir := t.TempDir()
	host := fakeSidecarHost("172.17.0.1:3333")

	_, err := host.Ensure(context.Background(), testSpec(dir))
	require.NoError(t, err)
	rec, err := loadRecord(dir)
	require.NoError(t, err)
	pid := rec.PID

	require.NoError(t, host.Stop(context.Background(), dir))

	gone, err := loadRecord(dir)
	require.NoError(t, err)
	assert.Nil(t, gone, "record cleared on stop")
	waitUntilDead(t, pid)
}

func TestHasRecordAndInjectorAlive(t *testing.T) {
	dir := t.TempDir()
	// No record yet.
	assert.False(t, HasRecord(dir))
	assert.False(t, InjectorAlive(dir))

	// A record for a live process (this test process) → brokered + alive.
	require.NoError(t, saveRecord(dir, &InjectorRecord{PID: os.Getpid(), Addr: "127.0.0.1:1"}))
	assert.True(t, HasRecord(dir))
	assert.True(t, InjectorAlive(dir))

	// A record for a dead PID → brokered but not alive (the respawn case).
	require.NoError(t, saveRecord(dir, &InjectorRecord{PID: 2147483646, Addr: "127.0.0.1:1"}))
	assert.True(t, HasRecord(dir))
	assert.False(t, InjectorAlive(dir))
}

func TestRespawnBindPort(t *testing.T) {
	assert.Equal(t, "0", respawnBindPort(nil), "no record -> ephemeral")
	assert.Equal(t, "34621", respawnBindPort(&InjectorRecord{Addr: "172.17.0.1:34621"}),
		"dead record -> reuse its port so the container's base_url stays valid")
	assert.Equal(t, "0", respawnBindPort(&InjectorRecord{Addr: "garbage"}), "unparseable addr -> ephemeral")
}

func TestSidecarHost_StopWithoutRecordIsNoop(t *testing.T) {
	dir := t.TempDir()
	host := fakeSidecarHost("127.0.0.1:9")
	assert.NoError(t, host.Stop(context.Background(), dir))
}

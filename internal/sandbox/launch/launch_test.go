// ABOUTME: Tests for the launch package: secrets-consumed wait, port binding
// ABOUTME: parsing, resource limit parsing, and instance config construction.
package launch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// TestEffectiveSecretsConsumedTimeout verifies the host honors a backend's
// declared wait budget (slow-booting backends raise it so the secrets dir
// isn't removed before the guest reads it) and falls back to the package
// default otherwise.
func TestEffectiveSecretsConsumedTimeout(t *testing.T) {
	assert.Equal(t, secretsConsumedTimeout, effectiveSecretsConsumedTimeout(runtime.BackendDescriptor{}),
		"no backend override → package default")
	assert.Equal(t, 90*time.Second, effectiveSecretsConsumedTimeout(runtime.BackendDescriptor{SecretsConsumedTimeout: 90 * time.Second}),
		"backend-declared cap is honored")
}

// TestWaitForSecretsConsumed_ReturnsWhenMarkerExists verifies the wait
// completes promptly once the marker the in-sandbox entrypoint writes
// appears — the path that lets the host remove the secrets temp dir only
// after the guest has read it.
func TestWaitForSecretsConsumed_ReturnsWhenMarkerExists(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed")
	require.NoError(t, os.WriteFile(marker, nil, 0600))

	start := time.Now()
	waitForSecretsConsumed(marker, 5*time.Second)
	assert.Less(t, time.Since(start), time.Second,
		"should return almost immediately when the marker is already present")
}

// TestWaitForSecretsConsumed_ReturnsWhenMarkerAppears verifies the poll
// observes a marker written after the wait starts (the real ordering: the
// guest boots, reads secrets, then writes the marker while the host polls).
func TestWaitForSecretsConsumed_ReturnsWhenMarkerAppears(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed")

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.WriteFile(marker, nil, 0600)
	}()

	start := time.Now()
	waitForSecretsConsumed(marker, 5*time.Second)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "should have waited for the marker")
	assert.Less(t, elapsed, 2*time.Second, "should return soon after the marker appears")
}

// TestWaitForSecretsConsumed_TimesOut verifies the wait gives up after the
// timeout rather than blocking forever — the safety net that guarantees the
// ephemeral secrets dir is always removed even if a guest never signals.
func TestWaitForSecretsConsumed_TimesOut(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed") // never created

	start := time.Now()
	waitForSecretsConsumed(marker, 250*time.Millisecond)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "should wait out the full timeout")
	assert.Less(t, elapsed, 2*time.Second, "should not block much past the timeout")
}

func TestParsePortBindings_Valid(t *testing.T) {
	mappings, err := parsePortBindings([]string{"3000:3000", "8080:80"})
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	assert.Equal(t, runtime.PortMapping{HostPort: 3000, ContainerPort: 3000, Protocol: "tcp"}, mappings[0])
	assert.Equal(t, runtime.PortMapping{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}, mappings[1])
}

func TestParsePortBindings_Invalid(t *testing.T) {
	_, err := parsePortBindings([]string{"invalid"})
	require.Error(t, err)
}

func TestParsePortBindings_Empty(t *testing.T) {
	mappings, err := parsePortBindings(nil)
	require.NoError(t, err)
	assert.Nil(t, mappings)
}

func TestParseResourceLimits(t *testing.T) {
	tests := []struct {
		name    string
		input   *config.ResourceLimits
		wantCPU int64
		wantMem int64
		wantNil bool
		wantErr bool
	}{
		{
			name:    "both set",
			input:   &config.ResourceLimits{CPUs: "4", Memory: "8g"},
			wantCPU: 4_000_000_000,
			wantMem: 8 * 1024 * 1024 * 1024,
		},
		{
			name:    "cpus only",
			input:   &config.ResourceLimits{CPUs: "2.5"},
			wantCPU: 2_500_000_000,
		},
		{
			name:    "memory only",
			input:   &config.ResourceLimits{Memory: "512m"},
			wantMem: 512 * 1024 * 1024,
		},
		{
			name:    "neither set",
			input:   &config.ResourceLimits{},
			wantNil: true,
		},
		{
			name:    "invalid cpus",
			input:   &config.ResourceLimits{CPUs: "abc"},
			wantErr: true,
		},
		{
			name:    "invalid memory",
			input:   &config.ResourceLimits{Memory: "xyz"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkResourceLimits(t, tt.input, tt.wantCPU, tt.wantMem, tt.wantNil, tt.wantErr)
		})
	}
}

// checkResourceLimits is the assertion body for a single TestParseResourceLimits case.
func checkResourceLimits(t *testing.T, input *config.ResourceLimits, wantCPU, wantMem int64, wantNil, wantErr bool) {
	t.Helper()
	result, err := parseResourceLimits(input)
	if wantErr {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	if wantNil {
		require.Nil(t, result)
		return
	}
	require.NotNil(t, result)
	require.Equal(t, wantCPU, result.NanoCPUs, "NanoCPUs")
	require.Equal(t, wantMem, result.Memory, "Memory")
}

func TestParseMemoryString(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"1g", 1024 * 1024 * 1024, false},
		{"512m", 512 * 1024 * 1024, false},
		{"1024k", 1024 * 1024, false},
		{"1048576b", 1048576, false},
		{"1048576", 1048576, false},        // no suffix = bytes
		{"0.5g", 512 * 1024 * 1024, false}, // fractional
		{"", 0, false},
		{"abc", 0, true},
		{"-1g", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseMemoryString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMemoryString(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestBuildInstanceConfig_RejectsNetworkIsolatedWithGvisor verifies that
// requesting --network-isolated together with --isolation=container-enhanced
// (gVisor) is rejected at sandbox-creation time with a specific, actionable
// error rather than producing a sandbox that lies about being isolated.
//
// gVisor's userspace netstack does not honor in-sandbox iptables rules, so
// the current entrypoint-based enforcement is a no-op there. Until the
// host-side filtering redesign lands (see docs/design/network-isolation.md)
// this combination must fail loudly.
func TestBuildInstanceConfig_RejectsNetworkIsolatedWithGvisor(t *testing.T) {
	st := &state.State{
		Name:        "test",
		Workdir:     &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:       agent.GetAgent("test"),
		NetworkMode: "isolated",
		Isolation:   "container-enhanced",
	}

	_, err := buildInstanceConfig(runtime.BackendDescriptor{Name: "mock", Capabilities: runtime.BackendCaps{NetworkIsolation: true}}, st, nil, nil)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "container-enhanced", "error names the broken isolation mode")
	assert.Contains(t, msg, "gVisor", "error explains why")
	assert.Contains(t, msg, "--isolation=container", "error points at the working alternatives")
}

// TestBuildInstanceConfig_AllowsNetworkIsolatedOnSupportedModes is the
// counterpart: every isolation mode that yoloai claims to support with
// --network-isolated must build a config without error. If a future change
// to IsolationEnforcesInSandboxIptables incorrectly excludes a working mode,
// this test catches the over-rejection.
func TestBuildInstanceConfig_AllowsNetworkIsolatedOnSupportedModes(t *testing.T) {
	supported := []runtime.IsolationMode{"", "container", "container-privileged", "vm", "vm-enhanced"}
	for _, isolation := range supported {
		t.Run("isolation="+string(isolation), func(t *testing.T) {
			st := &state.State{
				Name:        "test",
				Workdir:     &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
				Agent:       agent.GetAgent("test"),
				NetworkMode: "isolated",
				Isolation:   isolation,
			}
			_, err := buildInstanceConfig(runtime.BackendDescriptor{Name: "mock", Capabilities: runtime.BackendCaps{NetworkIsolation: true, OverlayDirs: true, CapAdd: true}}, st, nil, nil)
			require.NoError(t, err)
		})
	}
}

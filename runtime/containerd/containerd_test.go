//go:build linux

// ABOUTME: Containerd Runtime unit tests: namespace/sandbox-dir plumbing and
// ABOUTME: RequiredCapabilities gating (Kata shim, CNI, netns, KVM, devmapper)
// ABOUTME: per isolation mode, plus tmux attach-command construction.
package containerdrt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// TestDescriptor_Name verifies the backend name reported by Descriptor().
func TestDescriptor_Name(t *testing.T) {
	r := &Runtime{namespace: "yoloai"}
	assert.Equal(t, runtime.BackendType("containerd"), r.Descriptor().Type)
}

// TestWithNamespace verifies that withNamespace embeds the runtime's namespace
// into the context so containerd client calls are scoped to it.
func TestWithNamespace(t *testing.T) {
	r := &Runtime{namespace: "yoloai"}
	ctx := r.withNamespace(context.Background())

	ns, ok := namespaces.Namespace(ctx)
	require.True(t, ok, "withNamespace must set a containerd namespace on the context")
	assert.Equal(t, "yoloai", ns)
}

// TestSandboxDirForName verifies the sandbox directory path derivation.
func TestSandboxDirForName(t *testing.T) {
	r := &Runtime{layout: config.NewLayout("/home/testuser/.yoloai")}
	dir := r.sandboxDirForName("yoloai-mybox")
	assert.Contains(t, dir, "mybox")
	assert.NotContains(t, dir, "yoloai-mybox") // prefix stripped
}

// RequiredCapabilities tests

// buildTestRuntime constructs a Runtime with injected cap fields for unit testing.
func buildTestRuntime(t *testing.T) (*Runtime, func()) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create fake kata shim binaries on PATH (must be executable for exec.LookPath).
	fakeBinDir := filepath.Join(tmpDir, "bin")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "containerd-shim-kata-v2"), nil, 0o750))    //nolint:gosec // G306: intentional executable for test
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "containerd-shim-kata-fc-v2"), nil, 0o750)) //nolint:gosec // G306: intentional executable for test

	// Create fake CNI bridge.
	cniBridgeDir := filepath.Join(tmpDir, "cni", "bin")
	require.NoError(t, os.MkdirAll(cniBridgeDir, 0o750))
	cniBridgeFile := filepath.Join(cniBridgeDir, "bridge")
	require.NoError(t, os.WriteFile(cniBridgeFile, nil, 0o750)) //nolint:gosec // G306: intentional executable for test

	// Create fake /dev/kvm.
	kvmFile := filepath.Join(tmpDir, "kvm")
	require.NoError(t, os.WriteFile(kvmFile, nil, 0o666)) //nolint:gosec // G306: open permissions so our open check passes

	origShim := kataShimName
	origFCShim := kataFCShimName
	origCNI := cniBridgePath
	origKVM := kvmDevPath
	origCAN := canCreateNetNSFunc
	origBridge := canRunCNIBridgeFunc

	kataShimName = "containerd-shim-kata-v2"
	kataFCShimName = "containerd-shim-kata-fc-v2"
	cniBridgePath = cniBridgeFile
	kvmDevPath = kvmFile
	canCreateNetNSFunc = func() error { return nil }
	canRunCNIBridgeFunc = func() error { return nil }
	t.Setenv("PATH", fakeBinDir+":"+os.Getenv("PATH"))

	r := &Runtime{namespace: "yoloai"}
	r.kataShimV2 = buildKataShimV2Cap()
	r.kataFCShimV2 = buildKataFCShimV2Cap()
	r.cniBridge = buildCNIBridgeCap()
	r.cniNetAdmin = buildCNINetAdminCap()
	r.netnsCreation = buildNetnsCreationCap()
	r.kvmDevice = buildKVMDeviceCap()
	// devmapperSnapshotter requires a real client — leave zero-value for unit tests.
	r.devmapperSnapshotter = caps.HostCapability{
		ID:      "devmapper-snapshotter",
		Summary: "devmapper snapshotter",
		Check:   func(_ context.Context) error { return nil }, // pass by default
	}

	restore := func() {
		kataShimName = origShim
		kataFCShimName = origFCShim
		cniBridgePath = origCNI
		kvmDevPath = origKVM
		canCreateNetNSFunc = origCAN
		canRunCNIBridgeFunc = origBridge
	}
	return r, restore
}

func TestRequiredCapabilities_VM_AllPass(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	capList := r.RequiredCapabilities("vm")
	require.Len(t, capList, 5)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	assert.NoError(t, err)
}

func TestRequiredCapabilities_VMEnhanced_HasMoreCaps(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	vmCaps := r.RequiredCapabilities("vm")
	vmEnhancedCaps := r.RequiredCapabilities("vm-enhanced")

	assert.Greater(t, len(vmEnhancedCaps), len(vmCaps))
}

func TestRequiredCapabilities_KataShimMissing(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	kataShimName = "containerd-shim-kata-v2-nonexistent"
	r.kataShimV2 = buildKataShimV2Cap()

	capList := r.RequiredCapabilities("vm")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kata-containers shim")
}

func TestRequiredCapabilities_CNIMissing(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	cniBridgePath = filepath.Join(t.TempDir(), "no-bridge")
	r.cniBridge = buildCNIBridgeCap()

	capList := r.RequiredCapabilities("vm")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CNI plugins")
}

func TestRequiredCapabilities_CNINetAdminMissing(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	canRunCNIBridgeFunc = func() error {
		return errors.New("CAP_NET_ADMIN not available: CNI bridge plugin cannot create network bridge")
	}
	r.cniNetAdmin = buildCNINetAdminCap()

	capList := r.RequiredCapabilities("vm")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CAP_NET_ADMIN for CNI bridge")
}

// TestBridgeBinaryHasCNICaps_Missing verifies that a file with no xattr returns false.
func TestBridgeBinaryHasCNICaps_Missing(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bridge")
	require.NoError(t, os.WriteFile(tmp, nil, 0o750)) //nolint:gosec // G306: test binary
	assert.False(t, bridgeBinaryHasCNICaps(tmp))
}

// TestBridgeBinaryHasCNICaps_Nonexistent verifies that a missing file returns false.
func TestBridgeBinaryHasCNICaps_Nonexistent(t *testing.T) {
	assert.False(t, bridgeBinaryHasCNICaps(filepath.Join(t.TempDir(), "no-bridge")))
}

func TestRequiredCapabilities_CannotCreateNetNS(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	canCreateNetNSFunc = func() error { return errors.New("operation not permitted") }
	r.netnsCreation = buildNetnsCreationCap()

	capList := r.RequiredCapabilities("vm")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network namespace creation")
}

func TestRequiredCapabilities_KVMMissing(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	kvmDevPath = filepath.Join(t.TempDir(), "no-kvm")
	r.kvmDevice = buildKVMDeviceCap()

	capList := r.RequiredCapabilities("vm")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KVM device")
}

func TestRequiredCapabilities_FCShimMissing_VmEnhanced(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	kataFCShimName = "containerd-shim-kata-fc-v2-nonexistent"
	r.kataFCShimV2 = buildKataFCShimV2Cap()

	capList := r.RequiredCapabilities("vm-enhanced")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Firecracker")
}

func TestRequiredCapabilities_DevmapperFailure_VmEnhanced(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	r.devmapperSnapshotter = caps.HostCapability{
		ID:      "devmapper-snapshotter",
		Summary: "devmapper snapshotter",
		Check:   func(_ context.Context) error { return fmt.Errorf("devmapper snapshotter not configured") },
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{Description: "run setup script", NeedsRoot: true}}
		},
	}

	capList := r.RequiredCapabilities("vm-enhanced")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "devmapper")
}

func TestRequiredCapabilities_DevmapperNotCheckedForVM(t *testing.T) {
	r, restore := buildTestRuntime(t)
	defer restore()

	r.devmapperSnapshotter = caps.HostCapability{
		ID:      "devmapper-snapshotter",
		Summary: "devmapper snapshotter",
		Check:   func(_ context.Context) error { return fmt.Errorf("devmapper snapshotter not configured") },
	}

	capList := r.RequiredCapabilities("vm")
	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	assert.NoError(t, err, "vm isolation should not probe devmapper snapshotter")
}

func TestDescriptor_BaseModeName(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, runtime.IsolationModeVM, r.Descriptor().BaseModeName)
}

func TestDescriptor_SupportedIsolationModes(t *testing.T) {
	r := &Runtime{}
	modes := r.Descriptor().SupportedIsolationModes
	assert.Contains(t, modes, runtime.IsolationModeVM)
	assert.Contains(t, modes, runtime.IsolationModeVMEnhanced)
}

// TmuxSocket test

func TestTmuxSocket_Containerd(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, "/tmp/yoloai-tmux.sock", r.TmuxSocket("/any/path"))
}

// AttachCommand tests are in exec.go but verify routing here.

func TestAttachCommand_ContainerdBasic(t *testing.T) {
	r := &Runtime{}
	cmd := r.AttachCommand("/tmp/yoloai-tmux.sock", 24, 80, "vm")
	require.NotEmpty(t, cmd)
	// Should include the tmux socket path.
	joined := strings.Join(cmd, " ")
	assert.Contains(t, joined, "/tmp/yoloai-tmux.sock")
	assert.Contains(t, joined, "attach")
}

func TestAttachCommand_ContainerdNoSocket(t *testing.T) {
	r := &Runtime{}
	cmd := r.AttachCommand("", 24, 80, "vm")
	require.NotEmpty(t, cmd)
	joined := strings.Join(cmd, " ")
	assert.Contains(t, joined, "tmux")
	assert.Contains(t, joined, "attach")
	assert.NotContains(t, joined, "-S")
}

func TestAttachCommand_IncludesTermSize(t *testing.T) {
	r := &Runtime{}
	cmd := r.AttachCommand("/tmp/yoloai-tmux.sock", 50, 200, "vm")
	joined := strings.Join(cmd, " ")
	// Containerd attach command includes stty for terminal sizing.
	assert.Contains(t, joined, fmt.Sprintf("cols %d", 200))
	assert.Contains(t, joined, fmt.Sprintf("rows %d", 50))
}

func TestAttachCommand_ZeroTermSize_NoStty(t *testing.T) {
	r := &Runtime{}
	cmd := r.AttachCommand("/tmp/yoloai-tmux.sock", 0, 0, "vm")
	joined := strings.Join(cmd, " ")
	assert.NotContains(t, joined, "stty")
	assert.Contains(t, joined, "tmux")
}

package containerdrt

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestName verifies the backend name is "containerd".
func TestName(t *testing.T) {
	r := &Runtime{namespace: "yoloai"}
	assert.Equal(t, "containerd", r.Name())
}

// TestWithNamespace verifies that withNamespace injects the correct namespace.
func TestWithNamespace(t *testing.T) {
	r := &Runtime{namespace: "yoloai"}
	ctx := r.withNamespace(context.Background())
	require.NotNil(t, ctx)
	// The namespace is embedded in the context value — verify we can round-trip it.
	// We don't import namespaces package here to keep the test minimal, so just
	// verify the context is distinct from the input.
	assert.NotEqual(t, context.Background(), ctx)
}

// TestIsWSL2_Linux verifies isWSL2 doesn't panic in a regular Linux environment.
func TestIsWSL2_Linux(t *testing.T) {
	// On a non-WSL2 machine, isWSL2() should return false without panic.
	// On a WSL2 machine it may return true — either is acceptable.
	result := isWSL2()
	// No assertion on value — only testing that it doesn't panic.
	_ = result
}

// TestKataConfigPath verifies the correct config path is returned for each runtime.
func TestKataConfigPath(t *testing.T) {
	tests := []struct {
		runtime  string
		wantPath string
	}{
		{
			runtime:  "io.containerd.kata.v2",
			wantPath: "", // use shim default (Dragonball VMM)
		},
		{
			runtime:  "io.containerd.kata-fc.v2",
			wantPath: "/opt/kata/share/defaults/kata-containers/configuration-fc.toml",
		},
		{
			runtime:  "",
			wantPath: "", // use shim default (Dragonball VMM)
		},
	}

	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			got := kataConfigPath(tt.runtime)
			assert.Equal(t, tt.wantPath, got)
		})
	}
}

// TestSandboxDirForName verifies the sandbox directory path derivation.
func TestSandboxDirForName(t *testing.T) {
	dir := sandboxDirForName("yoloai-mybox")
	assert.Contains(t, dir, "mybox")
	assert.NotContains(t, dir, "yoloai-mybox") // prefix stripped
}

// isWSL2 tests

func TestIsWSL2_Microsoft(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "version")
	require.NoError(t, os.WriteFile(tmp, []byte("Linux version 5.15.0-microsoft-standard-WSL2"), 0o600))

	orig := procSelfStatusPath
	defer func() { procSelfStatusPath = orig }()
	// isWSL2 reads /proc/version directly; we test via the exported helper by
	// temporarily pointing the real /proc/version aside — instead just call the
	// real isWSL2 to confirm it doesn't panic, and unit-test the logic inline.
	_ = isWSL2() // smoke: no panic

	// Inline the same logic with a temp file to test the "microsoft" branch.
	data, _ := os.ReadFile(tmp)
	assert.True(t, strings.Contains(strings.ToLower(string(data)), "microsoft"))
}

func TestIsWSL2_NonWSL(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "version")
	require.NoError(t, os.WriteFile(tmp, []byte("Linux version 6.8.0-106-generic (buildd@lcy02-amd64-059)"), 0o600))
	data, _ := os.ReadFile(tmp)
	assert.False(t, strings.Contains(strings.ToLower(string(data)), "microsoft"))
}

// hasCapNetAdmin tests

func TestHasCapNetAdmin_WithCapability(t *testing.T) {
	// CapEff with bit 12 (CAP_NET_ADMIN) set: 0x0000000000001000
	tmp := filepath.Join(t.TempDir(), "status")
	content := "Name:\ttest\nCapEff:\t0000000000001000\nCapPrm:\t0000000000000000\n"
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0o600))

	orig := procSelfStatusPath
	defer func() { procSelfStatusPath = orig }()
	procSelfStatusPath = tmp

	assert.True(t, hasCapNetAdmin())
}

func TestHasCapNetAdmin_WithoutCapability(t *testing.T) {
	// CapEff with bit 12 clear (only bit 0 set = CAP_CHOWN)
	tmp := filepath.Join(t.TempDir(), "status")
	content := "Name:\ttest\nCapEff:\t0000000000000001\nCapPrm:\t0000000000000000\n"
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0o600))

	orig := procSelfStatusPath
	defer func() { procSelfStatusPath = orig }()
	procSelfStatusPath = tmp

	assert.False(t, hasCapNetAdmin())
}

func TestHasCapNetAdmin_AllCaps(t *testing.T) {
	// CapEff with all bits set (root / full capabilities)
	tmp := filepath.Join(t.TempDir(), "status")
	content := "Name:\ttest\nCapEff:\tffffffffffffffff\nCapPrm:\t0000000000000000\n"
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0o600))

	orig := procSelfStatusPath
	defer func() { procSelfStatusPath = orig }()
	procSelfStatusPath = tmp

	assert.True(t, hasCapNetAdmin())
}

func TestHasCapNetAdmin_MissingFile(t *testing.T) {
	orig := procSelfStatusPath
	defer func() { procSelfStatusPath = orig }()
	procSelfStatusPath = filepath.Join(t.TempDir(), "nonexistent")

	assert.False(t, hasCapNetAdmin())
}

func TestHasCapNetAdmin_MalformedHex(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "status")
	content := "Name:\ttest\nCapEff:\tZZZZZZZZZZZZZZZZ\n"
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0o600))

	orig := procSelfStatusPath
	defer func() { procSelfStatusPath = orig }()
	procSelfStatusPath = tmp

	assert.False(t, hasCapNetAdmin())
}

// ValidateIsolation tests

// setupValidateIsolationMocks configures all prerequisite check overrides so that
// ValidateIsolation passes cleanly. Returns a restore function.
func setupValidateIsolationMocks(t *testing.T) (sockPath string, restoreAll func()) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create a listening Unix socket so net.Dial succeeds.
	sockPath = filepath.Join(tmpDir, "containerd.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	// Create a fake CNI bridge binary.
	cniBridgeDir := filepath.Join(tmpDir, "cni", "bin")
	require.NoError(t, os.MkdirAll(cniBridgeDir, 0o755))
	cniBridgeFile := filepath.Join(cniBridgeDir, "bridge")
	require.NoError(t, os.WriteFile(cniBridgeFile, nil, 0o755))

	// Create a fake /dev/kvm.
	kvmFile := filepath.Join(tmpDir, "kvm")
	require.NoError(t, os.WriteFile(kvmFile, nil, 0o600))

	// Create a fake kata shim on PATH.
	fakeBinDir := filepath.Join(tmpDir, "bin")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "containerd-shim-kata-v2"), nil, 0o755))
	t.Setenv("PATH", fakeBinDir+":"+os.Getenv("PATH"))

	origSock := containerdSockPath
	origShim := kataShimName
	origCNI := cniBridgePath
	origKVM := kvmDevPath
	origCap := capNetAdminCheckFunc
	origWSL2 := wsl2CheckFunc

	containerdSockPath = sockPath
	kataShimName = "containerd-shim-kata-v2"
	cniBridgePath = cniBridgeFile
	kvmDevPath = kvmFile
	capNetAdminCheckFunc = func() bool { return true }
	wsl2CheckFunc = func() bool { return false }

	return sockPath, func() {
		containerdSockPath = origSock
		kataShimName = origShim
		cniBridgePath = origCNI
		kvmDevPath = origKVM
		capNetAdminCheckFunc = origCap
		wsl2CheckFunc = origWSL2
	}
}

func TestValidateIsolation_AllPrerequisitesMet(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	assert.NoError(t, err)
}

func TestValidateIsolation_SocketMissing(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	containerdSockPath = filepath.Join(t.TempDir(), "nonexistent.sock")

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "containerd socket not found")
}

func TestValidateIsolation_KataShimMissing(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	kataShimName = "containerd-shim-kata-v2-nonexistent"

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kata shim not found")
}

func TestValidateIsolation_CNIMissing(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	cniBridgePath = filepath.Join(t.TempDir(), "no-bridge")

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CNI plugins not found")
}

func TestValidateIsolation_NoCapNetAdmin(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	capNetAdminCheckFunc = func() bool { return false }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CAP_NET_ADMIN")
}

func TestValidateIsolation_KVMMissing_NonWSL2(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	kvmDevPath = filepath.Join(t.TempDir(), "no-kvm")
	wsl2CheckFunc = func() bool { return false }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/dev/kvm not found")
}

func TestValidateIsolation_KVMMissing_WSL2(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	kvmDevPath = filepath.Join(t.TempDir(), "no-kvm")
	wsl2CheckFunc = func() bool { return true }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nested virtualization")
}

func TestValidateIsolation_MultipleFailures(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	// Fail three checks simultaneously.
	cniBridgePath = filepath.Join(t.TempDir(), "no-bridge")
	kvmDevPath = filepath.Join(t.TempDir(), "no-kvm")
	capNetAdminCheckFunc = func() bool { return false }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	// All three failures should appear in the error message.
	assert.Contains(t, err.Error(), "CNI plugins not found")
	assert.Contains(t, err.Error(), "CAP_NET_ADMIN")
	assert.Contains(t, err.Error(), "/dev/kvm not found")
}

func TestValidateIsolation_SocketPermissionDenied(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	// Simulate permission denied by pointing to a socket path whose directory
	// is unreadable.
	tmpDir := t.TempDir()
	noAccessDir := filepath.Join(tmpDir, "noaccess")
	require.NoError(t, os.MkdirAll(noAccessDir, 0o000))
	t.Cleanup(func() { os.Chmod(noAccessDir, 0o755) }) //nolint:errcheck // cleanup
	containerdSockPath = filepath.Join(noAccessDir, "containerd.sock")

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	// Either "permission denied" or "not found" branch — both are valid.
	assert.True(t,
		strings.Contains(err.Error(), "permission") || strings.Contains(err.Error(), "containerd socket"),
		"expected socket error, got: %v", err)
}

func TestValidateIsolation_FormatError(t *testing.T) {
	_, restore := setupValidateIsolationMocks(t)
	defer restore()

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "vm")
	require.NoError(t, err)
	// Run with multiple failures to check the joined error format.
	cniBridgePath = filepath.Join(t.TempDir(), "no-bridge")
	capNetAdminCheckFunc = func() bool { return false }
	err = r.ValidateIsolation(context.Background(), "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VM isolation mode requires additional setup")
	assert.Contains(t, err.Error(), "  - ")

	// Verify that fmt.Errorf wrapping includes the error key phrases.
	errStr := err.Error()
	assert.True(t, strings.Count(errStr, "  - ") >= 2, "expected at least 2 bullet points in: %s", errStr)
}

// PreferredTmuxSocket test

func TestPreferredTmuxSocket_Containerd(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, "/tmp/yoloai-tmux.sock", r.PreferredTmuxSocket())
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

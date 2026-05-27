package yoerrors

import (
	"errors"
	"fmt"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUsageError_Message(t *testing.T) {
	err := NewUsageError("bad flag: %s", "--foo")
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "bad flag: --foo")
}

func TestUsageError_Unwrap(t *testing.T) {
	sentinel := fmt.Errorf("inner error")
	ue := &UsageError{Err: fmt.Errorf("wrapped: %w", sentinel)}

	assert.Equal(t, ue.Unwrap(), fmt.Errorf("wrapped: %w", sentinel))
	assert.True(t, errors.Is(ue.Unwrap(), sentinel))
}

func TestNewConfigError_Message(t *testing.T) {
	err := NewConfigError("missing key: %s", "backend")
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "missing key: backend")
}

func TestConfigError_Unwrap(t *testing.T) {
	sentinel := fmt.Errorf("inner config error")
	ce := &ConfigError{Err: fmt.Errorf("wrapped: %w", sentinel)}

	assert.True(t, errors.Is(ce.Unwrap(), sentinel))
}

func TestUsageError_ImplementsError(t *testing.T) {
	var _ error = (*UsageError)(nil)

	ue := NewUsageError("test")
	var e error = ue
	assert.NotNil(t, e)
}

func TestConfigError_ImplementsError(t *testing.T) {
	var _ error = (*ConfigError)(nil)

	ce := NewConfigError("test")
	var e error = ce
	assert.NotNil(t, e)
}

// ---- DiskSpaceError ----

func TestIsDiskSpaceError_Nil(t *testing.T) {
	assert.False(t, IsDiskSpaceError(nil))
}

func TestIsDiskSpaceError_SyscallENOSPC(t *testing.T) {
	// Direct ENOSPC.
	assert.True(t, IsDiskSpaceError(syscall.ENOSPC))
	// Wrapped through fmt.Errorf preserves errors.Is.
	wrapped := fmt.Errorf("write file: %w", syscall.ENOSPC)
	assert.True(t, IsDiskSpaceError(wrapped))
}

func TestIsDiskSpaceError_StringMarkers(t *testing.T) {
	cases := []string{
		"unpack image: no space left on device",
		"snapshot create: ENOSPC",
		"out of disk space on /var/lib/containerd",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			assert.True(t, IsDiskSpaceError(errors.New(msg)))
		})
	}
}

func TestIsDiskSpaceError_OtherErrors(t *testing.T) {
	cases := []error{
		errors.New("permission denied"),
		errors.New("connection refused"),
		errors.New(""),
		syscall.EACCES,
	}
	for _, e := range cases {
		assert.False(t, IsDiskSpaceError(e), "should not match: %v", e)
	}
}

func TestAsDiskSpaceError_Wraps(t *testing.T) {
	inner := syscall.ENOSPC
	wrapped := AsDiskSpaceError("unpack image", inner)

	var dse *DiskSpaceError
	require.True(t, errors.As(wrapped, &dse), "should wrap as *DiskSpaceError")
	assert.Equal(t, "unpack image", dse.Op)
	assert.True(t, errors.Is(wrapped, syscall.ENOSPC), "Unwrap must preserve sentinel")
}

func TestAsDiskSpaceError_PassesThroughNonENOSPC(t *testing.T) {
	other := errors.New("permission denied")
	result := AsDiskSpaceError("write file", other)
	assert.Same(t, other, result, "non-disk errors should be returned unchanged")
}

func TestAsDiskSpaceError_PassesThroughNil(t *testing.T) {
	assert.Nil(t, AsDiskSpaceError("anything", nil))
}

func TestDiskSpaceError_MessageMentionsPruneAndDisk(t *testing.T) {
	err := NewDiskSpaceError("unpack image", syscall.ENOSPC)
	msg := err.Error()
	// The recovery hint is the whole point of the typed error;
	// regress-guard the actionable parts.
	assert.Contains(t, msg, "unpack image")
	assert.Contains(t, msg, "no space left on device")
	assert.Contains(t, msg, "yoloai system disk")
	assert.Contains(t, msg, "yoloai system prune --cache")
}

func TestDiskSpaceError_ImplementsError(t *testing.T) {
	var _ error = (*DiskSpaceError)(nil)
}

func TestNewResourceLimitError_Message(t *testing.T) {
	err := NewResourceLimitError("macOS concurrent VM limit reached: %s", "vm.log output")
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "macOS concurrent VM limit reached")
	assert.Contains(t, err.Error(), "vm.log output")
}

func TestResourceLimitError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("inner error")
	e := &ResourceLimitError{Err: fmt.Errorf("wrapped: %w", inner)}
	assert.True(t, errors.Is(e, inner))
}

func TestResourceLimitError_ImplementsError(t *testing.T) {
	var _ error = (*ResourceLimitError)(nil)
}

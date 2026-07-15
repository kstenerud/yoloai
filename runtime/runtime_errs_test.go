// ABOUTME: Error-classification helpers (IsPermissionDenied, IsAddressInUse)
// ABOUTME: across syscall errno and SDK text-only forms, plus ExecError's
// ABOUTME: errors.As reachability (W8) for matching exit codes without strings.
package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsPermissionDenied(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated error", errors.New("not found"), false},
		{"wrapped fs.ErrPermission", fmt.Errorf("open foo: %w", fs.ErrPermission), true},
		{"wrapped syscall.EACCES", fmt.Errorf("connect: %w", syscall.EACCES), true},
		{"sdk text-only", errors.New("rpc error: code = PermissionDenied desc = permission denied"), true},
		{"sdk text without phrase", errors.New("rpc error: code = NotFound"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsPermissionDenied(tt.err))
		})
	}
}

func TestIsAddressInUse(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated error", errors.New("not running"), false},
		{"wrapped syscall.EADDRINUSE", fmt.Errorf("bind: %w", syscall.EADDRINUSE), true},
		{"text 'address in use'", errors.New("address in use"), true},
		{"text 'address already in use'", errors.New("Address already in use"), true},
		{"text 'connection refused' is not the same", errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsAddressInUse(tt.err))
		})
	}
}

func TestExecError_AsTarget(t *testing.T) {
	// ExecError must be reachable via errors.As so callers can match exit
	// codes without string-matching the error message. W8 of the architecture
	// remediation plan.
	inner := &ExecError{ExitCode: 1, Stderr: "diff present"}
	wrapped := fmt.Errorf("git diff failed: %w", inner)

	var target *ExecError
	assert.True(t, errors.As(wrapped, &target))
	assert.Equal(t, 1, target.ExitCode)
	assert.Equal(t, "diff present", target.Stderr)

	// Negative: a non-ExecError should not be matched.
	other := errors.New("regular error")
	var target2 *ExecError
	assert.False(t, errors.As(other, &target2))
}

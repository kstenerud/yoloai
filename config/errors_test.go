package config

import (
	"errors"
	"fmt"
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

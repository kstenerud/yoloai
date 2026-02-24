package cli

import (
	"errors"
	"testing"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveName_ExplicitArg(t *testing.T) {
	name, rest, err := resolveName(nil, []string{"my-sandbox"})
	require.NoError(t, err)
	assert.Equal(t, "my-sandbox", name)
	assert.Empty(t, rest)
}

func TestResolveName_EnvFallback(t *testing.T) {
	t.Setenv("YOLOAI_SANDBOX", "env-sandbox")

	name, rest, err := resolveName(nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "env-sandbox", name)
	assert.Nil(t, rest)
}

func TestResolveName_ExplicitOverridesEnv(t *testing.T) {
	t.Setenv("YOLOAI_SANDBOX", "env-sandbox")

	name, rest, err := resolveName(nil, []string{"explicit"})
	require.NoError(t, err)
	assert.Equal(t, "explicit", name)
	assert.Empty(t, rest)
}

func TestResolveName_NeitherSet(t *testing.T) {
	_, _, err := resolveName(nil, nil)
	require.Error(t, err)

	var usageErr *sandbox.UsageError
	assert.True(t, errors.As(err, &usageErr))
	assert.Contains(t, err.Error(), "YOLOAI_SANDBOX")
}

func TestResolveName_ExtraArgs(t *testing.T) {
	name, rest, err := resolveName(nil, []string{"my-sandbox", "extra1", "extra2"})
	require.NoError(t, err)
	assert.Equal(t, "my-sandbox", name)
	assert.Equal(t, []string{"extra1", "extra2"}, rest)
}

package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDestroyCmd_AllWithNames(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newDestroyCmd()
	cmd.SetArgs([]string{"--all", "mybox"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot specify sandbox names with --all")
}

func TestDestroyCmd_NoArgsNoEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv(EnvSandboxName, "")

	// Without --all and without args, the command needs a runtime to proceed.
	// But with no backend specified, it defaults to "docker" which will fail
	// to connect. The pre-validation (no args, no env → UsageError) happens
	// inside withRuntime's callback. If Docker is unavailable, we get a
	// connection error instead — which is still an error, so we just verify
	// the command doesn't succeed silently.
	cmd := newDestroyCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestDestroyCmd_InvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Invalid names fail at ValidateName inside the runtime callback.
	// Same caveat as above — if Docker is unavailable, we get a different error.
	cmd := newDestroyCmd()
	cmd.SetArgs([]string{"INVALID_NAME!!"})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestDestroyCmd_AllFlagRegistered(t *testing.T) {
	cmd := newDestroyCmd()
	assert.NotNil(t, cmd.Flags().Lookup("all"))
	assert.NotNil(t, cmd.Flags().Lookup("yes"))
}

func TestHasWildcard(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"test*", true},
		{"test?", true},
		{"*test", true},
		{"te*st", true},
		{"te?st", true},
		{"test", false},
		{"test123", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hasWildcard(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ABOUTME: Unit tests for the 'run' command's pure boundary validation:
// ABOUTME: positional parsing (workdir optional, unlike new) and the
// ABOUTME: prompt-required rule. No backend, no daemon.
package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRunCmdPositional(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantErr   string // "" means no error
		wantName  string
		wantWdArg string
	}{
		{name: "no args -> name required", args: nil, wantErr: "sandbox name is required"},
		{name: "name only parses (workdir guard is in runRunCmd, DF49)", args: []string{"box"}, wantName: "box", wantWdArg: ""},
		{name: "name + workdir ok", args: []string{"box", "."}, wantName: "box", wantWdArg: "."},
		{name: "too many positionals", args: []string{"box", "wd", "extra"}, wantErr: "too many positional arguments"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewRunCmd("test")
			name, wdArg, _, _, err := parseRunCmdPositional(cmd, tc.args)
			if tc.wantErr != "" {
				assertUsageError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, name)
			assert.Equal(t, tc.wantWdArg, wdArg)
		})
	}
}

func TestRunCmd_RequiresPrompt(t *testing.T) {
	// run without a prompt is a usage error, surfaced before any backend contact.
	cmd := NewRunCmd("test")
	err := runRunCmd(cmd, []string{"box", "."}, "test")
	assertUsageError(t, err, "requires a prompt")
}

func TestRunCmd_RequiresWorkdir(t *testing.T) {
	// run with a prompt but no workdir is a usage error (DF49: no-workdir mode is
	// deferred), surfaced before any backend contact.
	cmd := NewRunCmd("test")
	require.NoError(t, cmd.Flags().Set("prompt", "do the thing"))
	err := runRunCmd(cmd, []string{"box"}, "test")
	assertUsageError(t, err, "workdir is required")
}

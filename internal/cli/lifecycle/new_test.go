// ABOUTME: Unit tests for the 'new' command's pure usage-error paths: positional
// ABOUTME: arg validation, flag-conflict rejection, and port/env parsing. No
// ABOUTME: backend, no daemon — these exercise the boundary validation only.
package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/yoerrors"
)

func assertUsageError(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "expected a *yoerrors.UsageError, got %T", err)
	assert.Contains(t, err.Error(), wantSubstr)
}

func TestParseNewCmdPositional_Errors(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		profile   string
		wantErr   string // "" means no error
		wantName  string
		wantWdArg string
	}{
		{name: "no args -> name required", args: nil, wantErr: "sandbox name is required"},
		{name: "name only, no profile -> workdir required", args: []string{"box"}, wantErr: "workdir is required"},
		{name: "too many positionals", args: []string{"box", "wd", "extra"}, wantErr: "too many positional arguments"},
		{name: "name + workdir ok", args: []string{"box", "."}, wantName: "box", wantWdArg: "."},
		{name: "name only with profile ok", args: []string{"box"}, profile: "myprof", wantName: "box"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewNewCmd("test")
			if tc.profile != "" {
				require.NoError(t, cmd.Flags().Set("profile", tc.profile))
			}
			name, wdArg, _, _, err := parseNewCmdPositional(cmd, tc.args)
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

func TestResolveNewCmdOptions_FlagConflicts(t *testing.T) {
	t.Run("json + attach incompatible", func(t *testing.T) {
		cmd := NewNewCmd("test")
		// --json is a root persistent flag in production; register one here so
		// cliutil.JSONEnabled finds it.
		cmd.PersistentFlags().Bool("json", false, "")
		require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
		require.NoError(t, cmd.Flags().Set("attach", "true"))

		_, _, err := resolveNewCmdOptions(cmd, "box", ".", nil, "")
		assertUsageError(t, err, "--json and --attach are incompatible")
	})

	t.Run("port + network-none incompatible", func(t *testing.T) {
		cmd := NewNewCmd("test")
		require.NoError(t, cmd.Flags().Set("network-none", "true"))
		require.NoError(t, cmd.Flags().Set("port", "8080:80"))

		_, _, err := resolveNewCmdOptions(cmd, "box", ".", nil, "")
		assertUsageError(t, err, "--port is incompatible with --network-none")
	})
}

func TestParsePortFlags(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		wantErr string
	}{
		{name: "missing colon", in: []string{"8080"}, wantErr: "invalid port format"},
		{name: "non-numeric host", in: []string{"abc:80"}, wantErr: "invalid host port"},
		{name: "non-numeric container", in: []string{"8080:xyz"}, wantErr: "invalid container port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parsePortFlags(tc.in)
			assertUsageError(t, err, tc.wantErr)
		})
	}

	t.Run("valid mapping", func(t *testing.T) {
		t.Parallel()
		ports, err := parsePortFlags([]string{"8080:80"})
		require.NoError(t, err)
		require.Len(t, ports, 1)
		assert.Equal(t, 8080, ports[0].HostPort)
		assert.Equal(t, 80, ports[0].ContainerPort)
		assert.Equal(t, "tcp", ports[0].Protocol)
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		ports, err := parsePortFlags(nil)
		require.NoError(t, err)
		assert.Empty(t, ports)
	})
}

func TestParseEnvSlice(t *testing.T) {
	t.Run("missing equals", func(t *testing.T) {
		t.Parallel()
		_, err := parseEnvSlice([]string{"NOEQUALS"})
		assertUsageError(t, err, "must be KEY=VAL")
	})

	t.Run("valid pairs", func(t *testing.T) {
		t.Parallel()
		m, err := parseEnvSlice([]string{"A=1", "B=two", "C="})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"A": "1", "B": "two", "C": ""}, m)
	})
}

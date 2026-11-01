// ABOUTME: Tests for --env-file: the KEY=VAL rules, files and stdin, the refusal
// ABOUTME: of a key set twice, and the wiring into every verb that takes --env.
package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/cli/clitest"
)

// secret carries a '#' (an inline-comment stripper would cut it) and a trailing
// space (a trimmer would drop it), and is asserted absent from every error.
const secret = "s3cret#value "

func writeEnvFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// envVerbs is every verb that takes --env, and so must take --env-file.
var envVerbs = []string{"new", "run", "start", "restart", "reset"}

// newEnvVerbCmd returns a fresh command: StringArray flags accumulate across Parse.
func newEnvVerbCmd(t *testing.T, verb string) *cobra.Command {
	t.Helper()
	switch verb {
	case "new":
		return NewNewCmd("test")
	case "run":
		return NewRunCmd("test")
	case "start":
		return NewStartCmd()
	case "restart":
		return NewRestartCmd()
	case "reset":
		return NewResetCmd()
	}
	t.Fatalf("unknown verb %q", verb)
	return nil
}

func TestParseEnvFile_Rules(t *testing.T) {
	t.Parallel()
	got, err := parseEnvFile([]byte(strings.Join([]string{
		"# a comment",
		"   # an indented comment",
		"",
		"TOKEN=" + secret,
		"QUOTED=\"kept\"",
		"export EXPORTED=1",
		"  SPACED  =  value  ",
		"EMPTY=",
		"NO_EXPAND=$HOME",
		"CRLF=yes\r",
	}, "\n")))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"TOKEN":     secret,
		"QUOTED":    "\"kept\"",
		"EXPORTED":  "1",
		"SPACED":    "value  ",
		"EMPTY":     "",
		"NO_EXPAND": "$HOME",
		"CRLF":      "yes",
	}, got)
}

func TestParseEnvFile_Errors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ body, want string }{
		"no equals":     {"OK=1\n" + secret + "\n", "line 2: expected KEY=VAL"},
		"bad name":      {"A B=" + secret + "\n", "line 1: not a valid variable name"},
		"bare export":   {"export=" + secret + "\n", ""}, // a variable named export is valid
		"duplicate key": {"A=1\nA=" + secret + "\n", "line 2: A is already set"},
		"CR-only lines": {"# x\rA=" + secret + "\r", "line 1: CR-only line endings"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := parseEnvFile([]byte(tc.body))
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			assertUsageError(t, err, tc.want)
			assert.NotContains(t, err.Error(), strings.TrimSpace(secret), "an error must not echo a value")
		})
	}
}

func TestResolveEnv(t *testing.T) {
	t.Parallel()

	t.Run("files and stdin merge with --env", func(t *testing.T) {
		t.Parallel()
		got, err := resolveEnv([]string{"PLAIN=1"},
			[]string{writeEnvFile(t, "A="+secret+"\n"), stdinPath},
			strings.NewReader("B=2\n"))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"PLAIN": "1", "A": secret, "B": "2"}, got)
	})

	t.Run("a key in both --env and --env-file is an error", func(t *testing.T) {
		t.Parallel()
		_, err := resolveEnv([]string{"A=x"}, []string{writeEnvFile(t, "A="+secret+"\n")}, nil)
		assertUsageError(t, err, "A set by both --env and --env-file")
		assert.NotContains(t, err.Error(), strings.TrimSpace(secret))
	})

	t.Run("a key in two files is an error", func(t *testing.T) {
		t.Parallel()
		_, err := resolveEnv(nil, []string{writeEnvFile(t, "A=1\n"), writeEnvFile(t, "A="+secret+"\n")}, nil)
		assertUsageError(t, err, "A is set in more than one --env-file")
	})

	t.Run("a parse error names the file", func(t *testing.T) {
		t.Parallel()
		path := writeEnvFile(t, "oops\n")
		_, err := resolveEnv(nil, []string{path}, nil)
		assertUsageError(t, err, "--env-file "+path+": line 1")
	})
}

func TestEnvFilePaths(t *testing.T) {
	t.Parallel()
	_, err := envFilePaths([]string{""})
	assertUsageError(t, err, "--env-file needs a path")

	_, err = envFilePaths([]string{stdinPath, stdinPath})
	assertUsageError(t, err, "stdin can only be read once")
}

func TestResolveEnvFromFlags_StdinContention(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"prompt", "prompt-file"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			cmd := NewStartCmd()
			require.NoError(t, cmd.Flags().Parse([]string{"--env-file", "-", "--" + flag, "-"}))
			_, err := resolveEnvFromFlags(cmd)
			assertUsageError(t, err, "--env-file - and --"+flag+" - both read stdin")
		})
	}
}

// Every verb that takes --env takes --env-file, and both are repeatable arrays
// (a StringSlice would split a value on commas, DF195).
func TestEnvFlags_RegisteredOnEveryVerb(t *testing.T) {
	t.Parallel()
	for _, verb := range envVerbs {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			cmd := newEnvVerbCmd(t, verb)
			for _, name := range []string{"env", "env-file"} {
				f := cmd.Flags().Lookup(name)
				require.NotNil(t, f, "--%s", name)
				assert.Equal(t, "stringArray", f.Value.Type(), "--%s", name)
			}
		})
	}
}

// Each verb builds its own options; one that resolved the environment and then
// dropped it would start the sandbox without its secrets and fail nothing.
func TestEnvFile_ReachesEachVerbsOptions(t *testing.T) {
	want := map[string]string{"TOKEN": secret}
	args := func(t *testing.T) []string {
		return []string{"--env-file", writeEnvFile(t, "TOKEN="+secret+"\n")}
	}

	t.Run("new", func(t *testing.T) {
		clitest.Home(t) // resolveCreateOptions resolves dirs through cliutil.Layout()
		cmd := NewNewCmd("test")
		require.NoError(t, cmd.Flags().Parse(args(t)))
		opts, err := resolveCreateOptions(cmd, "box", ".", nil, "")
		require.NoError(t, err)
		assert.Equal(t, want, opts.Env)
	})
	t.Run("start", func(t *testing.T) {
		cmd := NewStartCmd()
		require.NoError(t, cmd.Flags().Parse(args(t)))
		opts, err := resolveStartOptions(cmd, &startOpts{})
		require.NoError(t, err)
		assert.Equal(t, want, opts.Env)
	})
	t.Run("restart", func(t *testing.T) {
		cmd := NewRestartCmd()
		require.NoError(t, cmd.Flags().Parse(args(t)))
		opts, err := resolveRestartOptions(cmd, &restartOpts{})
		require.NoError(t, err)
		assert.Equal(t, want, opts.Env)
	})
	t.Run("reset", func(t *testing.T) {
		cmd := NewResetCmd()
		require.NoError(t, cmd.Flags().Parse(args(t)))
		opts, err := resolveResetOptions(cmd, &resetOpts{})
		require.NoError(t, err)
		assert.Equal(t, want, opts.Env)
	})
}

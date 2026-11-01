// ABOUTME: --env / --env-file flags for the lifecycle verbs. --env-file reads
// ABOUTME: KEY=VAL lines from files or stdin, so a secret never reaches argv.
package lifecycle

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// stdinPath reads --env-file from stdin, as it does for --prompt and --prompt-file.
const stdinPath = "-"

var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envFlagUsage is one verb's help text for the --env / --env-file pair.
type envFlagUsage struct {
	env     string
	envFile string
}

var createEnvUsage = envFlagUsage{
	env:     "Environment variable (KEY=VAL, repeatable). Not for secrets: other local users can read it with 'ps'; use --env-file",
	envFile: "Read KEY=VAL lines from a file, or from stdin with '-' (repeatable). Use this for secrets",
}

// perStartEnvUsage is the help for start and restart, whose values apply only to
// that launch; verb names the command the user has to repeat them on.
func perStartEnvUsage(verb string) envFlagUsage {
	return envFlagUsage{
		env:     "Per-sandbox env var KEY=VAL (not persisted; re-supply on each " + verb + "). Not for secrets: use --env-file",
		envFile: "Read per-sandbox KEY=VAL lines from a file, or from stdin with '-' (repeatable, not persisted). Use this for secrets",
	}
}

var resetEnvUsage = envFlagUsage{
	env:     "Per-sandbox env var KEY=VAL applied on --restart (not persisted). Not for secrets: use --env-file",
	envFile: "Read per-sandbox KEY=VAL lines applied on --restart from a file, or from stdin with '-' (repeatable, not persisted)",
}

// addEnvFlags registers --env and --env-file together, so no verb has one without
// the other. StringArray, not StringSlice: a comma is ordinary inside a value
// (NO_PROXY=localhost,127.0.0.1), DF195.
func addEnvFlags(cmd *cobra.Command, u envFlagUsage) {
	cmd.Flags().StringArray("env", nil, u.env)
	cmd.Flags().StringArray("env-file", nil, u.envFile)
}

// resolveEnvFromFlags merges --env and --env-file into one environment.
func resolveEnvFromFlags(cmd *cobra.Command) (map[string]string, error) {
	envSlice, _ := cmd.Flags().GetStringArray("env")
	rawPaths, _ := cmd.Flags().GetStringArray("env-file")
	paths, err := envFilePaths(rawPaths)
	if err != nil {
		return nil, err
	}
	if slices.Contains(paths, stdinPath) {
		for _, name := range []string{"prompt", "prompt-file"} {
			if cliutil.FlagStr(cmd, name) == stdinPath {
				return nil, yoerrors.NewUsageError("--env-file - and --%s - both read stdin: give one of them a file", name)
			}
		}
	}
	return resolveEnv(envSlice, paths, cmd.InOrStdin())
}

// envFilePaths validates and expands the --env-file values. An empty value is an
// error rather than "no file": `--env-file "$SECRETS"` with SECRETS unset would
// otherwise start the sandbox without its secrets and say nothing.
func envFilePaths(raw []string) ([]string, error) {
	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		switch {
		case p == "":
			return nil, yoerrors.NewUsageError("--env-file needs a path, or - for stdin")
		case p == stdinPath:
			if slices.Contains(paths, stdinPath) {
				return nil, yoerrors.NewUsageError("--env-file - given twice: stdin can only be read once")
			}
		case strings.HasPrefix(p, "~") || strings.Contains(p, "${"):
			// Expanded as --prompt-file's path is; a plain path needs no Layout.
			expanded, err := cliutil.ExpandPath(p, cliutil.Layout().HomeDir, cliutil.Layout().Env().EnvForConfigInterpolation())
			if err != nil {
				return nil, yoerrors.NewUsageError("invalid --env-file path: %s", err)
			}
			p = expanded
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// resolveEnv merges --env values with the variables from each --env-file. A key
// set in two places is an error, never a silent precedence rule: whoever set it
// twice had two values in mind.
func resolveEnv(envSlice, paths []string, stdin io.Reader) (map[string]string, error) {
	envMap, err := parseEnvSlice(envSlice)
	if err != nil {
		return nil, err
	}
	fromFiles := make(map[string]string)
	for _, path := range paths {
		data, err := readEnvFile(path, stdin)
		if err != nil {
			return nil, err
		}
		vars, err := parseEnvFile(data)
		if err != nil {
			return nil, fmt.Errorf("--env-file %s: %w", path, err)
		}
		for k, v := range vars {
			if _, dup := fromFiles[k]; dup {
				return nil, yoerrors.NewUsageError("%s is set in more than one --env-file", k)
			}
			fromFiles[k] = v
		}
	}
	var both []string
	for k, v := range fromFiles {
		if _, dup := envMap[k]; dup {
			both = append(both, k)
			continue
		}
		envMap[k] = v
	}
	if len(both) > 0 {
		slices.Sort(both)
		return nil, yoerrors.NewUsageError("%s set by both --env and --env-file", strings.Join(both, ", "))
	}
	return envMap, nil
}

// readEnvFile reads one --env-file, from stdin for "-". File permissions are the
// caller's: refusing a group-readable file would only push the secret back onto
// the command line.
func readEnvFile(path string, stdin io.Reader) ([]byte, error) {
	if path == stdinPath {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read --env-file from stdin: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the user-supplied --env-file value
	if err != nil {
		return nil, fmt.Errorf("read --env-file: %w", err)
	}
	return data, nil
}

// parseEnvFile parses KEY=VAL lines. Blank lines and lines whose first
// non-whitespace character is '#' are skipped; a '#' anywhere else is part of
// the value. An optional leading `export` and whitespace around the key and the
// '=' are dropped; the value is otherwise literal to end of line (no unquoting,
// no expansion, trailing spaces kept). CRLF endings are accepted.
//
// Errors give a line number and never the line's text, which may hold a secret
// and would be printed to stderr and into a bug report's exit line (DF237).
func parseEnvFile(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		line = strings.TrimSuffix(line, "\r")
		// A CR left now means CR-only line endings, which this split reads as one
		// line: refuse it rather than take the rest of the file as one value.
		if strings.Contains(line, "\r") {
			return nil, yoerrors.NewUsageError("line %d: CR-only line endings are not supported; use LF or CRLF", n)
		}
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, val, ok := strings.Cut(trimmed, "=")
		if !ok {
			return nil, yoerrors.NewUsageError("line %d: expected KEY=VAL", n)
		}
		key = strings.TrimSpace(key)
		if rest, found := strings.CutPrefix(key, "export"); found && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			key = strings.TrimSpace(rest)
		}
		if !envVarNameRe.MatchString(key) {
			return nil, yoerrors.NewUsageError("line %d: not a valid variable name", n)
		}
		if _, dup := out[key]; dup {
			return nil, yoerrors.NewUsageError("line %d: %s is already set", n, key)
		}
		out[key] = strings.TrimLeft(val, " \t")
	}
	return out, nil
}

// ABOUTME: EnvSandboxName constant and ResolveName() for reading the sandbox
// ABOUTME: name from CLI args or the YOLOAI_SANDBOX environment variable fallback.
package cliutil

import (
	"os"

	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// EnvSandboxName is the environment variable used as default sandbox name.
const EnvSandboxName = "YOLOAI_SANDBOX"

// SandboxNameFromEnv returns the YOLOAI_SANDBOX fallback (empty if unset). This
// is the single point where the documented §12 ambient-config exception is read;
// every command that wants the env fallback routes through here rather than
// hand-rolling its own os.Getenv, so the forbidigo suppression lives in one place.
func SandboxNameFromEnv() string {
	return os.Getenv(EnvSandboxName) //nolint:forbidigo // §12: documented YOLOAI_SANDBOX feature; CLI boundary resolves it to an explicit name
}

// ValidateName checks that a sandbox name is well-formed (charset, no path
// traversal). The CLI boundary owns name-format validation; commands that
// resolve a name outside ResolveName (e.g. files' subcommand-first dispatch)
// call this instead of reaching into the store package directly.
func ValidateName(name string) error {
	sys, err := System()
	if err != nil {
		return err
	}
	return sys.ValidateSandboxName(name)
}

// ResolveName extracts the sandbox name from positional args, falling back
// to YOLOAI_SANDBOX if no name argument was provided.
// Returns the name and the remaining args (excluding the name).
// Returns a UsageError if no name is available from either source.
func ResolveName(_ *cobra.Command, args []string) (string, []string, error) {
	if len(args) >= 1 {
		if err := ValidateName(args[0]); err != nil {
			return "", nil, err
		}
		return args[0], args[1:], nil
	}

	if envName := SandboxNameFromEnv(); envName != "" {
		if err := ValidateName(envName); err != nil {
			return "", nil, err
		}
		return envName, nil, nil
	}

	return "", nil, yoerrors.NewUsageError("sandbox name required (or set YOLOAI_SANDBOX)")
}

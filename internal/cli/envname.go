// ABOUTME: EnvSandboxName constant and resolveName() for reading the sandbox
// ABOUTME: name from CLI args or the YOLOAI_SANDBOX environment variable fallback.
package cli

import (
	"os"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

// EnvSandboxName is the environment variable used as default sandbox name.
const EnvSandboxName = "YOLOAI_SANDBOX"

// resolveName extracts the sandbox name from positional args, falling back
// to YOLOAI_SANDBOX if no name argument was provided.
// Returns the name and the remaining args (excluding the name).
// Returns a UsageError if no name is available from either source.
func resolveName(_ *cobra.Command, args []string) (string, []string, error) {
	if len(args) >= 1 {
		if err := store.ValidateName(args[0]); err != nil {
			return "", nil, err
		}
		return args[0], args[1:], nil
	}

	if envName := os.Getenv(EnvSandboxName); envName != "" {
		if err := store.ValidateName(envName); err != nil {
			return "", nil, err
		}
		return envName, nil, nil
	}

	return "", nil, sandbox.NewUsageError("sandbox name required (or set YOLOAI_SANDBOX)")
}

package cli

import (
	"os"

	"github.com/kstenerud/yoloai/internal/sandbox"
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
		return args[0], args[1:], nil
	}

	if envName := os.Getenv(EnvSandboxName); envName != "" {
		return envName, nil, nil
	}

	return "", nil, sandbox.NewUsageError("sandbox name required (or set YOLOAI_SANDBOX)")
}

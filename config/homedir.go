package config

import (
	"fmt"
	"os"
	"os/user"
)

// HomeDir returns the home directory to use for yoloai data.
//
// When running under sudo (SUDO_USER is set and the current uid is 0), it
// returns the invoking user's home directory rather than root's. This lets
// users run "sudo yoloai ..." without losing their existing configuration.
//
// Panics if the home directory cannot be determined — this should never
// happen in a CLI context and a clear failure is better than silently
// producing malformed paths.
func HomeDir() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && os.Getuid() == 0 {
		u, err := user.Lookup(sudoUser)
		if err == nil {
			return u.HomeDir
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("yoloai: cannot determine home directory: %v", err))
	}
	return home
}

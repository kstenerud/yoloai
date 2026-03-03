package config

import (
	"fmt"
	"os"
)

// HomeDir returns the current user's home directory.
// Panics if the home directory cannot be determined — this should never
// happen in a CLI context and a clear failure is better than silently
// producing malformed paths.
func HomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("yoloai: cannot determine home directory: %v", err))
	}
	return home
}

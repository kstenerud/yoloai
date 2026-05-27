// ABOUTME: Single CLI-side Layout source. The one licensed os.UserHomeDir() call.

package cli

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
)

// rootLayout is the Layout recorded by SetRootLayout at CLI startup
// from either the --data-dir flag value or $HOME/.yoloai/. Every
// command handler reads it via cliLayout(). This is the single
// licensed place yoloai CLI code touches ambient HOME — every other
// library path now takes a config.Layout argument.
//
// See development-principles.md §12 (No ambient configuration).
var rootLayout config.Layout

// SetRootLayout records the Layout the rest of the CLI should use.
// Called once from the root-cobra-command setup after the
// --data-dir flag (if any) has been parsed.
//
// If SetRootLayout is never called (e.g. tests that bypass the root
// command and call command-handler functions directly), cliLayout()
// falls back to a Layout rooted at $HOME/.yoloai/ on each call (so
// tests that t.Setenv("HOME", ...) see the updated value).
func SetRootLayout(l config.Layout) {
	rootLayout = l
}

// cliLayout returns the CLI's working Layout. Reads the Layout set
// by SetRootLayout at startup, or constructs a fallback from
// $HOME/.yoloai/ when no SetRootLayout call was made (this happens
// in tests that exercise command handlers without going through the
// root command). The fallback is recomputed on every call so tests
// that change HOME between cases see fresh paths.
//
// W-L10-allowlist: this function is the single permitted caller of
// os.UserHomeDir() in CLI code (via homeBasedDataDir below). Future
// CLI handlers reading a Layout must go through here; the W-L10
// layering linter will eventually verify this.
func cliLayout() config.Layout {
	if rootLayout.DataDir == "" {
		return config.NewLayout(homeBasedDataDir())
	}
	return rootLayout
}

// homeBasedDataDir returns the conventional $HOME/.yoloai/ path.
// This is the ONE permitted os.UserHomeDir() call site in the yoloai
// library code (W-L10-allowlist). The Q-W discipline forbids any
// other library code from reading $HOME directly.
//
// Honors SUDO_USER (when running under sudo and uid 0) so a user who
// runs "sudo yoloai ..." doesn't lose their existing configuration
// to /root/.yoloai/.
func homeBasedDataDir() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && os.Getuid() == 0 {
		u, err := user.Lookup(sudoUser)
		if err == nil {
			return filepath.Join(u.HomeDir, ".yoloai")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Mirrors config.HomeDir's behavior; a CLI context without a
		// home directory is unrecoverable for our purposes.
		panic(fmt.Sprintf("yoloai: cannot determine home directory: %v", err))
	}
	return filepath.Join(home, ".yoloai")
}

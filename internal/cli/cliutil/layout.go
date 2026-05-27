// ABOUTME: Single CLI-side Layout source. The one licensed os.UserHomeDir() call.

package cliutil

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// rootLayout is the Layout recorded by SetRootLayout at CLI startup
// from either the --data-dir flag value or $HOME/.yoloai/. Every
// command handler reads it via Layout(). This is the single
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
// command and call command-handler functions directly), Layout()
// falls back to a Layout rooted at $HOME/.yoloai/ on each call (so
// tests that t.Setenv("HOME", ...) see the updated value).
func SetRootLayout(l config.Layout) {
	rootLayout = l
}

// Layout returns the CLI's working Layout. Reads the Layout set
// by SetRootLayout at startup, or constructs a fallback from
// $HOME/.yoloai/ when no SetRootLayout call was made (this happens
// in tests that exercise command handlers without going through the
// root command). The fallback is recomputed on every call so tests
// that change HOME between cases see fresh paths.
//
// W-L10-allowlist: this function is the single permitted caller of
// os.UserHomeDir() in CLI code (via resolveHome below). Future
// CLI handlers reading a Layout must go through here; the W-L10
// layering linter will eventually verify this.
func Layout() config.Layout {
	if rootLayout.DataDir == "" {
		home := resolveHome()
		return config.NewLayoutFor(filepath.Join(home, ".yoloai"), home)
	}
	return rootLayout
}

// LayoutForDataDir constructs a Layout for an explicit --data-dir
// value, pairing the supplied dataDir with the user's actual $HOME.
// Used by the root command's PersistentPreRunE when --data-dir is
// non-empty — the user's home stays bound to the real $HOME even
// when DataDir is rerooted (e.g. /var/lib/yoloai under a service
// install).
func LayoutForDataDir(dataDir string) config.Layout {
	return config.NewLayoutFor(dataDir, resolveHome())
}

// resolveHome returns the user's $HOME, honoring SUDO_USER under
// sudo so "sudo yoloai ..." doesn't reroot to /root. This is the
// ONE permitted os.UserHomeDir() call site in the yoloai library
// code (W-L10-allowlist). The Q-W discipline forbids any other
// library code from reading $HOME directly.
func resolveHome() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && fileutil.ProcessIsRoot() {
		u, err := user.Lookup(sudoUser)
		if err == nil {
			return u.HomeDir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// A CLI context without a home directory is unrecoverable for our purposes.
		panic(fmt.Sprintf("yoloai: cannot determine home directory: %v", err))
	}
	return home
}

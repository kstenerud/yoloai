// ABOUTME: Single CLI-side Layout source. The one licensed os.UserHomeDir() call.

package cliutil

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// rootLayout is the Layout recorded by SetRootLayout / SetRootLayoutFromFlag
// at CLI startup, from either the --data-dir flag value or the default
// $HOME/.yoloai/. Every command handler reads it via Layout(). This is the
// single licensed place yoloai CLI code touches ambient HOME — every other
// library path now takes a config.Layout argument.
//
// See development-principles.md §12 (No ambient configuration).
var rootLayout config.Layout

// SetRootLayout records the Layout the rest of the CLI should use.
// Production goes through SetRootLayoutFromFlag (from the root command's
// PersistentPreRunE); this lower-level setter exists for tests that call
// command-handler functions directly without going through the root command —
// they must establish a Layout explicitly rather than relying on a fabricated
// fallback.
func SetRootLayout(l config.Layout) {
	rootLayout = l
}

// SetRootLayoutFromFlag records the process-wide root Layout from the parsed
// --data-dir flag value, defaulting to $HOME/.yoloai when the flag is empty.
// Called once from the root command's PersistentPreRunE before any handler
// runs. This is the single place ambient HOME resolves into the CLI's Layout
// (via LayoutForDataDir → resolveHome); see development-principles.md §12.
func SetRootLayoutFromFlag(dataDir string) {
	if dataDir == "" {
		dataDir = filepath.Join(resolveHome(), ".yoloai")
	}
	rootLayout = LayoutForDataDir(dataDir)
}

// Layout returns the CLI's working Layout. It is a pure accessor for the
// Layout established by SetRootLayoutFromFlag (production) or SetRootLayout
// (direct-handler tests). Calling it before either ran is a programming error
// — the panic surfaces a handler that bypassed the root command's
// PersistentPreRunE rather than silently fabricating a Layout from ambient
// HOME.
func Layout() config.Layout {
	if rootLayout.DataDir == "" {
		panic("cliutil.Layout() called before the root Layout was set; CLI handlers run under the root command's PersistentPreRunE (SetRootLayoutFromFlag), and direct-handler tests must call SetRootLayout")
	}
	return rootLayout
}

// LayoutForDataDir constructs a library Layout for an explicit --data-dir
// value, pairing the supplied top dir with the user's actual $HOME. The
// --data-dir value names the shared top directory (TOP); the library is
// rooted at TOP/library so the CLI's own state (TOP/cli) can sit beside it
// without clashing. The user's home stays bound to the real $HOME even when
// TOP is rerooted (e.g. /var/lib/yoloai under a service install).
func LayoutForDataDir(dataDir string) config.Layout {
	l := config.NewLayoutFor(filepath.Join(dataDir, libraryNamespace), resolveHome())
	l.Env = processEnv()
	return l
}

// processEnv snapshots the process environment into a map for the
// Layout. This is the single licensed os.Environ() read — the §12
// boundary that captures ambient env once so library code can expand
// user-declared ${VAR} references and resolve agent credentials against
// threaded data instead of the live process env.
//
// Under `sudo` (without -E) the API-key / OAuth env vars are stripped
// from os.Environ; sudoRecoveredEnv recovers them from the parent sudo
// process so a sudo-launched `new`/`restart` still injects credentials.
// Live env values always win — recovery only fills keys absent from the
// snapshot. Sudo is a host/CLI concern; a daemon embedder never runs
// under sudo, so this recovery lives at the CLI boundary, not the library.
func processEnv() map[string]string {
	entries := os.Environ()
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	for k, v := range fileutil.SudoParentEnv() {
		if m[k] == "" {
			m[k] = v
		}
	}
	return m
}

// resolveHome returns the user's $HOME, honoring SUDO_USER under
// sudo so "sudo yoloai ..." doesn't reroot to /root. This is the one
// permitted os.UserHomeDir() call site in CLI code; no other code
// reads $HOME directly.
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

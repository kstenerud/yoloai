// ABOUTME: CLI test helpers that isolate HOME and establish the process-wide
// ABOUTME: root Layout, standing in for the root command's PersistentPreRunE.

// Package clitest provides test helpers for CLI-layer tests. Production
// resolves the implicit $HOME/.yoloai default once, at the edge (the root
// command's PersistentPreRunE → cliutil.SetRootLayoutFromFlag); cliutil.Layout
// is then a pure accessor. Leaf-command tests that Execute a subcommand
// standalone bypass that edge, so they must establish the Layout explicitly —
// these helpers are that explicit edge for tests.
package clitest

import (
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Home isolates HOME to a fresh temp dir and points the CLI root Layout at
// $HOME/.yoloai, mirroring what PersistentPreRunE does in production. Returns
// the temp home path.
func Home(t *testing.T) string {
	t.Helper()
	home := testutil.IsolatedHome(t)
	cliutil.SetRootLayout(cliutil.LayoutForDataDir(filepath.Join(home, ".yoloai")))
	return home
}

// ConfigDir isolates HOME, establishes the root Layout, and returns the
// library defaults dir ($HOME/.yoloai/library/defaults) for tests that seed a
// defaults config.yaml. The "library" namespace mirrors where the CLI roots
// the library Layout (TOP/library); see cliutil.Layout / clipaths.go.
func ConfigDir(t *testing.T) string {
	t.Helper()
	home := Home(t)
	dir := filepath.Join(home, ".yoloai", "library", "defaults")
	require.NoError(t, fileutil.MkdirAll(dir, 0750))
	return dir
}

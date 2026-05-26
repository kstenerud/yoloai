// ABOUTME: Single bridge from the CLI to a config.Layout — the one place that
// ABOUTME: turns ambient HOME into an explicit Layout until Q-W.5 lands.

package cli

import "github.com/kstenerud/yoloai/config"

// cliLayout returns the CLI's working Layout. Single bridge point
// between ambient HOME-based path resolution and Q-W's explicit
// Layout discipline.
//
// Q-W.5 plan: root.go will construct a Layout once at CLI startup
// (from $HOME or --data-dir flag) and pass it to every command
// handler. At that point this function's body becomes a reference
// to that root-constructed Layout, and the os.UserHomeDir() call
// inside config.YoloaiDir() becomes the single licensed allowlisted
// site — finally satisfying §12's no-ambient-configuration ban.
//
// Until then, every CLI handler that needs a sandbox path obtains
// it via cliLayout() — never via the deleted store.Dir(name) or
// the deprecated package-level config.SandboxesDir(). One bridge
// point, one place to remove in Q-W.5.
func cliLayout() config.Layout {
	return config.NewLayout(config.YoloaiDir())
}

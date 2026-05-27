// ABOUTME: `yoloai system tart` commands for managing Apple simulator runtime base images.
// ABOUTME: Pre-create, list, and remove runtime bases (iOS, tvOS, watchOS, visionOS).
//
// This is the backend-scoped subpackage `tart`. It is the ONLY package
// in internal/cli/ that may import internal/runtime/tart — enforced
// by depguard in .golangci.yml. The cli root injects layout and
// runtime-construction helpers via NewCmd so this package stays free
// of any import dependency on internal/cli (W-L13).
package tart

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	rt "github.com/kstenerud/yoloai/internal/runtime"
	tartrt "github.com/kstenerud/yoloai/internal/runtime/tart"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// pkgLayout and pkgNewRuntime are set by NewCmd at command-tree
// construction time. They reference the cli root chokepoint helpers
// without creating an import cycle (cli root imports this subpackage
// to register the command; this subpackage cannot import cli back).
var (
	pkgLayout     func() config.Layout
	pkgNewRuntime func(ctx context.Context, backend string) (rt.Runtime, error)
)

// NewCmd defines `yoloai system tart` (formerly `yoloai system runtime`).
// The old `runtime` name remains a hidden alias that emits a deprecation warning.
//
// layoutFn and newRuntimeFn inject the cli root's chokepoint helpers
// (cliLayout / newRuntime) so this subpackage doesn't need to import
// internal/cli back (which would create a cycle). NewCmd stores them
// in package-level vars; subcommand RunE handlers read those vars at
// invocation time.
func NewCmd(layoutFn func() config.Layout, newRuntimeFn func(ctx context.Context, backend string) (rt.Runtime, error)) *cobra.Command {
	pkgLayout = layoutFn
	pkgNewRuntime = newRuntimeFn
	cmd := &cobra.Command{
		Use:     "tart",
		Aliases: []string{"runtime"},
		Short:   "Manage Apple simulator runtime base images (Tart backend)",
		Long: `Pre-create and manage Tart base VMs with iOS/tvOS/watchOS/visionOS runtimes.

Only available on macOS with the Tart backend.`,
		PersistentPreRunE: requireTartBackend,
	}

	cmd.AddCommand(
		newSystemTartAddCmd(),
		newSystemTartListCmd(),
		newSystemTartRemoveCmd(),
	)

	return cmd
}

// invokedViaRuntimeAlias reports whether the user typed "system runtime …"
// rather than "system tart …". Cobra's CalledAs() is only populated on the
// resolved leaf command, so we sniff os.Args directly for the contiguous
// "system runtime" pair, skipping global flags.
func invokedViaRuntimeAlias(args []string) bool {
	// Skip the program name. Walk positional arguments (anything that doesn't
	// start with "-") until we find "system" followed immediately by "runtime".
	var positional []string
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		positional = append(positional, a)
	}
	for i := 0; i+1 < len(positional); i++ {
		if positional[i] == "system" && positional[i+1] == "runtime" {
			return true
		}
	}
	return false
}

// requireTartBackend gates the `system tart` subtree: it warns when invoked
// via the deprecated `system runtime` alias and verifies that the Tart
// backend is available before any subcommand runs.
func requireTartBackend(cmd *cobra.Command, _ []string) error {
	if invokedViaRuntimeAlias(os.Args) {
		fmt.Fprintln(cmd.ErrOrStderr(), //nolint:errcheck // best-effort warning
			"warning: 'yoloai system runtime' is deprecated; use 'yoloai system tart' instead.")
	}

	if runtime.GOOS != "darwin" {
		return sandbox.NewUsageError("yoloai system tart commands are only available on macOS")
	}

	// Inline of cli's checkBackend: spin up a tart runtime, close it,
	// and report availability + the failure reason. checkBackend lives
	// in internal/cli but importing back would cycle; the check is
	// only 5 lines so inline it.
	probeRT, err := pkgNewRuntime(cmd.Context(), "tart")
	if err != nil {
		return sandbox.NewUsageError("Tart backend not available: %s\n\nInstall Tart: brew install cirruslabs/cli/tart", err.Error())
	}
	_ = probeRT.Close()
	return nil
}

func newSystemTartAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <platform>...",
		Short: "Create a runtime base image with specified platforms",
		Long: `Create a runtime base image with one or more Apple simulator runtimes.

Platforms: ios, tvos, watchos, visionos
Version syntax: platform[:version]
  - ios           (latest available)
  - ios:26.4      (specific version)
  - ios tvos      (multiple platforms)

Examples:
  yoloai system tart add ios
  yoloai system tart add ios:26.4 tvos:26.1
  yoloai system tart add ios tvos watchos`,
		Args: cobra.MinimumNArgs(1),
		RunE: runSystemTartAdd,
	}
}

// runSystemTartAdd implements the `system tart add` command body.
func runSystemTartAdd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	tartRuntime, closeRT, err := openTartRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeRT()

	fmt.Fprintf(cmd.OutOrStdout(), "\nResolving runtime versions...\n") //nolint:errcheck
	resolved, err := tartrt.ResolveRuntimeVersions(args)
	if err != nil {
		return fmt.Errorf("resolve runtimes: %w", err)
	}

	printResolvedVersions(cmd, args, resolved)

	cacheKey := tartrt.GenerateCacheKey(resolved)
	baseName := "yoloai-base-" + cacheKey

	exists, err := tartRuntime.BaseExists(ctx, baseName)
	if err != nil {
		return fmt.Errorf("check base: %w", err)
	}
	if exists {
		return sandbox.NewUsageError("Runtime base '%s' already exists.\n\nUse 'yoloai system tart list' to see all bases.", baseName)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nCreating runtime base: %s\n\n", baseName) //nolint:errcheck

	release, err := tartrt.AcquireBaseLock(pkgLayout(), baseName)
	if err != nil {
		return fmt.Errorf("acquire base lock: %w", err)
	}
	defer release()

	if err := tartRuntime.CreateBase(ctx, baseName, resolved); err != nil {
		return fmt.Errorf("create base: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nRuntime base created successfully\n") //nolint:errcheck
	return nil
}

// printResolvedVersions prints the resolved platform versions to stdout.
func printResolvedVersions(cmd *cobra.Command, args []string, resolved []tartrt.RuntimeVersion) {
	for i, rv := range resolved {
		inputParts := strings.SplitN(args[i], ":", 2)
		platformCap := strings.ToUpper(rv.Platform[:1]) + rv.Platform[1:]
		if len(inputParts) == 1 || inputParts[1] == "" || inputParts[1] == "latest" {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s (latest) → %s %s\n", rv.Platform, platformCap, rv.Version) //nolint:errcheck
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s:%s → %s %s\n", rv.Platform, rv.Version, platformCap, rv.Version) //nolint:errcheck
		}
	}
}

// openTartRuntime opens the tart backend and returns the typed runtime and a close func.
func openTartRuntime(ctx context.Context) (*tartrt.Runtime, func(), error) {
	rt, err := pkgNewRuntime(ctx, "tart")
	if err != nil {
		return nil, nil, fmt.Errorf("create tart runtime: %w", err)
	}
	tartRuntime, ok := rt.(*tartrt.Runtime)
	if !ok {
		// This is structurally impossible: pkgNewRuntime(ctx, "tart") returns a
		// *tartrt.Runtime by construction (the registry's factory for "tart"
		// produces exactly that type). Reaching here means the registry has
		// been wired with the wrong factory under the "tart" key — a
		// programming bug, not an operational failure. Panic so the bug
		// surfaces immediately with a stack trace; root.go's recover() will
		// finalize the bug report and re-panic for the default Go handler
		// to render. Q-X principle: programming bugs panic, not returns.
		_ = rt.Close()
		panic(fmt.Sprintf("yoloai bug: pkgNewRuntime(\"tart\") returned %T, not *tartrt.Runtime; check runtime/tart registry wiring", rt))
	}
	return tartRuntime, func() { _ = rt.Close() }, nil
}

func newSystemTartListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [filter...]",
		Short: "List runtime base images",
		Long: `List all runtime base images with their platform versions and sizes.

Optionally filter by platform name (ios, tvos, watchos, visionos).

Examples:
  yoloai system tart list
  yoloai system tart list ios
  yoloai system tart list ios tvos`,
		Args: cobra.ArbitraryArgs,
		RunE: runSystemTartList,
	}
}

func runSystemTartList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	tartRuntime, closeRT, err := openTartRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeRT()

	bases, err := listRuntimeBases(ctx, tartRuntime)
	if err != nil {
		return fmt.Errorf("list bases: %w", err)
	}

	bases = filterRuntimeBases(bases, args)

	availableRuntimes, err := tartrt.QueryAvailableRuntimes()
	if err != nil {
		availableRuntimes = nil
	}

	return printRuntimeBaseList(cmd, bases, availableRuntimes)
}

// filterRuntimeBases filters bases by platform name filters (case-insensitive substring match).
func filterRuntimeBases(bases []runtimeBase, filters []string) []runtimeBase {
	if len(filters) == 0 {
		return bases
	}
	filtered := []runtimeBase{}
	for _, base := range bases {
		for _, filter := range filters {
			if strings.Contains(base.CacheKey, strings.ToLower(filter)) {
				filtered = append(filtered, base)
				break
			}
		}
	}
	return filtered
}

// printRuntimeBaseList displays runtime base images to stdout.
func printRuntimeBaseList(cmd *cobra.Command, bases []runtimeBase, availableRuntimes []tartrt.RuntimeVersion) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out) //nolint:errcheck
	if len(bases) == 0 {
		fmt.Fprintln(out, "No runtime base images found.")               //nolint:errcheck
		fmt.Fprintln(out)                                                //nolint:errcheck
		fmt.Fprintln(out, "Create one with: yoloai system tart add ios") //nolint:errcheck
		return nil
	}

	fmt.Fprintln(out, "Runtime Base Images:") //nolint:errcheck
	totalSize := int64(0)
	for _, base := range bases {
		if base.CacheKey == "" {
			fmt.Fprintf(out, "  %-32s (no runtimes, %s)\n", base.Name, formatSize(base.Size)) //nolint:errcheck
		} else {
			runtimes := formatCacheKey(base.CacheKey)
			fmt.Fprintf(out, "  %-32s (%s, %s)\n", base.Name, runtimes, formatSize(base.Size)) //nolint:errcheck
		}
		totalSize += base.Size
	}

	if len(availableRuntimes) > 0 {
		fmt.Fprintln(out) //nolint:errcheck
		latest := formatAvailableRuntimes(availableRuntimes)
		fmt.Fprintf(out, "Latest available on host: %s\n", latest) //nolint:errcheck
	}

	fmt.Fprintln(out)                                                                                        //nolint:errcheck
	fmt.Fprintf(out, "Total: %d %s, %s\n", len(bases), pluralize("base", len(bases)), formatSize(totalSize)) //nolint:errcheck

	return nil
}

type runtimeRemoveOpts struct {
	yes bool
}

func newSystemTartRemoveCmd() *cobra.Command {
	opts := &runtimeRemoveOpts{}
	cmd := &cobra.Command{
		Use:   "remove <base-name>",
		Short: "Remove a runtime base image",
		Long: `Remove a runtime base image to free disk space.

The base name should be the full name as shown in 'yoloai system tart list'.

Example:
  yoloai system tart remove yoloai-base-ios-26.4`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runSystemTartRemove(cmd, args, opts) },
	}

	cmd.Flags().BoolVar(&opts.yes, "yes", false, "Skip confirmation prompt")

	return cmd
}

// runSystemTartRemove implements the `system tart remove` command body.
func runSystemTartRemove(cmd *cobra.Command, args []string, opts *runtimeRemoveOpts) error {
	baseName := args[0]

	ctx := cmd.Context()
	tartRuntime, closeRT, err := openTartRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeRT()

	exists, err := tartRuntime.BaseExists(ctx, baseName)
	if err != nil {
		return fmt.Errorf("check base: %w", err)
	}
	if !exists {
		return sandbox.NewUsageError("Runtime base '%s' not found.\n\nUse 'yoloai system tart list' to see available bases.", baseName)
	}

	size, err := runtimeBaseSize(ctx, tartRuntime, baseName)
	if err != nil {
		return err
	}

	if !opts.yes {
		if cancelled, err := confirmRuntimeRemove(cmd, baseName, size); err != nil || cancelled {
			return err
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nDeleting %s...\n", baseName) //nolint:errcheck
	if err := tartRuntime.DeleteVM(ctx, baseName); err != nil {
		return fmt.Errorf("delete base: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Freed %s\n", formatSize(size)) //nolint:errcheck
	return nil
}

// runtimeBaseSize returns the disk size of the named runtime base.
func runtimeBaseSize(ctx context.Context, tartRuntime *tartrt.Runtime, baseName string) (int64, error) {
	bases, err := listRuntimeBases(ctx, tartRuntime)
	if err != nil {
		return 0, fmt.Errorf("list bases: %w", err)
	}
	for _, base := range bases {
		if base.Name == baseName {
			return base.Size, nil
		}
	}
	return 0, nil
}

// confirmRuntimeRemove prompts the user to confirm deletion. Returns (true, nil) if cancelled.
func confirmRuntimeRemove(cmd *cobra.Command, baseName string, size int64) (cancelled bool, err error) {
	fmt.Fprintf(cmd.OutOrStdout(), "\nThis will delete runtime base '%s' (%s).\n", baseName, formatSize(size)) //nolint:errcheck
	fmt.Fprintf(cmd.OutOrStdout(), "Continue? [y/N]: ")                                                        //nolint:errcheck
	var response string
	fmt.Scanln(&response) //nolint:errcheck,gosec
	if strings.ToLower(strings.TrimSpace(response)) != "y" {
		fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.") //nolint:errcheck
		return true, nil
	}
	return false, nil
}

// runtimeBase represents a runtime base image as shown to the user.
type runtimeBase struct {
	Name     string
	CacheKey string // e.g., "ios-26.4-tvos-26.1" (extracted from name)
	Size     int64
}

// listRuntimeBases lists all yoloai-base-* VMs via the typed tartrt.Runtime API.
func listRuntimeBases(ctx context.Context, tartRuntime *tartrt.Runtime) ([]runtimeBase, error) {
	entries, err := tartRuntime.ListVMs(ctx)
	if err != nil {
		return nil, err
	}

	var bases []runtimeBase
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name, "yoloai-base") {
			continue
		}
		var cacheKey string
		if entry.Name != "yoloai-base" {
			cacheKey = strings.TrimPrefix(entry.Name, "yoloai-base-")
		}
		bases = append(bases, runtimeBase{
			Name:     entry.Name,
			CacheKey: cacheKey,
			Size:     entry.Size,
		})
	}
	return bases, nil
}

// formatCacheKey converts "ios-26.4-tvos-26.1" to "iOS 26.4, tvOS 26.1".
func formatCacheKey(cacheKey string) string {
	// Split by hyphen, group in pairs
	parts := strings.Split(cacheKey, "-")
	var runtimes []string
	for i := 0; i+1 < len(parts); i += 2 {
		platform := parts[i]
		version := parts[i+1]
		platformCap := strings.ToUpper(platform[:1]) + platform[1:]
		runtimes = append(runtimes, fmt.Sprintf("%s %s", platformCap, version))
	}
	return strings.Join(runtimes, ", ")
}

// formatAvailableRuntimes formats available runtimes as "iOS 26.4, tvOS 26.2, ...".
func formatAvailableRuntimes(runtimes []tartrt.RuntimeVersion) string {
	// Group by platform, pick latest of each
	latest := make(map[string]tartrt.RuntimeVersion)
	for _, rt := range runtimes {
		existing, ok := latest[rt.Platform]
		if !ok {
			latest[rt.Platform] = rt
			continue
		}
		// Compare versions (simple string comparison is good enough for display)
		if rt.Version > existing.Version {
			latest[rt.Platform] = rt
		}
	}

	// Format
	var parts []string
	// Sort platforms for consistent output
	platforms := []string{"ios", "tvos", "watchos", "visionos"}
	for _, platform := range platforms {
		if rt, ok := latest[platform]; ok {
			platformCap := strings.ToUpper(rt.Platform[:1]) + rt.Platform[1:]
			parts = append(parts, fmt.Sprintf("%s %s", platformCap, rt.Version))
		}
	}

	return strings.Join(parts, ", ")
}

// formatSize formats bytes as "20 GB", "36 GB", etc.
func formatSize(bytes int64) string {
	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb < 0.1 {
		return "0 GB"
	}
	return fmt.Sprintf("%.0f GB", gb)
}

// pluralize returns singular or plural form based on count.
func pluralize(singular string, count int) string {
	if count == 1 {
		return singular
	}
	return singular + "s"
}

// ABOUTME: `yoloai system tart` commands for managing Apple simulator runtime base images.
// ABOUTME: Pre-create, list, and remove runtime bases (iOS, tvOS, watchOS, visionOS).
//
// This package is pure presentation over the public yoloai.System
// TartBases admin handle: it parses flags, prompts, and formats output, but
// performs no runtime work itself. The cli root injects a System factory
// via NewCmd so this package depends only on the public yoloai surface (no
// internal/runtime, internal/runtime/tart, or internal/cli imports).
package tart

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// pkgClient is set by NewCmd at command-tree construction time. It returns the
// cli root's public System so this subpackage never imports internal/cli
// back (which would create a cycle). Subcommand handlers read it at invocation.
var pkgClient func() (*yoloai.System, error)

// NewCmd defines `yoloai system tart` (formerly `yoloai system runtime`).
// The old `runtime` name remains a hidden alias that emits a deprecation warning.
//
// clientFn injects the cli root's public System factory so this
// subpackage doesn't need to import internal/cli back (which would create a
// cycle). NewCmd stores it in a package-level var; subcommand RunE handlers
// read that var at invocation time.
func NewCmd(clientFn func() (*yoloai.System, error)) *cobra.Command {
	pkgClient = clientFn
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
		return yoerrors.NewUsageError("yoloai system tart commands are only available on macOS")
	}

	sys, err := pkgClient()
	if err != nil {
		return err
	}
	available, err := sys.TartBases().Available(cmd.Context())
	if err != nil || !available {
		reason := "backend probe failed"
		if err != nil {
			reason = err.Error()
		}
		return yoerrors.NewUsageError("Tart backend not available: %s\n\nInstall Tart: brew install cirruslabs/cli/tart", reason)
	}
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
	sys, err := pkgClient()
	if err != nil {
		return err
	}
	h := sys.TartBases()

	fmt.Fprintf(cmd.OutOrStdout(), "\nResolving runtime versions...\n") //nolint:errcheck
	plan, err := h.PlanBase(args)
	if err != nil {
		return fmt.Errorf("resolve runtimes: %w", err)
	}

	printResolvedVersions(cmd, args, plan.Runtimes)

	fmt.Fprintf(cmd.OutOrStdout(), "\nCreating runtime base: %s\n\n", plan.Name) //nolint:errcheck

	if _, err := h.Add(ctx, plan, cmd.OutOrStdout()); err != nil {
		var exists *yoloai.TartBaseExistsError
		if errors.As(err, &exists) {
			return yoerrors.NewUsageError("Runtime base '%s' already exists.\n\nUse 'yoloai system tart list' to see all bases.", plan.Name)
		}
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nRuntime base created successfully\n") //nolint:errcheck
	return nil
}

// printResolvedVersions prints the resolved platform versions to stdout.
func printResolvedVersions(cmd *cobra.Command, args []string, resolved []yoloai.TartRuntimeVersion) {
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
	sys, err := pkgClient()
	if err != nil {
		return err
	}
	h := sys.TartBases()

	bases, err := h.List(ctx)
	if err != nil {
		return fmt.Errorf("list bases: %w", err)
	}

	bases = filterRuntimeBases(bases, args)

	availableRuntimes, err := h.AvailableRuntimes()
	if err != nil {
		availableRuntimes = nil
	}

	return printRuntimeBaseList(cmd, bases, availableRuntimes)
}

// filterRuntimeBases filters bases by platform name filters (case-insensitive substring match).
func filterRuntimeBases(bases []yoloai.TartBaseInfo, filters []string) []yoloai.TartBaseInfo {
	if len(filters) == 0 {
		return bases
	}
	filtered := []yoloai.TartBaseInfo{}
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
func printRuntimeBaseList(cmd *cobra.Command, bases []yoloai.TartBaseInfo, availableRuntimes []yoloai.TartRuntimeVersion) error {
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
	sys, err := pkgClient()
	if err != nil {
		return err
	}
	h := sys.TartBases()

	// Look up the size up front so the confirmation prompt can show it. This
	// also gives the clean "not found" message before any delete is attempted.
	bases, err := h.List(ctx)
	if err != nil {
		return fmt.Errorf("list bases: %w", err)
	}
	var size int64
	found := false
	for _, base := range bases {
		if base.Name == baseName {
			size, found = base.Size, true
			break
		}
	}
	if !found {
		return yoerrors.NewUsageError("Runtime base '%s' not found.\n\nUse 'yoloai system tart list' to see available bases.", baseName)
	}

	if !opts.yes {
		if cancelled, err := confirmRuntimeRemove(cmd, baseName, size); err != nil || cancelled {
			return err
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nDeleting %s...\n", baseName) //nolint:errcheck
	freed, err := h.Remove(ctx, baseName)
	if err != nil {
		var notFound *yoloai.TartBaseNotFoundError
		if errors.As(err, &notFound) {
			return yoerrors.NewUsageError("Runtime base '%s' not found.\n\nUse 'yoloai system tart list' to see available bases.", baseName)
		}
		return fmt.Errorf("delete base: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Freed %s\n", formatSize(freed)) //nolint:errcheck
	return nil
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
func formatAvailableRuntimes(runtimes []yoloai.TartRuntimeVersion) string {
	// Group by platform, pick latest of each
	latest := make(map[string]yoloai.TartRuntimeVersion)
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

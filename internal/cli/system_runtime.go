package cli

// ABOUTME: `yoloai system runtime` commands for managing Apple simulator runtime base images.
// ABOUTME: Pre-create, list, and remove runtime bases (iOS, tvOS, watchOS, visionOS).

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/kstenerud/yoloai/runtime/tart"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSystemRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage Apple simulator runtime base images",
		Long:  "Pre-create and manage base images with iOS/tvOS/watchOS/visionOS runtimes.\n\nOnly available on macOS with Tart backend.",
	}

	cmd.AddCommand(
		newSystemRuntimeAddCmd(),
		newSystemRuntimeListCmd(),
		newSystemRuntimeRemoveCmd(),
	)

	return cmd
}

func newSystemRuntimeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <platform>...",
		Short: "Create a runtime base image with specified platforms",
		Long: `Create a runtime base image with one or more Apple simulator runtimes.

Platforms: ios, tvos, watchos, visionos
Version syntax: platform[:version]
  - ios           (latest available)
  - ios:26.4      (specific version)
  - ios tvos      (multiple platforms)

Examples:
  yoloai system runtime add ios
  yoloai system runtime add ios:26.4 tvos:26.1
  yoloai system runtime add ios tvos watchos`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check macOS
			if runtime.GOOS != "darwin" {
				return sandbox.NewUsageError("yoloai system runtime commands are only available on macOS")
			}

			// Check Tart backend
			ctx := cmd.Context()
			available, note := checkBackend(ctx, "tart")
			if !available {
				return sandbox.NewUsageError("Tart backend not available: %s\n\nInstall Tart: brew install cirruslabs/cli/tart", note)
			}

			// Get Tart runtime
			rt, err := newRuntime(ctx, "tart")
			if err != nil {
				return fmt.Errorf("create tart runtime: %w", err)
			}
			defer rt.Close() //nolint:errcheck

			tartRuntime, ok := rt.(*tart.Runtime)
			if !ok {
				return fmt.Errorf("internal error: tart backend type mismatch")
			}

			// Resolve runtime versions
			fmt.Fprintf(cmd.OutOrStdout(), "\nResolving runtime versions...\n") //nolint:errcheck
			resolved, err := tart.ResolveRuntimeVersions(args)
			if err != nil {
				return fmt.Errorf("resolve runtimes: %w", err)
			}

			// Show what was resolved
			for i, rt := range resolved {
				// Check if version was explicitly specified or is "latest"
				inputParts := strings.SplitN(args[i], ":", 2)
				platformCap := strings.ToUpper(rt.Platform[:1]) + rt.Platform[1:]
				if len(inputParts) == 1 || inputParts[1] == "" || inputParts[1] == "latest" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s (latest) → %s %s\n", //nolint:errcheck
						rt.Platform, platformCap, rt.Version)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s:%s → %s %s\n", //nolint:errcheck
						rt.Platform, rt.Version, platformCap, rt.Version)
				}
			}

			// Generate cache key and base name
			cacheKey := tart.GenerateCacheKey(resolved)
			baseName := "yoloai-base-" + cacheKey

			// Check if base already exists
			exists, err := tartRuntime.BaseExists(ctx, baseName)
			if err != nil {
				return fmt.Errorf("check base: %w", err)
			}
			if exists {
				return sandbox.NewUsageError("Runtime base '%s' already exists.\n\nUse 'yoloai system runtime list' to see all bases.", baseName)
			}

			// Create the base
			fmt.Fprintf(cmd.OutOrStdout(), "\nCreating runtime base: %s\n\n", baseName) //nolint:errcheck

			// Acquire lock (blocks if another process creating)
			release, err := tart.AcquireBaseLock(baseName)
			if err != nil {
				return fmt.Errorf("acquire base lock: %w", err)
			}
			defer release()

			// Create base with output streaming to stdout (not stderr)
			if err := tartRuntime.CreateBase(ctx, baseName, resolved); err != nil {
				return fmt.Errorf("create base: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nRuntime base created successfully\n") //nolint:errcheck
			return nil
		},
	}

	return cmd
}

func newSystemRuntimeListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [filter...]",
		Short: "List runtime base images",
		Long: `List all runtime base images with their platform versions and sizes.

Optionally filter by platform name (ios, tvos, watchos, visionos).

Examples:
  yoloai system runtime list
  yoloai system runtime list ios
  yoloai system runtime list ios tvos`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check macOS
			if runtime.GOOS != "darwin" {
				return sandbox.NewUsageError("yoloai system runtime commands are only available on macOS")
			}

			// Check Tart backend
			ctx := cmd.Context()
			available, note := checkBackend(ctx, "tart")
			if !available {
				return sandbox.NewUsageError("Tart backend not available: %s\n\nInstall Tart: brew install cirruslabs/cli/tart", note)
			}

			// Get Tart runtime
			rt, err := newRuntime(ctx, "tart")
			if err != nil {
				return fmt.Errorf("create tart runtime: %w", err)
			}
			defer rt.Close() //nolint:errcheck

			tartRuntime, ok := rt.(*tart.Runtime)
			if !ok {
				return fmt.Errorf("internal error: tart backend type mismatch")
			}

			// List all bases (call tart list and filter for yoloai-base-*)
			bases, err := listRuntimeBases(ctx, tartRuntime)
			if err != nil {
				return fmt.Errorf("list bases: %w", err)
			}

			// Apply filters if provided
			if len(args) > 0 {
				filtered := []runtimeBase{}
				for _, base := range bases {
					// Check if any filter matches
					match := false
					for _, filter := range args {
						if strings.Contains(base.CacheKey, strings.ToLower(filter)) {
							match = true
							break
						}
					}
					if match {
						filtered = append(filtered, base)
					}
				}
				bases = filtered
			}

			// Query latest available versions on host
			availableRuntimes, err := tart.QueryAvailableRuntimes()
			if err != nil {
				// Non-fatal: just skip showing latest
				availableRuntimes = nil
			}

			// Display results
			out := cmd.OutOrStdout()
			fmt.Fprintln(out) //nolint:errcheck
			if len(bases) == 0 {
				fmt.Fprintln(out, "No runtime base images found.")                  //nolint:errcheck
				fmt.Fprintln(out)                                                   //nolint:errcheck
				fmt.Fprintln(out, "Create one with: yoloai system runtime add ios") //nolint:errcheck
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

			// Show latest available on host
			if len(availableRuntimes) > 0 {
				fmt.Fprintln(out) //nolint:errcheck
				latest := formatAvailableRuntimes(availableRuntimes)
				fmt.Fprintf(out, "Latest available on host: %s\n", latest) //nolint:errcheck
			}

			fmt.Fprintln(out)                                                                                        //nolint:errcheck
			fmt.Fprintf(out, "Total: %d %s, %s\n", len(bases), pluralize("base", len(bases)), formatSize(totalSize)) //nolint:errcheck

			return nil
		},
	}

	return cmd
}

func newSystemRuntimeRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <base-name>",
		Short: "Remove a runtime base image",
		Long: `Remove a runtime base image to free disk space.

The base name should be the full name as shown in 'yoloai system runtime list'.

Example:
  yoloai system runtime remove yoloai-base-ios-26.4`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			baseName := args[0]
			yes, _ := cmd.Flags().GetBool("yes")

			// Check macOS
			if runtime.GOOS != "darwin" {
				return sandbox.NewUsageError("yoloai system runtime commands are only available on macOS")
			}

			// Check Tart backend
			ctx := cmd.Context()
			available, note := checkBackend(ctx, "tart")
			if !available {
				return sandbox.NewUsageError("Tart backend not available: %s\n\nInstall Tart: brew install cirruslabs/cli/tart", note)
			}

			// Get Tart runtime
			rt, err := newRuntime(ctx, "tart")
			if err != nil {
				return fmt.Errorf("create tart runtime: %w", err)
			}
			defer rt.Close() //nolint:errcheck

			tartRuntime, ok := rt.(*tart.Runtime)
			if !ok {
				return fmt.Errorf("internal error: tart backend type mismatch")
			}

			// Check if base exists
			exists, err := tartRuntime.BaseExists(ctx, baseName)
			if err != nil {
				return fmt.Errorf("check base: %w", err)
			}
			if !exists {
				return sandbox.NewUsageError("Runtime base '%s' not found.\n\nUse 'yoloai system runtime list' to see available bases.", baseName)
			}

			// Get size for display
			bases, err := listRuntimeBases(ctx, tartRuntime)
			if err != nil {
				return fmt.Errorf("list bases: %w", err)
			}
			var size int64
			for _, base := range bases {
				if base.Name == baseName {
					size = base.Size
					break
				}
			}

			// Confirm deletion unless --yes
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "\nThis will delete runtime base '%s' (%s).\n", baseName, formatSize(size)) //nolint:errcheck
				fmt.Fprintf(cmd.OutOrStdout(), "Continue? [y/N]: ")                                                        //nolint:errcheck
				var response string
				fmt.Scanln(&response) //nolint:errcheck,gosec
				if strings.ToLower(strings.TrimSpace(response)) != "y" {
					fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.") //nolint:errcheck
					return nil
				}
			}

			// Delete the base
			fmt.Fprintf(cmd.OutOrStdout(), "\nDeleting %s...\n", baseName) //nolint:errcheck
			if err := deleteBase(ctx, baseName); err != nil {
				return fmt.Errorf("delete base: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Freed %s\n", formatSize(size)) //nolint:errcheck
			return nil
		},
	}

	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")

	return cmd
}

// runtimeBase represents a runtime base image.
type runtimeBase struct {
	Name     string
	CacheKey string // e.g., "ios-26.4-tvos-26.1" (extracted from name)
	Size     int64
}

// listRuntimeBases lists all yoloai-base-* VMs.
func listRuntimeBases(ctx context.Context, tartRuntime *tart.Runtime) ([]runtimeBase, error) {
	// Call tart list to get all VMs
	output, err := runTartCommand(ctx, "list")
	if err != nil {
		return nil, err
	}

	var bases []runtimeBase
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		// tart list output format (columns separated by spaces):
		// NAME  SIZE  DISK  ...
		// Parse the line
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := fields[0]
		if !strings.HasPrefix(name, "yoloai-base") {
			continue
		}

		// Extract cache key from name
		var cacheKey string
		if name != "yoloai-base" {
			cacheKey = strings.TrimPrefix(name, "yoloai-base-")
		}

		// Parse size (format: "20GB" or "36.5GB")
		sizeStr := fields[1]
		size := parseSize(sizeStr)

		bases = append(bases, runtimeBase{
			Name:     name,
			CacheKey: cacheKey,
			Size:     size,
		})
	}

	return bases, nil
}

// runTartCommand runs a tart command and returns stdout.
func runTartCommand(ctx context.Context, args ...string) (string, error) {
	// Use exec to run tart
	cmd := exec.CommandContext(ctx, "tart", args...) //nolint:gosec // G204: args are constructed internally
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// deleteBase deletes a base VM using tart delete.
func deleteBase(ctx context.Context, baseName string) error {
	_, err := runTartCommand(ctx, "delete", baseName)
	return err
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
func formatAvailableRuntimes(runtimes []tart.RuntimeVersion) string {
	// Group by platform, pick latest of each
	latest := make(map[string]tart.RuntimeVersion)
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

// parseSize parses size strings like "20GB", "36.5GB" to bytes.
func parseSize(sizeStr string) int64 {
	// Remove "GB" suffix and parse as float
	sizeStr = strings.TrimSuffix(strings.TrimSpace(sizeStr), "GB")
	var size float64
	fmt.Sscanf(sizeStr, "%f", &size) //nolint:errcheck,gosec // best-effort parse
	return int64(size * 1024 * 1024 * 1024)
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

# Implementation Plan: yoloai system runtime Commands

## Context

We verified that iOS runtime downloading via `xcodebuild -downloadPlatform` works successfully (see `docs/dev/research/ios-runtime-download-verification.md`). The download is slow (~8GB, takes several minutes), so we want to separate runtime base creation from sandbox creation.

**Current behavior:** When `yoloai new --runtime ios` is called, it automatically creates the runtime base if missing. This blocks sandbox creation for several minutes with no clear indication of progress.

**Desired behavior:**
- `yoloai new --runtime ios` should error if the runtime base doesn't exist, with clear instructions to run `yoloai system runtime add ios` first
- Users explicitly create runtime bases with `yoloai system runtime add`, seeing download progress
- Users can list existing bases with `yoloai system runtime list`

**Design reference:** `docs/design/apple-runtime-caching.md` has the complete spec

## Changes Required

### 1. Create new CLI command: `internal/cli/system_runtime.go`

**File:** `internal/cli/system_runtime.go` (new file)

Create three subcommands under `yoloai system runtime`:

#### 1a. Parent command

```go
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
```

Add this to `internal/cli/system.go` in the `newSystemCmd()` function after line 34:
```go
cmd.AddCommand(
    newSystemInfoCmd(version, commit, date),
    newSystemAgentsCmd(),
    newSystemBackendsCmd(),
    newSystemBuildCmd(),
    newSystemCheckCmd(),
    newSystemDoctorCmd(),
    newSystemPruneCmd(),
    newSystemSetupCmd(),
    newSystemRuntimeCmd(),  // ADD THIS LINE
    newCompletionCmd(),
)
```

#### 1b. `yoloai system runtime add <platform>...`

**Behavior:**
- Accepts one or more runtime specifiers: `ios`, `ios:26.4`, `tvos:latest`, etc.
- Resolves "latest" to actual versions by querying host `xcrun simctl list runtimes`
- Generates cache key (e.g., `ios-26.4`, `ios-26.4-tvos-26.1`)
- Creates base name (e.g., `yoloai-base-ios-26.4`)
- If base already exists, error: "Runtime base 'yoloai-base-ios-26.4' already exists"
- If doesn't exist, call `tartRuntime.CreateBase(ctx, baseName, resolved)` with output streaming
- Only works on macOS with Tart backend

**Implementation notes:**
- Reuse `tart.ResolveRuntimeVersions()` from `sandbox/create.go:322`
- Reuse `tart.GenerateCacheKey()` from `sandbox/create.go:328`
- Check existence with `tartRuntime.BaseExists(ctx, baseName)` from `sandbox/create.go:339`
- Call `tartRuntime.CreateBase(ctx, baseName, resolved)` from `sandbox/create.go:347`
- Stream output to stdout (not os.Stderr like in create.go)

**Error cases:**
- Not on macOS: "This command is only available on macOS"
- Tart not available: "Tart backend not available. Install Tart: https://github.com/cirruslabs/tart"
- Invalid runtime spec: "Invalid runtime specifier 'foo'. Valid platforms: ios, tvos, watchos, visionos"
- Runtime not available on host: "Runtime ios:26.5 not found on host. Available: ios:26.4, ios:26.3"
- Base already exists: "Runtime base 'yoloai-base-ios-26.4' already exists. Use 'yoloai system runtime list' to see all bases."

**Example output:**
```
$ yoloai system runtime add ios tvos:26.1

Resolving runtime versions...
  ios (latest) → iOS 26.4
  tvos:26.1 → tvOS 26.1

Creating runtime base: yoloai-base-ios-26.4-tvos-26.1

Cloning from yoloai-base...
Booting VM...
Configuring Xcode...
Downloading iOS 26.4 runtime...
Downloading iOS 26.4 Simulator (23E244) (arm64): 0.1% (9.9 MB of 8.46 GB)
Downloading iOS 26.4 Simulator (23E244) (arm64): 1.2% (99.2 MB of 8.46 GB)
...
Downloading iOS 26.4 Simulator (23E244) (arm64): 100.0% (8.45 GB of 8.46 GB)
Downloading iOS 26.4 Simulator (23E244) (arm64): Installing...
Downloading iOS 26.4 Simulator (23E244) (arm64): Done.

Downloading tvOS 26.1 runtime...
...

Verifying runtimes...
Stopping VM...
Creating snapshot yoloai-base-ios-26.4-tvos-26.1...

Runtime base created successfully (32 GB)
```

#### 1c. `yoloai system runtime list [filters...]`

**Behavior:**
- List all `yoloai-base-*` Tart VMs (those with runtime suffixes)
- Parse base names to extract runtime info (use cache key format)
- Show size, runtime versions
- If filters provided, only show matching bases
- Show what's latest available on host for comparison

**Implementation:**
- Call `tart list` and parse output to find `yoloai-base-*` VMs
- Parse cache key from name (everything after `yoloai-base-`)
- For size, use `tart get <name>` or parse `tart list` output
- Query host with `xcrun simctl list runtimes` to get latest versions
- Apply filters if provided

**Example output:**
```
$ yoloai system runtime list

Runtime Base Images:
  yoloai-base                      (no runtimes, 20 GB)
  yoloai-base-ios-26.4             (iOS 26.4, 36 GB)
  yoloai-base-ios-26.3-tvos-26.1   (iOS 26.3, tvOS 26.1, 52 GB)

Latest available on host: iOS 26.4, tvOS 26.2, watchOS 11.2

Total: 3 bases, 108 GB
```

With filters:
```
$ yoloai system runtime list ios

Runtime Base Images:
  yoloai-base-ios-26.4             (iOS 26.4, 36 GB)
  yoloai-base-ios-26.3-tvos-26.1   (iOS 26.3, tvOS 26.1, 52 GB)

Total: 2 bases, 88 GB
```

#### 1d. `yoloai system runtime remove <base-name>` (simple version)

**Behavior:**
- Accept base name (e.g., `yoloai-base-ios-26.4`)
- Confirm deletion (unless `--yes` flag)
- Call `tart delete <base-name>`
- Show freed space

**Later enhancements** (not required now):
- `--older-than latest` flag
- `--older-than <version>` flag
- `--all` flag

**Example output:**
```
$ yoloai system runtime remove yoloai-base-ios-26.3

This will delete runtime base 'yoloai-base-ios-26.3' (36 GB).
Continue? [y/N]: y

Deleting yoloai-base-ios-26.3...
Freed 36 GB
```

### 2. Modify sandbox creation: `sandbox/create.go`

**Location:** Lines 345-350 in `sandbox/create.go`

**Current code:**
```go
// 5. Create base if missing
if !exists {
    _, _ = fmt.Fprintf(m.output, "Creating runtime base %s...\n", baseName)
    if err := tartRuntime.CreateBase(ctx, baseName, resolved); err != nil {
        return nil, fmt.Errorf("create base: %w", err)
    }
    _, _ = fmt.Fprintf(m.output, "Runtime base %s created\n", baseName)
} else {
    _, _ = fmt.Fprintf(m.output, "Using cached base %s\n", baseName)
}
```

**Replace with:**
```go
// 5. Check base exists; error if not (don't auto-create)
if !exists {
    // Build helpful error message
    runtimeDesc := tart.FormatRuntimeList(resolved) // e.g., "iOS 26.4, tvOS 26.1"

    // Show what was attempted (especially important when "latest" was implied)
    var attemptedSpecs []string
    for _, rt := range resolved {
        attemptedSpecs = append(attemptedSpecs, fmt.Sprintf("%s:%s", rt.Platform, rt.Version))
    }

    return nil, NewUsageError(
        "Runtime base '%s' not found.\n\n"+
        "Requested runtimes: %s\n"+
        "Resolved to: %s\n\n"+
        "To create this runtime base, run:\n"+
        "  yoloai system runtime add %s\n\n"+
        "To see existing runtime bases:\n"+
        "  yoloai system runtime list",
        baseName,
        strings.Join(opts.Runtimes, ", "),
        runtimeDesc,
        strings.Join(attemptedSpecs, " "),
    )
}

_, _ = fmt.Fprintf(m.output, "Using runtime base %s\n", baseName)
```

**Add helper in `runtime/tart/runtime_resolution.go`:**
```go
// FormatRuntimeList returns a human-readable string like "iOS 26.4, tvOS 26.1"
func FormatRuntimeList(runtimes []RuntimeVersion) string {
    var parts []string
    for _, rt := range runtimes {
        parts = append(parts, fmt.Sprintf("%s %s",
            strings.Title(rt.Platform), rt.Version))
    }
    return strings.Join(parts, ", ")
}
```

### 3. Stream xcodebuild output: `runtime/tart/base.go`

**Location:** The `CreateBase()` function in `runtime/tart/base.go`

**Current:** Output goes to the writer passed in, but xcodebuild download output might not be visible

**Change needed:** When running `xcodebuild -downloadPlatform`, capture and stream its output

**Look for the xcodebuild command in CreateBase() and modify:**

Instead of:
```go
cmd := exec.CommandContext(ctx, "xcodebuild", "-downloadPlatform", platform)
cmd.Stderr = output
if err := cmd.Run(); err != nil {
    return err
}
```

Use:
```go
cmd := exec.CommandContext(ctx, "xcodebuild", "-downloadPlatform", platform)
cmd.Stdout = output  // Stream stdout (download progress)
cmd.Stderr = output  // Stream stderr (errors)
if err := cmd.Run(); err != nil {
    return err
}
```

**Note:** xcodebuild outputs progress to stdout/stderr with carriage returns. The terminal will handle the \r updates automatically, so we just need to pass through the output.

### 4. Testing

**Manual testing steps:**

```bash
# 1. Verify command exists
./yoloai system runtime --help

# 2. List when no runtime bases exist
./yoloai system runtime list

# 3. Try to create sandbox without base
./yoloai new test1 --runtime ios --no-start
# Should error with helpful message

# 4. Create runtime base
./yoloai system runtime add ios
# Should show download progress, take several minutes

# 5. List again
./yoloai system runtime list
# Should show yoloai-base-ios-26.4 (or whatever version)

# 6. Now create sandbox should work
./yoloai new test2 --runtime ios --no-start
# Should use cached base, be fast

# 7. Try with explicit version
./yoloai system runtime add ios:26.4 tvos
# If ios:26.4 base exists, should error
# If not, should create combo base

# 8. List with filters
./yoloai system runtime list ios
./yoloai system runtime list tvos

# 9. Remove a base
./yoloai system runtime remove yoloai-base-ios-26.4
# Should prompt for confirmation

# 10. Verify it's gone
./yoloai system runtime list
```

**Unit tests:** Not critical for initial implementation since this is Tart-specific and requires real Tart/Xcode. Focus on getting manual testing working first.

### 5. Edge Cases

**macOS-only check:**
```go
if runtime.GOOS != "darwin" {
    return NewUsageError("yoloai system runtime commands are only available on macOS")
}
```

**Tart backend check:**
```go
backend := "tart"
available, err := checkBackend(ctx, backend)
if err != nil || !available {
    return NewUsageError("Tart backend not available. Install Tart: https://github.com/cirruslabs/tart")
}
```

**Base already exists:**
```go
exists, err := tartRuntime.BaseExists(ctx, baseName)
if err != nil {
    return fmt.Errorf("check base: %w", err)
}
if exists {
    return NewUsageError("Runtime base '%s' already exists.\nUse 'yoloai system runtime list' to see all bases.", baseName)
}
```

**No runtimes specified:**
```go
if len(args) == 0 {
    return NewUsageError("No runtimes specified.\n\nExamples:\n  yoloai system runtime add ios\n  yoloai system runtime add ios tvos\n  yoloai system runtime add ios:26.4 tvos:26.1")
}
```

**Latest resolution message:**
Show users what "latest" resolved to, since they might have meant to create a base for an older version:
```
Resolving runtime versions...
  ios (latest) → iOS 26.4
```

### 6. Code Structure

**Files to create:**
- `internal/cli/system_runtime.go` - new CLI commands

**Files to modify:**
- `internal/cli/system.go` - add runtime subcommand
- `sandbox/create.go` - change auto-create to error
- `runtime/tart/base.go` or wherever CreateBase is - stream xcodebuild output

**Helper functions to add in `runtime/tart/`:**
- `FormatRuntimeList([]RuntimeVersion) string` - for error messages
- Possibly helpers for listing bases, parsing cache keys if not already present

### 7. Output Examples

**When user needs to create a base:**
```
$ yoloai new test1 --runtime ios

Error: Runtime base 'yoloai-base-ios-26.4' not found.

Requested runtimes: ios
Resolved to: iOS 26.4

To create this runtime base, run:
  yoloai system runtime add ios:26.4

To see existing runtime bases:
  yoloai system runtime list
```

**When user creates base:**
```
$ yoloai system runtime add ios

Resolving runtime versions...
  ios (latest) → iOS 26.4

Creating runtime base: yoloai-base-ios-26.4

Cloning from yoloai-base...
Booting VM...
Configuring Xcode...
Downloading iOS 26.4 runtime...
Downloading iOS 26.4 Simulator (23E244) (arm64): 0.1% (9.9 MB of 8.46 GB)
Downloading iOS 26.4 Simulator (23E244) (arm64): 15.7% (1.33 GB of 8.46 GB)
Downloading iOS 26.4 Simulator (23E244) (arm64): 34.2% (2.89 GB of 8.46 GB)
...
Downloading iOS 26.4 Simulator (23E244) (arm64): 100.0% (8.45 GB of 8.46 GB)
Downloading iOS 26.4 Simulator (23E244) (arm64): Installing...
Downloading iOS 26.4 Simulator (23E244) (arm64): Done.

Verifying runtime...
Stopping VM...
Creating snapshot yoloai-base-ios-26.4...

Runtime base created successfully (36 GB)
```

### 8. Quality Gate

Before committing, run:
```bash
make check
```

All checks must pass.

### 9. Commit Message Template

```
Feat: implement 'yoloai system runtime' commands

Add CLI commands for managing Apple simulator runtime base images:
- yoloai system runtime add <platform>... - pre-create runtime bases
- yoloai system runtime list [filters...] - show cached bases
- yoloai system runtime remove <base> - delete bases

Change yoloai new --runtime behavior to error if base doesn't exist
instead of auto-creating. This gives users visibility into the slow
download process and allows pre-warming runtime bases.

Key changes:
- internal/cli/system_runtime.go: new CLI commands
- internal/cli/system.go: add runtime subcommand
- sandbox/create.go: error with helpful message if base not found
- runtime/tart/base.go: stream xcodebuild download progress

Users now explicitly create runtime bases with progress visibility:
  yoloai system runtime add ios tvos

Then create sandboxes quickly with cached bases:
  yoloai new test --runtime ios

Implements design from docs/design/apple-runtime-caching.md

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>
```

### 10. Implementation Priority

**Core functionality (must have):**
1. `system runtime add` - create bases with progress
2. `system runtime list` - show what exists
3. Error in `yoloai new` when base missing
4. Stream xcodebuild output

**Nice to have (can defer):**
1. `system runtime remove` with `--older-than` flags
2. JSON output mode for all commands
3. Advanced filtering in list
4. Partial base reuse (design mentions reusing ios base when creating ios+tvos)

**Start with core functionality.** The basic add/list/error flow is enough to improve UX dramatically.

## Questions Already Answered

**Q: Should we use xcodebuild -downloadPlatform or copy from VirtioFS?**
A: Use xcodebuild -downloadPlatform. The ditto copy approach produces incomplete runtimes (see docs/dev/backend-idiosyncrasies.md and docs/dev/research/ios-runtime-download-verification.md).

**Q: Does xcodebuild show progress?**
A: Yes, it outputs percentage and size to stdout/stderr using carriage returns. We just need to stream it to the terminal.

**Q: What if user specifies ios but latest is different than what's cached?**
A: The error message shows both what was requested and what it resolved to, making it clear. User can then decide whether to create the new base or use an existing one.

**Q: Should we auto-create bases during yoloai new?**
A: No. The download is too slow (8GB, several minutes) and gives no indication of progress. Users should explicitly create bases with visible progress.

## References

- Design: `docs/design/apple-runtime-caching.md`
- Verification: `docs/dev/research/ios-runtime-download-verification.md`
- Idiosyncrasies: `docs/dev/backend-idiosyncrasies.md` (ditto vs download section)
- Current runtime code: `runtime/tart/` directory
- Current base creation: `sandbox/create.go` lines 310-357

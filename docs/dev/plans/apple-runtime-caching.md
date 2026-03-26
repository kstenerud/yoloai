# Apple Runtime Caching Implementation Plan

**Design:** `docs/design/apple-runtime-caching.md`
**Status:** Not Started
**Last updated:** 2026-03-26

## Overview

Implementation plan for Apple runtime base image caching feature. Enables sandboxes to share cached base images containing iOS/tvOS/watchOS/visionOS simulator runtimes, eliminating the need to copy runtimes into each new sandbox.

## Phases

### Phase 1: Core Functionality (MVP)

**Goal:** Basic runtime caching works end-to-end

1. **Add `--runtime` flag to `yoloai new` command**
   - File: `internal/cli/commands.go`
   - Add flag parsing and validation (ios, tvos, watchos, visionos)
   - Pass to Create() call

2. **Runtime name normalization and version parsing**
   - File: `runtime/tart/runtime.go` (new)
   - Function: `ParseRuntime(input string) (platform, version string, err error)`
   - Parse format: `platform[:version]` (e.g., "ios", "ios:latest", "ios:26.1")
   - Case-insensitive platform: ios/iOS/IOS → "ios"
   - Version `:latest` treated same as omitted (both query host for latest)
   - Validation: reject unknown platforms and non-existent versions
   - Error immediately if requested version not available on host
   - Use `github.com/hashicorp/go-version` for version comparison
   - Parse simctl JSON output (schema documented in design doc)

3. **Cache key generation**
   - File: `runtime/tart/runtime.go`
   - Function: `GenerateCacheKey(runtimes []RuntimeVersion) string`
   - Input: Runtime+version pairs (e.g., [{platform: "ios", version: "26.2"}, {platform: "tvos", version: "26.1"}])
   - Sort by platform alphabetically, then by version within platform
   - Format each as `<platform>-<version>`, join with hyphens
   - Example: [{tvos:26.1}, {ios:26.2}] → "ios-26.2-tvos-26.1"

4. **Base image locking**
   - File: `runtime/tart/base_lock.go` (new, similar to `sandbox/lock_unix.go`)
   - Function: `acquireBaseLock(baseName string) (func(), error)`
   - Uses `flock(2)` for exclusive advisory locks on Unix/macOS
   - Lock file: `~/.yoloai/tart-base-locks/<base-name>.lock`
   - Blocks until lock is available (serializes concurrent base creation)
   - Auto-releases on process exit/crash

5. **Base image cache lookup**
   - File: `runtime/tart/tart.go`
   - Function: `findCachedBase(cacheKey string) (string, bool)`
   - Check if `yoloai-base-<cacheKey>` VM exists
   - Return base name if found

6. **Runtime copying implementation**
   - File: `runtime/tart/runtime_copy.go` (new)
   - Function: `CopyRuntimeToVM(vmName, platform, version string) error`
   - Find runtime on host mount
   - Copy with ditto + Info.plist fixup
   - Verify with simctl

7. **Base image snapshotting and error recovery**
   - File: `runtime/tart/tart.go`
   - Function: `snapshotAsBase(tempVM, baseName string) error`
   - Clone temp VM to new base name
   - Handle errors: clean partial base if snapshot fails
   - Function: `cleanupTempVM(vmName string) error` - best-effort cleanup
   - Temp VM naming: `yoloai-base-<cacheKey>-tmp-<random>`
   - Use defer for cleanup on any error
   - Log creation with timestamps

8. **Integrate into Create() flow**
   - File: `sandbox/create.go`
   - Add base image resolution logic before calling runtime.Create()
   - Acquire base lock, check cache, create if missing, release lock
   - Pass resolved base name to runtime.Create()
   - Report progress to user

**Acceptance criteria:**
- `yoloai new test --runtime ios` creates base and sandbox
- Second `yoloai new test2 --runtime ios` reuses cached base
- Both sandboxes have working iOS simulator
- Concurrent `yoloai new` with same runtime blocks second process until first completes
- Second process discovers base exists and uses it (no duplicate work)

### Phase 2: Smart Reuse

**Goal:** Minimize redundant copying via parent selection

9. **Parent base selection**
   - File: `runtime/tart/runtime.go`
   - Function: `FindBestParentBase(runtimes []string) string`
   - List existing bases, parse their runtimes
   - Calculate overlap, return best match
   - Fallback to `yoloai-base`

10. **Incremental runtime copying**
    - File: `runtime/tart/runtime_copy.go`
    - Modify `CopyRuntimeToVM()` to check what's already present
    - Only copy missing runtimes
    - Report: "Reusing iOS from parent, copying tvOS..."

11. **Cache metadata tracking**
    - File: `runtime/tart/metadata.go` (new)
    - Struct: `BaseMetadata` (version=0, base_name, runtimes, created_at, yoloai_version)
    - Functions: `SaveMetadata()`, `LoadMetadata()`
    - Location: `~/.yoloai/tart-base-metadata/<base>.json`
    - Schema version 0 for now (TODO: assign proper version at yoloAI 1.0)
    - No disk_size field (compute on-demand to avoid staleness)
    - Atomic writes (write to temp file, then rename)

**Acceptance criteria:**
- `yoloai new test --runtime ios --runtime tvos` reuses `yoloai-base-ios` if it exists
- Only tvOS is copied (iOS reused)
- Metadata correctly tracks both runtimes

### Phase 3: Management Commands

**Goal:** User-friendly cache inspection and cleanup

**Note:** Commands only registered on macOS/Darwin hosts.

12. **`yoloai system runtime` base command**
    - File: `internal/cli/system.go`
    - Add runtime subcommand group
    - Platform check: only register on darwin
    - Help text explains Apple runtime caching

13. **`yoloai system runtime list [runtime[:version]...]` command**
    - File: `internal/cli/system_runtime.go` (new)
    - List all `yoloai-base-*` VMs (default: show all)
    - Load and display metadata
    - Show disk sizes, runtimes, versions
    - Optional positional filters (multiple):
      - Platform only: `list ios` → bases with iOS (any version)
      - Platform+version: `list ios:26.2` → bases with iOS 26.2
      - Version only: `list 26.2` → bases with any runtime at 26.2
      - Multiple filters: `list ios tvos:26.0` → bases with iOS (any) AND tvOS 26.0
    - Query host for latest available runtimes (shown in output)

14. **`yoloai system runtime add <runtime[:version]>...` command**
    - File: `internal/cli/system_runtime.go`
    - Pre-create runtime base without sandbox
    - Accept one or more runtime arguments: `add ios tvos`
    - Support version syntax: `add ios:26.1 tvos:26.2`
    - Default to latest if no version specified
    - Reuse existing cache creation logic (including base locking)
    - Useful for CI setup, team onboarding

15. **`yoloai system runtime remove [runtime[:version]]` command**
    - File: `internal/cli/system_runtime.go`
    - Remove runtime bases with filters
    - Optional positional filter: runtime (e.g., `remove ios:26.1`)
    - Flags: `--older-than`, `--all`, `--dry-run`, `--yes`
    - `--older-than` accepts version or platform:version (repeatable)
    - Parse "latest" as special version (queries host for latest)
    - Check for sandbox usage before removing
    - Confirm before deletion (unless `--yes`)

**Acceptance criteria:**
- `yoloai system runtime list` shows all bases with details
- `yoloai system runtime list ios` filters to bases with iOS (any version)
- `yoloai system runtime list ios:26.2` filters to bases with iOS 26.2
- `yoloai system runtime list 26.2` filters to bases with any runtime at version 26.2
- `yoloai system runtime list ios tvos:26.0` filters to bases with iOS (any) AND tvOS 26.0
- `yoloai system runtime add ios` creates base with latest iOS
- `yoloai system runtime add ios:26.1 tvos:26.2` creates base with specific versions
- `yoloai system runtime remove ios:26.1` removes specific runtime bases
- `yoloai system runtime remove --older-than latest` removes outdated bases
- `yoloai system runtime remove --older-than 26.2` removes bases older than 26.2
- `yoloai system runtime remove --older-than ios:latest` removes bases with old iOS
- Commands only visible in help on macOS hosts

### Phase 4: Polish and Optimization

16. **Progress reporting**
    - Show spinner during long operations
    - Report: "Copying iOS runtime (15GB, ~2 min)..."
    - Update progress during copy
    - Final summary: "Saved 3 min on future sandboxes!"

17. **Error recovery and edge cases**
    - **Non-existent runtime version**: Error immediately if requested version not available on host
      - Example: `--runtime ios:99.0` → "iOS 99.0 not available on host, latest is 26.2"
      - Query host runtimes during flag validation
    - **Partial failure cleanup**: Undo anything created in the current command (best effort)
      - Temp VM cleanup via defer (already in step 7)
      - Delete partial base if snapshot fails
      - Leave parent bases alone (they're valid, even if unused)
    - **Corrupted metadata**: Delete base and rebuild from scratch
      - Don't attempt to repair or rebuild metadata
      - Log warning and proceed with rebuild
      - User can manually recreate with `yoloai system runtime add`
    - **Orphaned temp VMs**: Add to `yoloai system prune` command
      - Find VMs matching `yoloai-base-*-tmp-*` pattern
      - Safe to delete automatically

## Files to Create

- `runtime/tart/runtime.go` - Runtime parsing, normalization, cache key generation, version comparison
- `runtime/tart/runtime_copy.go` - Runtime copying logic
- `runtime/tart/metadata.go` - Base metadata tracking
- `runtime/tart/base_lock.go` - Base image locking (similar to `sandbox/lock_unix.go`)
- `internal/cli/system_runtime.go` - Runtime management commands

## Files to Modify

- `internal/cli/commands.go` - Add `--runtime` flag to `new` command
- `runtime/tart/tart.go` - Add helper functions for runtime operations
- `sandbox/create.go` - Orchestrate base image resolution and creation
- `internal/cli/system.go` - Register runtime subcommand (darwin only):
  ```go
  if runtime.GOOS == "darwin" {
      cmd.AddCommand(newSystemRuntimeCmd())
  }
  ```
- `config/dirs.go` - Add metadata and lock directory helpers:
  ```go
  func TartBaseMetadataDir() string {
      return filepath.Join(YoloaiDir(), "tart-base-metadata")
  }

  func TartBaseLocksDir() string {
      return filepath.Join(YoloaiDir(), "tart-base-locks")
  }
  ```

## Architecture Notes

**Base Image Creation Flow:**
1. Sandbox package (`sandbox/create.go`) orchestrates the high-level flow
2. Tart package (`runtime/tart/`) provides low-level mechanics
3. Separation allows platform-specific logic to stay in runtime backend

**Runtime Discovery:**
- Must happen on **host**, not in VM
- CoreSimulator cannot discover runtimes from VirtioFS mounts
- Use `xcrun simctl list runtimes --json` on host before VM creation

**Snapshotting:**
- Verified: `tart clone` successfully snapshots VM state
- System files (like `/Library/Developer/CoreSimulator/...`) persist through cloning
- Stop VM before cloning to ensure all changes are flushed to disk

**Testing:**
- Unit tests: Version parsing, cache key generation, parent selection (no Tart needed)
- Integration tests: Add to existing smoke test suite (`make smoke-test`)
- Mock simctl output for unit testing runtime discovery
- End-to-end test: Create sandbox with `--runtime ios`, verify it works
- Concurrency test: Launch two `yoloai new` with same runtime, verify locking works

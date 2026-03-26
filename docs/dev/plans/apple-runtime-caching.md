# Apple Runtime Caching Implementation Plan

**Design:** `docs/design/apple-runtime-caching.md`
**Status:** Not Started
**Last updated:** 2026-03-26

## Overview

Implementation plan for Apple runtime base image caching feature. Enables sandboxes to share cached base images containing iOS/tvOS/watchOS/visionOS simulator runtimes, eliminating the need to copy runtimes into each new sandbox.

## Phases

### Phase 1: Core Functionality (MVP)

**Goal:** Basic runtime caching works end-to-end

1. **Add `--apple-runtime` flag to `yoloai new` command**
   - File: `internal/cli/commands.go`
   - Add flag parsing and validation
   - Pass to Create() call

2. **Runtime name normalization and version parsing**
   - File: `runtime/tart/runtime.go` (new)
   - Function: `ParseRuntime(input string) (platform, version string, err error)`
   - Parse format: `platform[:version]` (e.g., "ios", "ios:latest", "ios:26.1")
   - Case-insensitive platform: ios/iOS/IOS → "ios"
   - Version `:latest` treated same as omitted (both query host for latest)
   - Validation: reject unknown platforms

3. **Cache key generation**
   - File: `runtime/tart/runtime.go`
   - Function: `GenerateCacheKey(runtimes []string) string`
   - Sort runtimes alphabetically
   - Join with hyphens: ["tvos", "ios"] → "ios-tvos"

4. **Base image cache lookup**
   - File: `runtime/tart/tart.go`
   - Function: `findCachedBase(cacheKey string) (string, bool)`
   - Check if `yoloai-base-<cacheKey>` VM exists
   - Return base name if found

5. **Runtime copying implementation**
   - File: `runtime/tart/runtime_copy.go` (new)
   - Function: `CopyRuntimeToVM(vmName, platform, version string) error`
   - Find runtime on host mount
   - Copy with ditto + Info.plist fixup
   - Verify with simctl

6. **Base image snapshotting**
   - File: `runtime/tart/tart.go`
   - Function: `snapshotAsBase(tempVM, baseName string) error`
   - Clone temp VM to new base name
   - Delete temp VM
   - Log creation

7. **Integrate into Create() flow**
   - File: `runtime/tart/tart.go`
   - Modify `Create()` to handle runtime flags
   - Check cache → create if missing → clone from cache
   - Report progress to user

**Acceptance criteria:**
- `yoloai new test --apple-runtime ios` creates base and sandbox
- Second `yoloai new test2 --apple-runtime ios` reuses cached base
- Both sandboxes have working iOS simulator

### Phase 2: Smart Reuse

**Goal:** Minimize redundant copying via parent selection

8. **Parent base selection**
   - File: `runtime/tart/runtime.go`
   - Function: `FindBestParentBase(runtimes []string) string`
   - List existing bases, parse their runtimes
   - Calculate overlap, return best match
   - Fallback to `yoloai-base`

9. **Incremental runtime copying**
   - File: `runtime/tart/runtime_copy.go`
   - Modify `CopyRuntimeToVM()` to check what's already present
   - Only copy missing runtimes
   - Report: "Reusing iOS from parent, copying tvOS..."

10. **Cache metadata tracking**
    - File: `runtime/tart/metadata.go` (new)
    - Struct: `BaseMetadata` (runtimes, created, disk_size)
    - Functions: `SaveMetadata()`, `LoadMetadata()`
    - Location: `~/.yoloai/tart-base-metadata/<base>.json`

**Acceptance criteria:**
- `yoloai new test --apple-runtime ios --apple-runtime tvos` reuses `yoloai-base-ios` if it exists
- Only tvOS is copied (iOS reused)
- Metadata correctly tracks both runtimes

### Phase 3: Management Commands

**Goal:** User-friendly cache inspection and cleanup

**Note:** Commands only registered on macOS/Darwin hosts.

11. **`yoloai system runtime` base command**
    - File: `internal/cli/system.go`
    - Add runtime subcommand group
    - Platform check: only register on darwin
    - Help text explains Apple runtime caching

12. **`yoloai system runtime list [runtime[:version]...]` command**
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

13. **`yoloai system runtime add <runtime[:version]>...` command**
    - File: `internal/cli/system_runtime.go`
    - Pre-create runtime base without sandbox
    - Accept one or more runtime arguments: `add ios tvos`
    - Support version syntax: `add ios:26.1 tvos:26.2`
    - Default to latest if no version specified
    - Reuse existing cache creation logic
    - Useful for CI setup, team onboarding

14. **`yoloai system runtime remove [runtime[:version]]` command**
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

15. **Runtime version specifiers**
    - Support `--apple-runtime ios:26.1`
    - Parse version from flag
    - Match against runtime Info.plist
    - Include version in cache key

16. **Progress reporting**
    - Show spinner during long operations
    - Report: "Copying iOS runtime (15GB, ~2 min)..."
    - Update progress during copy
    - Final summary: "Saved 3 min on future sandboxes!"

17. **Error recovery**
    - Cleanup temp VMs on failure
    - Partial base creation → delete incomplete base
    - Retry logic for transient failures
    - Clear error messages with next steps

## Files to Create

- `runtime/tart/runtime.go` - Runtime parsing, normalization, cache key generation
- `runtime/tart/runtime_copy.go` - Runtime copying logic
- `runtime/tart/metadata.go` - Base metadata tracking
- `internal/cli/system_runtime.go` - Runtime management commands

## Files to Modify

- `internal/cli/commands.go` - Add `--apple-runtime` flag to `new` command
- `runtime/tart/tart.go` - Integrate caching into Create() flow, add snapshotting
- `internal/cli/system.go` - Register runtime subcommand (darwin only)

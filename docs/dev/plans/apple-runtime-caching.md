# Apple Runtime Caching Implementation Plan

**Design:** `docs/design/apple-runtime-caching.md`
**Status:** Not Started
**Last updated:** 2026-03-26

## Overview

Implementation plan for Apple runtime base image caching feature. Enables sandboxes to share cached base images containing iOS/tvOS/watchOS/visionOS simulator runtimes, eliminating the need to copy runtimes into each new sandbox.

## Core Types

**RuntimeVersion struct** (used throughout implementation):
```go
// RuntimeVersion represents a resolved Apple simulator runtime
type RuntimeVersion struct {
    Platform string // "ios", "tvos", "watchos", "visionos" (lowercase, normalized)
    Version  string // "26.2", "26.1" (semantic version string)
    Build    string // "23B86" (build identifier, for tie-breaking)
}
```

## Phases

### Phase 1: Core Functionality (MVP)

**Goal:** Basic runtime caching works end-to-end

1. **Add `--runtime` flag to `yoloai new` command**
   - File: `internal/cli/commands.go`
   - Cobra flag declaration:
     ```go
     cmd.Flags().StringArray("runtime", []string{},
       "Apple simulator runtime (ios, tvos, watchos, visionos). Repeatable. Example: --runtime ios --runtime tvos:26.1")
     ```
   - Parse flag values, validate format (platform[:version])
   - Pass runtime strings to sandbox.Create() via new field in CreateOptions

2. **Runtime name normalization and version parsing**
   - File: `runtime/tart/runtime.go` (new)
   - Function: `ParseRuntime(input string) (platform, version string, err error)`
     - Parse format: `platform[:version]` (e.g., "ios", "ios:latest", "ios:26.1")
     - Case-insensitive platform: ios/iOS/IOS → "ios"
     - Version `:latest` treated same as omitted (both defer to QueryAvailableRuntimes)
     - Validation: reject unknown platforms (must be one of: ios, tvos, watchos, visionos)

   - Function: `QueryAvailableRuntimes() ([]RuntimeVersion, error)`
     - **Runs on HOST** (not in VM) - CoreSimulator can't see VirtioFS mounts
     - Execute: `xcrun simctl list runtimes --json`
     - Parse JSON schema (see design doc for structure)
     - Filter by `isAvailable: true`
     - Return slice of RuntimeVersion structs

   - Function: `ResolveRuntimeVersions(inputs []string) ([]RuntimeVersion, error)`
     - Query available runtimes once via QueryAvailableRuntimes()
     - For each input, parse with ParseRuntime()
     - If version omitted/":latest", pick latest by semantic version
     - If version specified, match exact version or error
     - Error immediately if requested version not available: "iOS 26.2 not found on host, latest is 26.1"
     - Use `github.com/hashicorp/go-version` for version comparison
     - Return resolved RuntimeVersion slice

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
   - Check if `yoloai-base-<cacheKey>` VM exists using `tart list`
   - Execute: `tart list` and parse output for VM name match
   - Return (baseName, true) if found, ("", false) if not
   - Handle errors: if `tart list` fails, return error (don't silently assume missing)

6. **Runtime copying implementation**
   - File: `runtime/tart/runtime_copy.go` (new)
   - Function: `CopyRuntimeToVM(vmName string, runtime RuntimeVersion) error`
   - Prerequisites:
     - VM must be running
     - Runtime directory must be mounted via VirtioFS (see existing Xcode mount logic in `runtime/tart/tart.go`)
     - Mount: `/Library/Developer/CoreSimulator/Volumes/` from host → accessible in VM at `/Volumes/My Shared Files/m-Volumes/`

   - Find runtime bundle path:
     - Query simctl on host for bundlePath (already have from ResolveRuntimeVersions)
     - Extract relative path from `/Library/Developer/CoreSimulator/Volumes/`
     - Example: `iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime`

   - Copy runtime (execute via `tart exec <vmName> -- <command>`):
     ```bash
     # Create target directory
     sudo mkdir -p /Library/Developer/CoreSimulator/Profiles/Runtimes

     # Copy runtime bundle from VirtioFS mount to local VM storage
     sudo ditto \
       "/Volumes/My Shared Files/m-Volumes/<runtime-path>" \
       "/Library/Developer/CoreSimulator/Profiles/Runtimes/"

     # Fix Info.plist (ditto may fail on this due to permissions)
     sudo cp \
       "/Volumes/My Shared Files/m-Volumes/<runtime-path>/Contents/Info.plist" \
       "/Library/Developer/CoreSimulator/Profiles/Runtimes/<runtime-name>.simruntime/Contents/"
     ```

   - Verify runtime visible (execute in VM):
     ```bash
     xcrun simctl list runtimes | grep "<Platform> <Version>"
     ```
   - If verification fails, return error with helpful message

7. **Base image snapshotting and error recovery**
   - File: `runtime/tart/tart.go`

   - Function: `snapshotAsBase(tempVM, baseName string) error`
     - Stop temp VM first: `tart stop <tempVM>` (ensures all changes flushed to disk)
     - Clone to new base: `tart clone <tempVM> <baseName>`
     - If clone fails and partial base exists, delete it: `tart delete <baseName>`
     - Return error with context

   - Function: `cleanupTempVM(vmName string) error`
     - Best-effort cleanup (never fails)
     - Stop VM: `tart stop <vmName>` (ignore errors)
     - Delete VM: `tart delete <vmName>` (ignore errors)
     - Log any errors at debug level

   - Temp VM naming convention:
     - Format: `yoloai-base-<cacheKey>-tmp-<random>`
     - Random: 6 hex characters from crypto/rand
     - Example: `yoloai-base-ios-26.2-tmp-a3f7b2`

   - Error recovery pattern in base creation:
     ```go
     func createBase(...) error {
         tempVM := generateTempVMName(cacheKey)
         defer cleanupTempVM(tempVM) // Always cleanup temp VM

         // Clone, copy, snapshot...
         // Any error triggers defer cleanup
     }
     ```

8. **Integrate into Create() flow**
   - File: `sandbox/create.go`

   - Add new field to CreateOptions:
     ```go
     type CreateOptions struct {
         // ... existing fields
         Runtimes []string // Apple simulator runtimes (e.g., ["ios", "tvos:26.1"])
     }
     ```

   - Add base image resolution before calling runtime.Create():
     ```go
     func Create(ctx context.Context, name string, opts CreateOptions) error {
         // ... existing validation

         // Resolve Apple runtime base (if --runtime flags provided)
         var baseName string = "yoloai-base" // default
         if len(opts.Runtimes) > 0 {
             // 1. Resolve runtime versions (query host for latest)
             resolved, err := tart.ResolveRuntimeVersions(opts.Runtimes)
             if err != nil {
                 return fmt.Errorf("resolve runtimes: %w", err)
             }

             // 2. Generate cache key
             cacheKey := tart.GenerateCacheKey(resolved)
             baseName = "yoloai-base-" + cacheKey

             // 3. Acquire lock (blocks if another process is creating)
             release, err := tart.AcquireBaseLock(baseName)
             if err != nil {
                 return fmt.Errorf("acquire base lock: %w", err)
             }
             defer release()

             // 4. Check if base exists
             exists, err := tart.BaseExists(baseName)
             if err != nil {
                 return fmt.Errorf("check base: %w", err)
             }

             // 5. Create base if missing
             if !exists {
                 fmt.Printf("Creating runtime base %s...\n", baseName)
                 if err := tart.CreateBase(ctx, baseName, resolved); err != nil {
                     return fmt.Errorf("create base: %w", err)
                 }
             } else {
                 fmt.Printf("Using cached base %s\n", baseName)
             }
         }

         // Pass resolved base name to runtime.Create()
         // Note: runtime.Create() signature already supports base parameter
         return rt.Create(ctx, name, baseName, opts)
     }
     ```

   - Report progress to user throughout (see Phase 4 step 16)

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
   - Function: `FindBestParentBase(requested []RuntimeVersion) (string, error)`
   - List all `yoloai-base-*` VMs using `tart list`
   - For each base:
     - Load metadata from `~/.yoloai/tart-base-metadata/<base>.json`
     - If metadata missing/corrupt, parse runtimes from VM name as fallback
     - Check if base contains ALL requested runtimes (exact version match)
     - If exact match found, return base name immediately
   - If no exact match, find base with most overlapping runtimes:
     - Count exact runtime+version matches
     - Tiebreaker: prefer base with fewer total runtimes (minimize base size)
     - Second tiebreaker: alphabetically earlier base name
   - Fallback: return `yoloai-base` if no bases have any overlap
   - Algorithm:
     ```
     1. exactMatch = find base where base.runtimes ⊇ requested
     2. If exactMatch: return exactMatch (no copying needed!)
     3. Else: find base with max overlap, min total size
     4. Fallback: "yoloai-base"
     ```

10. **Incremental runtime copying**
    - File: `runtime/tart/runtime_copy.go`
    - Function: `GetInstalledRuntimes(vmName string) ([]RuntimeVersion, error)`
      - Execute in VM: `xcrun simctl list runtimes --json`
      - Parse JSON output to get installed runtimes
      - VM must be running for this check

    - Modify `CopyRuntimeToVM()` to check what's already present:
      ```go
      func CopyRuntimesToVM(vmName string, requested []RuntimeVersion) error {
          // Check what's already installed
          installed, err := GetInstalledRuntimes(vmName)
          if err != nil {
              return err
          }

          // Only copy missing runtimes
          for _, rt := range requested {
              if contains(installed, rt) {
                  fmt.Printf("Reusing %s %s from parent\n", rt.Platform, rt.Version)
                  continue
              }
              fmt.Printf("Copying %s %s runtime...\n", rt.Platform, rt.Version)
              if err := copyRuntime(vmName, rt); err != nil {
                  return err
              }
          }
          return nil
      }
      ```

11. **Cache metadata tracking**
    - File: `runtime/tart/metadata.go` (new)

    - Struct definition:
      ```go
      type BaseMetadata struct {
          Version        int              `json:"version"`         // Schema version (currently 0)
          BaseName       string           `json:"base_name"`       // e.g., "yoloai-base-ios-26.2"
          Runtimes       []RuntimeVersion `json:"runtimes"`        // Installed runtimes
          CreatedAt      time.Time        `json:"created_at"`      // RFC3339 format in JSON
          YoloAIVersion  string           `json:"yoloai_version"`  // e.g., "0.5.0"
          // Note: No disk_size field - compute on-demand with `tart get` to avoid staleness
      }
      ```

    - Function: `SaveMetadata(baseName string, runtimes []RuntimeVersion) error`
      - Construct BaseMetadata struct (version=0, TODO: proper version at 1.0)
      - Get yoloAI version from build-time variable (e.g., `version.Version`)
      - Marshal to JSON with indentation
      - Atomic write: write to temp file, then rename
      - Location: `~/.yoloai/tart-base-metadata/<baseName>.json`
      - Create directory if missing

    - Function: `LoadMetadata(baseName string) (*BaseMetadata, error)`
      - Read from `~/.yoloai/tart-base-metadata/<baseName>.json`
      - If file doesn't exist, return nil, os.ErrNotExist
      - If file corrupt, return error (caller handles: delete base and rebuild)
      - Unmarshal JSON, validate schema version
      - Return parsed metadata

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
    - List all `yoloai-base-*` VMs using `tart list`
    - For each base:
      - Load metadata from `~/.yoloai/tart-base-metadata/<base>.json`
      - If metadata missing/corrupt: show "unknown runtimes" or parse from VM name
      - Get disk size: execute `tart get <base>` and parse "Disk" field from output
    - Optional positional filters (multiple, AND logic):
      - Platform only: `list ios` → bases with iOS (any version)
      - Platform+version: `list ios:26.2` → bases with iOS 26.2
      - Version only: `list 26.2` → bases with any runtime at 26.2
      - Multiple filters: `list ios tvos:26.0` → bases with iOS (any) AND tvOS 26.0
    - Query host for latest available runtimes via QueryAvailableRuntimes()
    - Display in table format:
      ```
      Runtime Base Images:
        yoloai-base-ios-26.1              (iOS 26.1, 25.3GB)
        yoloai-base-ios-26.2-tvos-26.1    (iOS 26.2, tvOS 26.1, 35.8GB)

      Latest available on host: iOS 26.2, tvOS 26.2
      Total cache size: 61GB
      ```

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
    - Parse "latest" as special version (queries host via QueryAvailableRuntimes)

    - Check for sandbox usage before removing:
      - Note: Currently no base tracking in sandbox metadata
      - Proposed approach: Warn that sandboxes may be using these bases
      - Future: Add "base_image" field to sandbox meta.json for proper tracking
      - For now: Show warning, require --yes confirmation

    - Confirm before deletion (unless `--yes`):
      - Show list of bases to be deleted with sizes
      - Prompt: "Remove? [y/N]"
      - Execute `tart delete <base>` for each
      - Delete corresponding metadata file

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
    - **Non-existent runtime version**: (handled in Phase 1 step 2)
      - ResolveRuntimeVersions() errors immediately if version not available
      - Example: `--runtime ios:99.0` → "iOS 99.0 not available on host, latest is 26.2"

    - **Partial failure cleanup**: (pattern established in Phase 1 step 7)
      - All base creation uses defer cleanupTempVM()
      - If snapshot fails, delete partial base in snapshotAsBase()
      - Parent bases never deleted (they're valid, even if unused)

    - **Corrupted metadata**:
      - LoadMetadata() returns error if JSON malformed
      - Caller (e.g., FindBestParentBase) handles:
        - Log warning: "Corrupted metadata for <base>, ignoring"
        - Fallback: parse runtimes from VM name
        - Or: delete base and rebuild with proper metadata
      - User can manually recreate with `yoloai system runtime add`

    - **Orphaned temp VMs**:
      - Add cleanup to existing `yoloai system prune` command
      - File: `internal/cli/system.go` (modify prune command)
      - Find VMs matching pattern: `yoloai-base-*-tmp-*`
      - Safe to delete automatically (they're always incomplete/abandoned)
      - Show in prune output: "Removing 2 orphaned base VMs (5GB)..."

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

**Unit tests** (no Tart/simctl needed):
- File: `runtime/tart/runtime_test.go`
  - ParseRuntime: valid/invalid formats, case insensitivity
  - GenerateCacheKey: sorting, version ordering
  - FindBestParentBase: exact match, overlap calculation, tiebreakers
- File: `runtime/tart/metadata_test.go`
  - SaveMetadata/LoadMetadata: roundtrip, corrupt JSON handling
- Mock simctl output using interface:
  ```go
  type RuntimeQuerier interface {
      QueryAvailableRuntimes() ([]RuntimeVersion, error)
  }
  // Production: executes xcrun simctl
  // Tests: returns hardcoded test data
  ```

**Integration tests** (require Tart):
- File: `smoke/runtime_test.go` (add to existing smoke test suite)
- Run via `make smoke-test` (already exists)
- Tests:
  1. Create sandbox with `--runtime ios`, verify iOS simulator works
  2. Create second sandbox with same runtime, verify base reuse (faster)
  3. Create sandbox with multiple runtimes, verify all present
  4. Verify base locking: launch two `yoloai new` concurrently with same runtime,
     verify second blocks and reuses (no duplicate work)

**End-to-end test checklist:**
- `yoloai new test --runtime ios` creates base and sandbox
- `xcrun simctl list runtimes` in sandbox shows iOS runtime
- Second `yoloai new test2 --runtime ios` reuses base (takes <1 min)
- `yoloai system runtime list` shows created bases
- `yoloai system runtime remove ios:26.1` removes specific bases

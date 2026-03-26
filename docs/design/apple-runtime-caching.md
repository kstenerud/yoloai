# Apple Runtime Base Image Caching

**Status:** Design Proposal
**Last updated:** 2026-03-26
**Related:** `docs/design/ios-testing.md`

## Problem Statement

Currently, iOS/tvOS/watchOS/visionOS simulator testing requires users to manually copy runtimes into each sandbox after creation. This is tedious and slow:

**Current workflow:**
```bash
yoloai new test1
yoloai exec test1 -- sudo ditto "/Volumes/My Shared Files/m-Volumes/iOS_23B86/..." /Library/Developer/CoreSimulator/...
# Takes 2-3 minutes per sandbox
```

**Pain points:**
- Users must repeat runtime copying for every new sandbox
- 2-3 minutes of copying per sandbox (or 15+ minutes if downloading)
- Manual commands are error-prone (wrong paths, missing Info.plist, etc.)
- No way to pre-configure "iOS testing sandbox" as a template

## Proposed Solution: Runtime-Cached Base Images

Create specialized base images that cache runtime combinations. First sandbox with a given runtime set does the copying work; subsequent sandboxes clone from the cached base instantly.

### Core Idea

Instead of:
```
yoloai-base (no runtimes)
  └─> sandbox1 (copy iOS runtime)
  └─> sandbox2 (copy iOS runtime again)
  └─> sandbox3 (copy iOS runtime again)
```

We get:
```
yoloai-base (no runtimes)
  └─> yoloai-base-ios (iOS runtime cached)
       └─> sandbox1 (instant clone)
       └─> sandbox2 (instant clone)
       └─> sandbox3 (instant clone)
```

### User Experience

**First time with iOS:**
```bash
$ yoloai new test1 --apple-runtime ios
Creating sandbox with iOS runtime...
Runtime base 'yoloai-base-ios' not found, creating it...
Copying iOS runtime from host... (2-3 min)
Snapshotting as yoloai-base-ios...
Cloning sandbox from yoloai-base-ios...
Sandbox 'test1' created successfully.
```

**Second time with iOS:**
```bash
$ yoloai new test2 --apple-runtime ios
Creating sandbox with iOS runtime...
Using cached base 'yoloai-base-ios'...
Cloning sandbox... (30 seconds)
Sandbox 'test2' created successfully.
```

**Mixed runtimes:**
```bash
$ yoloai new test3 --apple-runtime ios --apple-runtime tvos
Creating sandbox with iOS, tvOS runtimes...
Runtime base 'yoloai-base-ios-tvos' not found, creating it...
Found partial match: yoloai-base-ios (already has iOS)
Copying tvOS runtime... (1-2 min, iOS reused)
Snapshotting as yoloai-base-ios-tvos...
Cloning sandbox...
Sandbox 'test3' created successfully.
```

## Design Details

### Flag Design

**`--apple-runtime <platform>`** (repeatable, case-insensitive)

**Accepted values:**
- `ios` → iOS Simulator runtime
- `tvos` → tvOS Simulator runtime
- `watchos` → watchOS Simulator runtime
- `visionos` → visionOS Simulator runtime

**Optional version specifier:**
- `--apple-runtime ios` → Use latest iOS runtime from host
- `--apple-runtime ios:26.1` → Use specific iOS 26.1 runtime
- `--apple-runtime ios:latest` → Explicit latest (same as no version)

**Examples:**
```bash
# Single runtime
yoloai new sandbox1 --apple-runtime ios

# Multiple runtimes (repeatable flag)
yoloai new sandbox2 --apple-runtime ios --apple-runtime tvos

# Specific versions
yoloai new sandbox3 --apple-runtime ios:26.0 --apple-runtime tvos:26.1
```

### Cache Naming Scheme

**Version Resolution Philosophy:**
- Always resolve runtimes to specific versions before caching
- Include versions in every cache name (no versionless caches)
- Query host for available runtimes, pick latest (or user-specified)
- This makes combination logic simple and enables version-based pruning

**Naming format:** `yoloai-base-<platform>-<version>[-<platform>-<version>...]`

Base images named by runtime+version combinations, sorted alphabetically:

| User Request | Resolution | Base Image Name |
|--------------|------------|----------------|
| (none) | — | `yoloai-base` |
| `--apple-runtime ios` | Query host → iOS 26.2 (latest) | `yoloai-base-ios-26.2` |
| `--apple-runtime ios:26.1` | Use specific version | `yoloai-base-ios-26.1` |
| `--apple-runtime ios --apple-runtime tvos` | iOS 26.2, tvOS 26.1 | `yoloai-base-ios-26.2-tvos-26.1` |
| `--apple-runtime tvos --apple-runtime ios` | Same (sorted alphabetically) | `yoloai-base-ios-26.2-tvos-26.1` |
| `--apple-runtime watchos --apple-runtime ios --apple-runtime tvos` | iOS 26.2, tvOS 26.1, watchOS 26.0 | `yoloai-base-ios-26.2-tvos-26.1-watchos-26.0` |

**Naming rules:**
1. Resolve all runtimes to specific versions (query host or use user-specified)
2. Sort by platform name alphabetically: `ios`, `tvos`, `visionos`, `watchos`
3. Format: `<platform>-<version>` (e.g., `ios-26.2`)
4. Join multiple runtimes with hyphens
5. Always lowercase in cache name (normalized from case-insensitive input)

**Examples:**
```bash
# Host has: iOS 26.2, iOS 26.1, tvOS 26.1
yoloai new test --apple-runtime ios
# → Resolves to iOS 26.2 (latest)
# → Cache name: yoloai-base-ios-26.2

# Specific version
yoloai new test --apple-runtime ios:26.1
# → Uses iOS 26.1
# → Cache name: yoloai-base-ios-26.1

# Multiple runtimes
yoloai new test --apple-runtime tvos --apple-runtime ios
# → Resolves to: iOS 26.2, tvOS 26.1
# → Sorted: ios-26.2, tvos-26.1
# → Cache name: yoloai-base-ios-26.2-tvos-26.1
```

### Smart Caching Workflow

#### Scenario 1: No cached base exists

```
User: yoloai new test --apple-runtime ios

1. Normalize input: "ios" → ["ios"]
2. Generate cache key: "ios"
3. Check if yoloai-base-ios exists → NO
4. Find best parent base (see Parent Selection below)
5. Clone parent base → temp VM
6. Copy iOS runtime to temp VM
7. Snapshot temp VM as yoloai-base-ios
8. Clone yoloai-base-ios → test
9. Cleanup temp VM
```

**Time:** ~3-5 minutes (one-time cost)

#### Scenario 2: Cached base exists

```
User: yoloai new test2 --apple-runtime ios

1. Normalize input: "ios" → ["ios"]
2. Generate cache key: "ios"
3. Check if yoloai-base-ios exists → YES
4. Validate Xcode version matches (see Version Tracking below)
5. Clone yoloai-base-ios → test2
```

**Time:** ~30 seconds (just clone, no copying)

#### Scenario 3: Subset match (smart reuse)

```
User: yoloai new test --apple-runtime ios --apple-runtime tvos

1. Normalize input: ["ios", "tvos"] → sorted: ["ios", "tvos"]
2. Generate cache key: "ios-tvos"
3. Check if yoloai-base-ios-tvos exists → NO
4. Find best parent: yoloai-base-ios (has 1 of 2 runtimes needed)
5. Clone yoloai-base-ios → temp VM
6. Copy only tvOS runtime (iOS already present)
7. Snapshot as yoloai-base-ios-tvos
8. Clone yoloai-base-ios-tvos → test
```

**Time:** ~2 minutes (only copying tvOS, iOS reused)

### Parent Selection Strategy

When creating a new cached base, choose the best existing parent to minimize copying:

**Algorithm:**
```
1. List all existing yoloai-base-* images
2. Parse each into runtime+version set (e.g., "yoloai-base-ios-26.2-tvos-26.1" → {ios:26.2, tvos:26.1})
3. Calculate exact match overlap with requested runtime+version combinations
4. Select base with highest overlap (matching both platform AND version)
5. Fallback: yoloai-base (no runtimes)
```

**Version matching is exact:**
- `yoloai-base-ios-26.2` matches request for `ios:26.2` ✅
- `yoloai-base-ios-26.1` does NOT match request for `ios:26.2` ❌
- Must match both platform and version for overlap credit

**Example:**

Existing bases:
- `yoloai-base` (no runtimes)
- `yoloai-base-ios-26.2` ({ios:26.2})
- `yoloai-base-ios-26.1` ({ios:26.1})
- `yoloai-base-tvos-26.1` ({tvos:26.1})
- `yoloai-base-ios-26.1-tvos-26.1` ({ios:26.1, tvos:26.1})

Request: `--apple-runtime ios --apple-runtime tvos --apple-runtime watchos`
- Resolves to: {ios:26.2, tvos:26.1, watchos:26.0}

Overlap scores (exact version matching):
- `yoloai-base` → 0 matches
- `yoloai-base-ios-26.2` → 1 match (ios:26.2) ✅
- `yoloai-base-ios-26.1` → 0 matches (wrong iOS version) ❌
- `yoloai-base-tvos-26.1` → 1 match (tvos:26.1) ✅
- `yoloai-base-ios-26.1-tvos-26.1` → 1 match (tvos:26.1 only, iOS wrong version)

**Tiebreaker:** When multiple bases have same overlap, prefer:
1. More total runtimes (partial match on multi-runtime base)
2. Alphabetically earlier platform

**Winner:** `yoloai-base-ios-26.2` (has exact iOS 26.2 match)

**Result:**
- Clone from `yoloai-base-ios-26.2`
- Copy tvOS 26.1 (not present)
- Copy watchOS 26.0 (not present)
- Snapshot as `yoloai-base-ios-26.2-tvos-26.1-watchos-26.0`

### Runtime Detection and Copying

#### Runtime Directory Mapping

Map platform name to CoreSimulator directory prefix:

```go
var runtimePrefixes = map[string]string{
    "ios":      "iOS_",      // matches iOS_23B86, iOS_22F77, etc.
    "tvos":     "tvOS_",     // matches tvOS_23J579, etc.
    "watchos":  "watchOS_",  // matches watchOS_23R353, etc.
    "visionos": "xrOS_",     // matches xrOS_23N47, etc. (Apple's internal name)
}
```

#### Finding Runtimes on Host

**Source:** `/Volumes/My Shared Files/m-Volumes/` (VirtioFS mount of host's `/Library/Developer/CoreSimulator/Volumes/`)

**Version selection:**
- No version specified → pick latest by build number (e.g., iOS_23B86 over iOS_23A343)
- Version specified → match against runtime version string in Info.plist

**Algorithm:**
```
1. List directories in /Volumes/My Shared Files/m-Volumes/
2. Filter by prefix (e.g., "iOS_" for ios platform)
3. For each match:
   - Read Info.plist from runtime bundle
   - Extract version (e.g., "26.1")
   - Extract build (e.g., "23B86")
4. If version specified, filter by version match
5. Sort by build number descending
6. Pick first (latest)
```

#### Copying Runtime

**Copy procedure (same as manual process, but automated):**

```bash
# 1. Create target directory
sudo mkdir -p /Library/Developer/CoreSimulator/Profiles/Runtimes

# 2. Copy runtime bundle
sudo ditto \
  "/Volumes/My Shared Files/m-Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime" \
  /Library/Developer/CoreSimulator/Profiles/Runtimes/

# 3. Copy Info.plist (ditto may fail on this due to permissions)
sudo cp \
  "/Volumes/My Shared Files/m-Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime/Contents/Info.plist" \
  "/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime/Contents/"

# 4. Verify runtime is visible
xcrun simctl list runtimes | grep "iOS 26.1"
```

**Error handling:**
- If host has no matching runtime → Download with `xcodebuild -downloadPlatform <platform>`
- If download disabled → Fail with helpful error message
- If copy fails → Cleanup partial copy, report error

#### Download Fallback

If host doesn't have the requested runtime:

**Option A: Auto-download (slower, always works)**
```bash
xcodebuild -downloadPlatform iOS
# Takes 10-15 minutes
```

**Option B: Fail with helpful message (faster feedback)**
```
Error: iOS runtime not found on host.

To fix:
1. On host Mac: Open Xcode > Settings > Platforms
2. Download iOS Simulator runtime
3. Restart VM and try again

Or download in VM (slower):
  yoloai new sandbox --apple-runtime ios --download-if-missing
```

**Recommendation:** Default to Option B (fail fast), add `--download-if-missing` flag for Option A.

### Xcode Version Tracking

#### Metadata Storage

Store metadata for each cached base image:

**Location:** `~/.yoloai/tart-base-metadata/<base-name>.json`

**Format:**
```json
{
  "base_name": "yoloai-base-ios-tvos",
  "runtimes": [
    {
      "platform": "ios",
      "version": "26.1",
      "build": "23B86"
    },
    {
      "platform": "tvos",
      "version": "26.1",
      "build": "23J579"
    }
  ],
  "xcode_version": "26.1.1",
  "xcode_build": "17B100",
  "xcode_path": "/Applications/Xcode.app",
  "created": "2026-03-26T10:30:00Z",
  "disk_size": "35.2GB"
}
```

#### Version Detection

**On VM creation (`yoloai new`):**
```
1. Check host Xcode version:
   - xcodebuild -version → "Xcode 26.1.1"
   - xcodebuild -version | tail -1 → "Build version 17B100"

2. Load cached base metadata (if exists)

3. Compare versions:
   - If metadata.xcode_version != current → WARN
   - If metadata.xcode_build != current → WARN

4. Prompt user:
   "Cached base 'yoloai-base-ios' was built with Xcode 26.0.1.
    Current host has Xcode 26.1.1.
    Runtime may be incompatible.

    Options:
    [R]ebuild base (recommended)
    [U]se anyway
    [A]bort"
```

#### Invalidation Strategy

**On Xcode upgrade:**

**Option 1: Automatic invalidation**
- Detect version mismatch
- Mark all runtime bases as "stale"
- Prompt to rebuild on next use

**Option 2: Manual invalidation**
- User runs `yoloai system clean-runtime-bases --xcode-mismatch`
- Removes all bases built with different Xcode version

**Option 3: Version-specific caching**
- Include Xcode version in base name: `yoloai-base-ios-xcode26.1`
- Allows multiple Xcode versions to coexist
- More disk space, but safer

**Recommendation:** Option 1 (automatic invalidation) with manual cleanup command.

### Cache Management

#### List Cached Bases

```bash
$ yoloai system list-runtime-bases

Runtime Base Images:
  yoloai-base                        (no runtimes, Xcode 26.1.1, 20GB)
  yoloai-base-ios-26.1              (iOS 26.1, Xcode 26.1.1, 25.3GB) [OLD]
  yoloai-base-ios-26.2              (iOS 26.2, Xcode 26.1.1, 25.5GB)
  yoloai-base-ios-26.2-tvos-26.1    (iOS 26.2, tvOS 26.1, Xcode 26.1.1, 35.8GB) [OLD tvOS]
  yoloai-base-ios-26.2-tvos-26.2    (iOS 26.2, tvOS 26.2, Xcode 26.1.1, 36.0GB)
  yoloai-base-tvos-26.1             (tvOS 26.1, Xcode 26.1.1, 28.1GB) [OLD]
  yoloai-base-tvos-26.2             (tvOS 26.2, Xcode 26.1.1, 28.3GB)

Latest available on host: iOS 26.2, tvOS 26.2, watchOS 26.1

Total cache size: 198GB
Old versions (can be cleaned): 89GB

Hint: Run 'yoloai system clean-runtime-bases --keep-latest' to free 89GB
```

**With filters:**
```bash
# Show only stale bases (Xcode mismatch)
yoloai system list-runtime-bases --stale

# Show only bases with specific runtime (any version)
yoloai system list-runtime-bases --runtime ios

# Show only bases with specific runtime version
yoloai system list-runtime-bases --runtime ios:26.1
```

#### Clean Stale Bases

**Xcode version mismatch:**
```bash
$ yoloai system clean-runtime-bases --xcode-mismatch

Found 2 runtime bases built with old Xcode 26.0.1 (current is 26.1.1):
  yoloai-base-ios-26.1       (25.3GB)
  yoloai-base-ios-26.1-tvos-26.1 (35.8GB)

Remove? [y/N]: y
Removing yoloai-base-ios-26.1...
Removing yoloai-base-ios-26.1-tvos-26.1...
Freed 61.1GB.
```

**Old runtime versions (recommended after OS updates):**
```bash
$ yoloai system clean-runtime-bases --keep-latest

Current host has:
  iOS 26.2, tvOS 26.2, watchOS 26.1

Found 4 bases with outdated runtime versions:
  yoloai-base-ios-26.0       (iOS 26.0 → 26.2 available, 25GB)
  yoloai-base-ios-26.1       (iOS 26.1 → 26.2 available, 25GB)
  yoloai-base-ios-26.1-tvos-26.1 (iOS 26.1, tvOS 26.1 → newer available, 36GB)
  yoloai-base-tvos-26.1      (tvOS 26.1 → 26.2 available, 28GB)

Keep these bases:
  yoloai-base-ios-26.2       (latest iOS)
  yoloai-base-ios-26.2-tvos-26.2 (latest iOS + tvOS)
  yoloai-base-watchos-26.1   (latest watchOS)

WARNING: 2 active sandboxes use bases that would be removed:
  sandbox 'test1' uses yoloai-base-ios-26.1
  sandbox 'test2' uses yoloai-base-ios-26.1-tvos-26.1

Options:
  [P]roceed anyway (sandboxes will still work with their current runtimes)
  [U]pgrade sandboxes first (recreate with latest runtimes, then clean)
  [A]bort

Choice: U

Upgrading sandboxes...
  Recreating 'test1' with iOS 26.2...
  Recreating 'test2' with iOS 26.2, tvOS 26.2...
Done.

Removing old bases...
Freed 114GB.
```

**Flags:**
- `--keep-latest` → Remove bases with outdated runtime versions (recommended)
- `--xcode-mismatch` → Remove bases built with different Xcode version
- `--all` → Remove all runtime bases (nuclear option)
- `--dry-run` → Show what would be removed without doing it
- `--runtime ios` → Remove only bases containing iOS (any version)
- `--runtime ios:26.1` → Remove only bases with specific iOS 26.1
- `--force` → Skip sandbox usage check, remove anyway

#### Rebuild Bases

```bash
$ yoloai system rebuild-runtime-bases

Rebuilding all runtime bases with current Xcode 26.1.1 and latest runtime versions...
Found 3 bases to rebuild:
  yoloai-base-ios-26.1       → yoloai-base-ios-26.2 (iOS 26.1 → 26.2)
  yoloai-base-ios-26.1-tvos-26.1 → yoloai-base-ios-26.2-tvos-26.2 (both updated)
  yoloai-base-tvos-26.1      → yoloai-base-tvos-26.2 (tvOS 26.1 → 26.2)

This will create new bases with latest versions.
Old bases will be kept unless you also run --clean-old.

Rebuild? [y/N]: y

Rebuilding as yoloai-base-ios-26.2...
  Copying iOS 26.2 runtime... done
  Snapshotting... done

Rebuilding as yoloai-base-ios-26.2-tvos-26.2...
  Starting from yoloai-base-ios-26.2 (has iOS 26.2)
  Copying tvOS 26.2 runtime... done
  Snapshotting... done

Rebuilding as yoloai-base-tvos-26.2...
  Copying tvOS 26.2 runtime... done
  Snapshotting... done

All bases rebuilt successfully.

Hint: Run 'yoloai system clean-runtime-bases --keep-latest' to remove old versions.
```

**Flags:**
- `--runtime ios` → Rebuild only bases containing iOS (any version → latest)
- `--clean-old` → Also remove old bases after rebuilding
- `--parallel` → Rebuild multiple bases concurrently

#### Upgrade Sandboxes

When runtime versions are updated, existing sandboxes can be upgraded to use newer runtimes:

```bash
$ yoloai upgrade-runtimes test1

Sandbox 'test1' currently uses:
  Base: yoloai-base-ios-26.1
  Runtimes: iOS 26.1

Latest available:
  iOS 26.2

Upgrade to iOS 26.2? [y/N]: y

Recreating sandbox with iOS 26.2...
  Checking for base yoloai-base-ios-26.2... found
  Cloning yoloai-base-ios-26.2 → test1
  Preserving sandbox state (work dirs, agent state, logs)
  Done.

Sandbox 'test1' now uses iOS 26.2.
```

**Batch upgrade:**
```bash
$ yoloai upgrade-runtimes --all

Found 3 sandboxes with outdated runtimes:
  test1: iOS 26.1 → 26.2 available
  test2: iOS 26.1, tvOS 26.1 → iOS 26.2, tvOS 26.2 available
  test3: iOS 26.0 → 26.2 available

Upgrade all? [y/N]: y
Upgrading test1... done
Upgrading test2... done
Upgrading test3... done

All sandboxes upgraded.
```

**Flags:**
- `--all` → Upgrade all sandboxes with outdated runtimes
- `--runtime ios` → Only upgrade sandboxes using iOS (to latest iOS)
- `--dry-run` → Show what would be upgraded
- `--force` → Skip confirmation prompts

**Important:** Upgrading recreates the sandbox from a newer base. Sandbox state is preserved (work dirs, logs), but the VM itself is new.

## Implementation Plan

### Phase 1: Core Functionality (MVP)

**Goal:** Basic runtime caching works end-to-end

1. **Add `--apple-runtime` flag to `yoloai new` command**
   - File: `internal/cli/commands.go`
   - Add flag parsing and validation
   - Pass to Create() call

2. **Runtime name normalization**
   - File: `runtime/tart/runtime.go` (new)
   - Function: `NormalizeRuntimeName(input string) (string, error)`
   - Case-insensitive mapping: ios/iOS/IOS → "ios"
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
    - Struct: `BaseMetadata` (runtimes, xcode_version, etc.)
    - Functions: `SaveMetadata()`, `LoadMetadata()`
    - Location: `~/.yoloai/tart-base-metadata/<base>.json`

**Acceptance criteria:**
- `yoloai new test --apple-runtime ios --apple-runtime tvos` reuses `yoloai-base-ios` if it exists
- Only tvOS is copied (iOS reused)
- Metadata correctly tracks both runtimes

### Phase 3: Xcode Version Tracking

**Goal:** Detect Xcode changes and invalidate stale bases

11. **Detect Xcode version on host**
    - File: `runtime/tart/xcode.go` (new)
    - Function: `DetectXcodeVersion() (version, build string, err error)`
    - Execute: `xcodebuild -version`
    - Parse output

12. **Store Xcode version in metadata**
    - Extend `BaseMetadata` struct
    - Save Xcode version during base creation
    - Include in metadata.json

13. **Validation on cache lookup**
    - Modify `findCachedBase()` to load metadata
    - Compare current Xcode vs metadata Xcode
    - Warn if mismatch detected

14. **`yoloai system clean-runtime-bases` command**
    - File: `internal/cli/system.go`
    - Add new command: `clean-runtime-bases`
    - List stale bases (Xcode mismatch)
    - Prompt for confirmation, delete

**Acceptance criteria:**
- After Xcode upgrade, `yoloai new --apple-runtime ios` warns about stale base
- `yoloai system clean-runtime-bases` removes only mismatched bases
- Metadata correctly tracks Xcode version

### Phase 4: Management Commands

**Goal:** User-friendly cache inspection and cleanup

15. **`yoloai system list-runtime-bases` command**
    - File: `internal/cli/system.go`
    - List all `yoloai-base-*` VMs
    - Load and display metadata
    - Show disk sizes, runtimes, Xcode versions
    - Add filters: `--stale`, `--runtime ios`

16. **`yoloai system rebuild-runtime-bases` command**
    - File: `internal/cli/system.go`
    - Find all runtime bases
    - Rebuild each from scratch with current Xcode
    - Reuse parent selection for efficiency
    - Prompt for confirmation

**Acceptance criteria:**
- `yoloai system list-runtime-bases` shows all bases with details
- `yoloai system rebuild-runtime-bases` successfully recreates bases
- Filters work correctly

### Phase 5: Polish and Optimization

17. **Runtime version specifiers**
    - Support `--apple-runtime ios:26.1`
    - Parse version from flag
    - Match against runtime Info.plist
    - Include version in cache key

18. **Download fallback**
    - Add `--download-if-missing` flag
    - If runtime not on host, download with xcodebuild
    - Show progress, estimated time
    - Cache downloaded runtime like copied ones

19. **Progress reporting**
    - Show spinner during long operations
    - Report: "Copying iOS runtime (15GB, ~2 min)..."
    - Update progress during copy
    - Final summary: "Saved 3 min on future sandboxes!"

20. **Error recovery**
    - Cleanup temp VMs on failure
    - Partial base creation → delete incomplete base
    - Retry logic for transient failures
    - Clear error messages with next steps

## Open Questions

### 1. Runtime Version Selection

**Question:** When no version specified, how to pick runtime?

**Options:**
- **A. Latest by build number** (e.g., iOS_23B86 over iOS_23A343)
- **B. Latest by version** (e.g., iOS 26.1 over iOS 26.0)
- **C. Match Xcode's default** (query Xcode for preferred runtime)
- **D. Prompt user** if multiple found

**Decision:** ✅ **RESOLVED** - Always resolve to specific version and include in cache name.
- Use Option B (latest by version number, then build number as tiebreaker)
- Parse version from Info.plist: `CFBundleShortVersionString` (e.g., "26.1")
- Sort: 26.2 > 26.1 > 26.0
- If multiple builds of same version (rare), pick latest build
- Always include resolved version in cache name: `yoloai-base-ios-26.2`

### 2. Xcode Upgrade Behavior

**Question:** When Xcode version changes, what to do with cached bases?

**Options:**
- **A. Auto-invalidate immediately** (safest, may surprise users)
- **B. Warn on next use** (user decides)
- **C. Keep both versions** (e.g., `yoloai-base-ios-xcode26.0` and `yoloai-base-ios-xcode26.1`)
- **D. Manual cleanup only** (user must run clean command)

**Recommendation:** Option B (warn on use) + Option D (provide cleanup command).

### 3. Cache Size Limits and Pruning

**Question:** Should we limit number of cached bases?

**Options:**
- **A. No limit** (user manages disk manually)
- **B. Hard limit** (e.g., max 5 bases, LRU eviction)
- **C. Disk space threshold** (e.g., stop caching if <50GB free)
- **D. Warn at threshold** (e.g., "Runtime cache using 100GB")

**Decision:** ✅ **RESOLVED** - Option A (no limit) + smart pruning.
- No automatic limits or eviction
- `yoloai system list-runtime-bases` shows cache size and hints to prune if large
- `yoloai system clean-runtime-bases --keep-latest` removes old runtime versions
- User has full control over what gets removed
- Warn if total cache exceeds 100GB (in list command output)

### 4. Download Fallback Behavior

**Question:** If host has no runtime, should we download automatically?

**Options:**
- **A. Auto-download always** (slow but convenient)
- **B. Fail with helpful message** (fast feedback, user decides)
- **C. Prompt user** (ask before 15-min download)
- **D. Flag-gated** (`--download-if-missing`)

**Recommendation:** Option B (fail with message) + Option D (opt-in flag).

### 5. Concurrent Base Creation

**Question:** What if two users try to create same base simultaneously?

**Options:**
- **A. Lock file** (second waits for first to finish)
- **B. Duplicate work** (both create, last one wins)
- **C. Detect in-progress** (second user waits and uses result)
- **D. Ignore** (rare enough not to worry)

**Recommendation:** Option D (ignore for MVP) → Option A (lock file) in Phase 5.

### 6. Multiple Xcode Versions

**Question:** Should we support multiple Xcodes on host?

**Options:**
- **A. Single Xcode only** (simplest, `/Applications/Xcode.app`)
- **B. Detect active Xcode** (via `xcode-select -p`)
- **C. Multiple Xcode support** (mount all, let user choose)
- **D. Xcode path flag** (`--xcode-path /Applications/Xcode-15.app`)

**Recommendation:** Option A (single Xcode) for MVP → Option B (xcode-select) later.

## Success Metrics

**Performance:**
- First sandbox with runtime: ~3-5 min (one-time cost)
- Subsequent sandboxes: ~30 sec (95% faster)
- Incremental runtime addition: ~2 min (vs 4 min from scratch)

**Usability:**
- Zero manual commands for runtime setup
- Clear progress indicators during long operations
- Helpful error messages with actionable next steps

**Disk efficiency:**
- Shared bases save disk: 5 sandboxes with iOS = 1 base (25GB) + 5 clones (5GB each) = 50GB total
  - vs without caching: 5 × 25GB = 125GB (60% savings)

**Reliability:**
- Stale base detection: 100% (warn on Xcode mismatch)
- Runtime copying: 95%+ success rate (fallback to download)
- Cache invalidation: User-controlled, no surprise deletions

## Future Enhancements

### Multi-architecture Support

Support both Apple Silicon and Intel Macs:
- Separate base images: `yoloai-base-ios-arm64`, `yoloai-base-ios-x86_64`
- Auto-detect host architecture
- Handle Rosetta 2 translation

### Custom Runtime Sources

Allow runtimes from non-standard locations:
```bash
yoloai new test --apple-runtime ios:~/Downloads/iOS_26.1.dmg
```

### Remote Base Image Registry

Share cached bases across team:
- Push base to registry: `yoloai system push-base yoloai-base-ios`
- Pull from registry: `yoloai new test --apple-runtime ios --pull-base`
- Save team's bandwidth and time

### Profile Integration

Extend profile system to specify default runtimes:
```yaml
# ~/.yoloai/profiles/ios-dev/config.yaml
apple_runtimes:
  - ios
  - tvos
```

Then: `yoloai new test --profile ios-dev` automatically includes runtimes.

## References

- Related design: `docs/design/ios-testing.md`
- Runtime investigation: `docs/dev/ios-testing-investigation.md`
- Tart documentation: https://github.com/cirruslabs/tart
- Apple Simulator runtime management: `man simctl`

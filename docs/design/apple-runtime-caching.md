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
- 2-3 minutes of copying per sandbox
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
$ yoloai new test1 --runtime ios
Creating sandbox with iOS runtime...
Runtime base 'yoloai-base-ios' not found, creating it...
Copying iOS runtime from host... (2-3 min)
Snapshotting as yoloai-base-ios...
Cloning sandbox from yoloai-base-ios...
Sandbox 'test1' created successfully.
```

**Second time with iOS:**
```bash
$ yoloai new test2 --runtime ios
Creating sandbox with iOS runtime...
Using cached base 'yoloai-base-ios'...
Cloning sandbox... (30 seconds)
Sandbox 'test2' created successfully.
```

**Mixed runtimes:**
```bash
$ yoloai new test3 --runtime ios --runtime tvos
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

**`--runtime <platform>[:version]`** (repeatable, case-insensitive)

**Format:** `platform[:version]` where version is a specific version (e.g., `26.1`) or `latest` (implied if omitted).

**Accepted platforms:**
- `ios` → iOS Simulator runtime
- `tvos` → tvOS Simulator runtime
- `watchos` → watchOS Simulator runtime
- `visionos` → visionOS Simulator runtime

**Version specifier:**
- `ios` → Use latest iOS runtime (`:latest` is implied)
- `ios:latest` → Use latest iOS runtime (explicit)
- `ios:26.1` → Use specific iOS 26.1 runtime

**Note:** If no version is specified, `:latest` is implied.

**Examples:**
```bash
# Single runtime (implicit latest)
yoloai new sandbox1 --runtime ios

# Explicit latest
yoloai new sandbox2 --runtime ios:latest

# Multiple runtimes (repeatable flag)
yoloai new sandbox3 --runtime ios --runtime tvos

# Specific versions
yoloai new sandbox4 --runtime ios:26.0 --runtime tvos:26.1

# Mix of latest and specific
yoloai new sandbox5 --runtime ios:latest --runtime tvos:26.1
```

### Cache Naming Scheme

**Version Resolution Philosophy:**
- Always resolve runtimes to specific versions before caching
- Include versions in every cache name (no versionless caches)
- Query host for available runtimes, pick latest (or user-specified)
- This makes combination logic simple and enables version-based pruning

**Naming format:** `yoloai-base-<platform>-<version>[-<platform>-<version>...]`

Base images named by runtime+version combinations, **sorted alphabetically by platform, then by version within platform:**

| User Request | Resolution | Base Image Name |
|--------------|------------|----------------|
| (none) | — | `yoloai-base` |
| `--runtime ios` | Query host → iOS 26.2 (latest) | `yoloai-base-ios-26.2` |
| `--runtime ios:26.1` | Use specific version | `yoloai-base-ios-26.1` |
| `--runtime ios --runtime tvos` | iOS 26.2, tvOS 26.1 | `yoloai-base-ios-26.2-tvos-26.1` |
| `--runtime tvos --runtime ios` | Same (sorted alphabetically) | `yoloai-base-ios-26.2-tvos-26.1` |
| `--runtime ios:26.1 --runtime ios:26.2` | Both iOS versions | `yoloai-base-ios-26.1-ios-26.2` |
| `--runtime ios:26.2 --runtime tvos:26.0 --runtime tvos:26.1` | iOS 26.2, tvOS 26.0, tvOS 26.1 | `yoloai-base-ios-26.2-tvos-26.0-tvos-26.1` |
| `--runtime ios:26.1 --runtime ios:26.2 --runtime tvos:26.1` | iOS 26.1, iOS 26.2, tvOS 26.1 | `yoloai-base-ios-26.1-ios-26.2-tvos-26.1` |
| `--runtime watchos --runtime ios --runtime tvos` | iOS 26.2, tvOS 26.1, watchOS 26.0 | `yoloai-base-ios-26.2-tvos-26.1-watchos-26.0` |

**Multiple versions of same platform:**
- Bases can contain multiple versions of the same platform
- Example: `yoloai-base-ios-26.1-ios-26.2-tvos-26.1`
- Sorted first by platform, then by version within platform
- This allows incremental addition without rebuilding the entire base

**Naming rules:**
1. Resolve all runtimes to specific versions (query host or use user-specified)
2. Sort by platform name alphabetically: `ios`, `tvos`, `visionos`, `watchos`
3. Within each platform, sort by version number (semver order)
4. Format: `<platform>-<version>` (e.g., `ios-26.2`)
5. Join all runtime+version pairs with hyphens
6. Always lowercase in cache name (normalized from case-insensitive input)

**Examples:**
```bash
# Host has: iOS 26.2, iOS 26.1, tvOS 26.1

# Implicit latest
yoloai new test --runtime ios
# → Resolves to iOS 26.2 (latest)
# → Cache name: yoloai-base-ios-26.2

# Explicit latest (same result)
yoloai new test --runtime ios:latest
# → Resolves to iOS 26.2 (latest)
# → Cache name: yoloai-base-ios-26.2

# Specific version
yoloai new test --runtime ios:26.1
# → Uses iOS 26.1
# → Cache name: yoloai-base-ios-26.1

# Multiple runtimes
yoloai new test --runtime tvos --runtime ios
# → Resolves to: iOS 26.2, tvOS 26.1
# → Sorted: ios-26.2, tvos-26.1
# → Cache name: yoloai-base-ios-26.2-tvos-26.1
```

### Smart Caching Workflow

#### Scenario 1: No cached base exists

```
User: yoloai new test --runtime ios

1. Normalize input: "ios" → ["ios"]
2. Resolve to specific version: "ios" → "ios:26.2" (query host)
3. Generate cache key: "ios-26.2"
4. Acquire exclusive lock on "yoloai-base-ios-26.2" (blocks if another process is creating it)
5. Check if yoloai-base-ios-26.2 exists → NO
6. Find best parent base (see Parent Selection below)
7. Clone parent base → temp VM
8. Copy iOS 26.2 runtime to temp VM
9. Snapshot temp VM as yoloai-base-ios-26.2
10. Release lock
11. Clone yoloai-base-ios-26.2 → test
12. Cleanup temp VM
```

**Time:** ~3-5 minutes (one-time cost)

**Concurrency:** If another process tries to create the same base simultaneously, it blocks at step 4 until the first process completes, then discovers the base exists at step 5 and skips to step 11.

#### Scenario 2: Cached base exists

```
User: yoloai new test2 --runtime ios

1. Normalize input: "ios" → ["ios"]
2. Resolve to specific version: "ios" → "ios:26.2"
3. Generate cache key: "ios-26.2"
4. Acquire exclusive lock on "yoloai-base-ios-26.2" (non-blocking if just checking)
5. Check if yoloai-base-ios-26.2 exists → YES
6. Release lock
7. Clone yoloai-base-ios-26.2 → test2
```

**Time:** ~30 seconds (just clone, no copying)

#### Scenario 3: Subset match (smart reuse)

```
User: yoloai new test --runtime ios --runtime tvos

1. Normalize input: ["ios", "tvos"] → sorted: ["ios", "tvos"]
2. Resolve versions: ios → ios:26.2, tvos → tvos:26.1 (query host for latest)
3. Generate cache key: "ios-26.2-tvos-26.1"
4. Acquire exclusive lock on "yoloai-base-ios-26.2-tvos-26.1"
5. Check if yoloai-base-ios-26.2-tvos-26.1 exists → NO
6. Find best parent: yoloai-base-ios-26.2 (has 1 of 2 runtimes needed)
7. Clone yoloai-base-ios-26.2 → temp VM
8. Copy only tvOS 26.1 runtime (iOS 26.2 already present)
9. Snapshot as yoloai-base-ios-26.2-tvos-26.1
10. Release lock
11. Clone yoloai-base-ios-26.2-tvos-26.1 → test
12. Cleanup temp VM
```

**Time:** ~2 minutes (only copying tvOS, iOS reused)

### Error Recovery

**Multi-step base creation workflow:**
1. Clone parent base → temp VM
2. Copy runtimes into temp VM
3. Snapshot temp VM → new base
4. Save metadata
5. Cleanup temp VM

**Temp VM naming convention:**
```
yoloai-base-<cacheKey>-tmp-<random>
```
Example: `yoloai-base-ios-26.1-tmp-a3f7b2`

**Recovery strategy:**

```go
// Pseudocode for base creation with error recovery
func createBase(ctx, cacheKey, runtimes) error {
    tempName := fmt.Sprintf("yoloai-base-%s-tmp-%s", cacheKey, randomID())
    baseName := fmt.Sprintf("yoloai-base-%s", cacheKey)

    // Best-effort cleanup on any error
    defer cleanupTempVM(ctx, tempName)

    // Step 1: Clone parent
    if err := cloneParent(ctx, parentBase, tempName); err != nil {
        return fmt.Errorf("clone parent: %w", err)
    }

    // Step 2: Copy runtimes
    for _, runtime := range runtimes {
        if err := copyRuntime(ctx, tempName, runtime); err != nil {
            // Temp VM cleaned by defer
            return fmt.Errorf("copy %s %s: %w", runtime.Platform, runtime.Version, err)
        }
    }

    // Step 3: Snapshot as new base
    if err := snapshot(ctx, tempName, baseName); err != nil {
        // Clean partial base if created
        if vmExists(baseName) {
            _ = deleteVM(baseName)
        }
        return fmt.Errorf("snapshot as %s: %w", baseName, err)
    }

    // Step 4: Save metadata (non-fatal)
    if err := saveMetadata(baseName, meta); err != nil {
        slog.Warn("failed to save metadata", "base", baseName, "error", err)
    }

    return nil
}

// Best-effort cleanup - never fails
func cleanupTempVM(ctx, vmName) {
    _ = runTart(ctx, "stop", vmName)
    _ = runTart(ctx, "delete", vmName)
}
```

**Orphaned temp VM cleanup:**
- Add to `yoloai system prune` command
- Find VMs matching pattern `yoloai-base-*-tmp-*`
- Delete automatically (they're always safe to remove)

**Edge case handling:**

1. **Non-existent runtime version:**
   - User requests `--runtime ios:99.0` but host only has up to 26.2
   - **Error immediately**: "iOS 99.0 not available on host, latest is 26.2"
   - Do not attempt fuzzy matching or suggestions
   - Query host runtimes during flag validation

2. **Partial failure cleanup:**
   - If base creation fails at any step, **undo anything created in that command** (best effort)
   - Example: Disk full during snapshot → delete temp VM (already planned via defer)
   - If temp VM was cloned from a newly-created parent base, leave parent alone (it's valid, just not used yet)
   - Only clean resources created in the current operation

3. **Corrupted metadata:**
   - Base exists but `~/.yoloai/tart-base-metadata/<base>.json` is malformed or missing
   - **Delete base and rebuild from scratch** (don't attempt repair)
   - Rationale: Metadata is critical for parent selection; corrupted state indicates larger problem
   - User can always recreate base with `yoloai system runtime add`
   - Log warning: "Corrupted metadata for yoloai-base-ios-26.2, deleting and rebuilding"

### Base Image Locking

**Problem:** Two concurrent `yoloai new` commands requesting the same runtime could both try to create the same base image simultaneously, resulting in duplicate work or corrupted state.

**Solution:** Use advisory file locking (same pattern as sandbox locking in `sandbox/lock_unix.go`).

**Mechanism:**
- Lock file location: `~/.yoloai/tart-base-locks/<base-name>.lock`
- Uses `flock(2)` on Unix/macOS for exclusive advisory locks
- **Blocks until lock is available** (serializes concurrent base creation)
- Auto-releases on process exit or crash
- 0-byte files left on disk (harmless, reused on next lock)

**Example flow:**
```
Process A: yoloai new test1 --runtime ios
Process B: yoloai new test2 --runtime ios (starts 1 second later)

Timeline:
0s  - A: Acquires lock on "yoloai-base-ios-26.2.lock"
0s  - A: Starts creating base (clone, copy runtime, snapshot)
1s  - B: Tries to acquire same lock, BLOCKS waiting
180s - A: Finishes base creation, releases lock
180s - B: Acquires lock, checks base exists, releases lock
180s - B: Clones existing base → test2 (fast)
```

**Benefits:**
- No race conditions - fully serialized
- No duplicate work - second process waits and reuses
- Automatic cleanup - flock releases on crash
- Proven pattern - same mechanism as sandbox locking

**Windows:** Uses no-op locks (same pattern as sandbox locking on Windows). Tart does not support Windows, and yoloAI does not currently support Windows either. If Windows support is added in the future, concurrent base creation may result in duplicate work, but this is acceptable given the rarity of the scenario.

### Parent Selection Strategy

When creating a new cached base, choose the best existing parent to minimize copying. **Bases can contain multiple versions of the same platform** (e.g., `ios-26.1-ios-26.2-tvos-26.1`).

**Algorithm:**
```
1. List all existing yoloai-base-* images
2. Parse each into runtime+version set (e.g., "yoloai-base-ios-26.2-tvos-26.0-tvos-26.1" → {ios:26.2, tvos:26.0, tvos:26.1})
3. Check if any base contains ALL requested runtime+version combinations (exact match)
   - If yes, use that base directly (no copying needed)
4. If no exact match, find base with most overlapping runtimes (exact version matches)
5. Prioritize: maximize overlap (minimize copying), then minimize total base size
6. Fallback: yoloai-base (no runtimes)
```

**Version matching is exact:**
- `yoloai-base-ios-26.2` matches request for `ios:26.2` ✅
- `yoloai-base-ios-26.1` does NOT match request for `ios:26.2` ❌
- Must match both platform and version for overlap credit

**Multiple versions of same platform:**
- A base can have `ios-26.1-ios-26.2` (both iOS 26.1 and 26.2)
- If you need ios:26.1, and a base has `ios-26.1-ios-26.2`, that's a match ✅
- You can add to existing bases rather than rebuilding from scratch

**Example 1: Exact match**

Existing bases:
- `yoloai-base-ios-26.2-tvos-26.1`

Request: `--runtime ios:26.2 --runtime tvos:26.1`

**Result:** Use existing base directly (exact match, no copying needed)

**Example 2: Partial match, add to existing base**

Existing bases:
- `yoloai-base-ios-26.2-tvos-26.0` ({ios:26.2, tvos:26.0})

Request: `--runtime ios:26.2 --runtime tvos:26.1`

Overlap: ios:26.2 matches (1/2)

**Result:**
- Clone from `yoloai-base-ios-26.2-tvos-26.0`
- Copy tvOS 26.1 (not present)
- Snapshot as `yoloai-base-ios-26.2-tvos-26.0-tvos-26.1` (includes both tvOS versions)
- 2 runtimes copied total, tvOS 26.0 reused from parent

**Example 3: Multiple candidates**

Existing bases:
- `yoloai-base-ios-26.2` (1 runtime, 1 match)
- `yoloai-base-tvos-26.1` (1 runtime, 1 match)
- `yoloai-base-ios-26.1-tvos-26.1` (2 runtimes, 1 match - only tvOS matches)

Request: `--runtime ios:26.2 --runtime tvos:26.1`

**Tiebreaker:**
1. Prefer base with most matches (all have 1 match - tie)
2. Prefer base with fewer total runtimes (minimize base size)
3. `yoloai-base-ios-26.2` and `yoloai-base-tvos-26.1` both have 1 runtime
4. Pick alphabetically earlier platform: `yoloai-base-ios-26.2`

**Result:**
- Clone from `yoloai-base-ios-26.2`
- Copy tvOS 26.1
- Snapshot as `yoloai-base-ios-26.2-tvos-26.1`

### Runtime Detection and Copying

**IMPORTANT:** Runtime discovery must happen on the **host**, not inside the VM. CoreSimulator cannot discover runtimes from VirtioFS mounts (see `docs/dev/ios-testing-investigation.md` for details). The host must query available runtimes and coordinate copying them into the VM.

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

**Runtime discovery happens on the host via `xcrun simctl`:**

```bash
# Query host for available runtimes
xcrun simctl list runtimes --json
```

**JSON Schema (from actual simctl output):**
```json
{
  "runtimes": [
    {
      "platform": "iOS",              // "iOS", "tvOS", "watchOS", "visionOS"
      "version": "26.1",               // Version (X.Y or X.Y.Z format)
      "buildversion": "23B86",         // Build ID
      "bundlePath": "/Library/Developer/CoreSimulator/Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime",
      "identifier": "com.apple.CoreSimulator.SimRuntime.iOS-26-1",
      "name": "iOS 26.1",              // Human-readable name
      "isAvailable": true,             // Whether runtime is usable
      "isInternal": false,
      "supportedArchitectures": ["x86_64", "arm64"],
      "supportedDeviceTypes": [...]    // Array of supported device types
    }
  ]
}
```

**Key observations:**
- `platform` field directly specifies iOS/tvOS/watchOS/visionOS (no need to parse identifier)
- `bundlePath` provides full path to .simruntime bundle
- Multiple builds per version are possible (e.g., iOS 26.0 build 23A339 AND 23A343)
- Version format is X.Y or X.Y.Z (18.3.1, 26.0, 26.1)

**Version selection:**
- No version specified → pick latest by semantic version (use `github.com/hashicorp/go-version`)
- Version specified → match against runtime version from simctl output
- If multiple builds of same version, pick latest build (lexicographic sort of buildversion)

**Algorithm (executed on host):**
```
1. Run: xcrun simctl list runtimes --json
2. Parse JSON output into Runtime structs:
   type Runtime struct {
       Platform    string
       Version     string
       BuildVersion string
       BundlePath  string
   }
3. Filter by platform (e.g., "iOS" for ios platform)
4. If version specified (e.g., "26.1"), filter by exact version match
5. Parse versions using go-version library
6. Sort by version descending, then buildversion descending (tiebreaker)
7. Pick first (latest)
8. Return bundlePath for copying
```

**Note:** This query must happen on the host before creating the VM, so we know which runtime bundles to copy into the base image.

#### Copying Runtime into VM

**Prerequisites:**
1. Host has queried available runtimes via `xcrun simctl list runtimes --json`
2. Selected runtime bundle path is known (e.g., `/Library/Developer/CoreSimulator/Volumes/iOS_23B86/...`)
3. VM is running with VirtioFS mount of the runtime directory (read-only)

**Copy procedure (executed inside the VM via `tart exec`):**

```bash
# 1. Create target directory in VM
sudo mkdir -p /Library/Developer/CoreSimulator/Profiles/Runtimes

# 2. Copy runtime bundle from VirtioFS mount to local VM storage
# (CoreSimulator cannot use VirtioFS mounts directly, so we copy locally)
sudo ditto \
  "/Volumes/My Shared Files/m-Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime" \
  /Library/Developer/CoreSimulator/Profiles/Runtimes/

# 3. Copy Info.plist (ditto may fail on this due to permissions)
sudo cp \
  "/Volumes/My Shared Files/m-Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime/Contents/Info.plist" \
  "/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime/Contents/"

# 4. Verify runtime is visible to CoreSimulator in VM
xcrun simctl list runtimes | grep "iOS 26.1"
```

**Key insight:** Runtimes must be copied to local VM storage (`/Library/Developer/CoreSimulator/...`), not used from VirtioFS mounts. This is why base image caching provides value — copy once to base, clone the base for each sandbox.

**Error handling:**
- If host has no matching runtime → Fail with helpful error message
- If copy fails → Cleanup partial copy, report error

**Missing runtime error message:**
```
Error: iOS 26.2 runtime not found on host.

To fix:
1. On host Mac: Open Xcode > Settings > Platforms
2. Download iOS Simulator runtime
3. Try again: yoloai new sandbox --runtime ios

Runtime must be on host so all VMs can share it.
Downloading in each VM would waste time and disk space.
```

**Note:** yoloai never downloads runtimes automatically. User must download on host to enable sharing across all VMs.

### Base Image Metadata (No Xcode Tracking Needed)

#### Metadata Storage

Store minimal metadata for each cached base image:

**Location:** `~/.yoloai/tart-base-metadata/<base-name>.json`

**Format:**
```json
{
  "version": 0,
  "base_name": "yoloai-base-ios-26.2-tvos-26.2",
  "runtimes": [
    {
      "platform": "ios",
      "version": "26.2",
      "build": "23B86"
    },
    {
      "platform": "tvos",
      "version": "26.2",
      "build": "23J579"
    }
  ],
  "created_at": "2026-03-26T10:30:00Z",
  "yoloai_version": "0.5.0"
}
```

**Schema notes:**
- `version`: Schema version (currently 0; TODO: assign proper version when yoloAI 1.0 is released)
- `base_name`: Redundant with filename but useful for validation
- `runtimes`: Array of runtimes in this base (platform, version, build)
- `created_at`: ISO 8601 timestamp (UTC)
- `yoloai_version`: Version of yoloAI that created this base
- No `disk_size` field - compute on-demand with `tart get` to avoid staleness

**No Xcode version tracking** - runtime versions in cache names handle it automatically.

#### How Xcode Upgrades Work

**Automatic handling via runtime versions:**

1. User upgrades Xcode (e.g., 26.0 → 26.1)
2. New runtime becomes available (e.g., iOS 26.2)
3. User runs: `yoloai new test --runtime ios`
4. Resolves to iOS 26.2 (latest)
5. Cache miss: `yoloai-base-ios-26.2` doesn't exist
6. Creates new base with iOS 26.2 and new Xcode
7. Old `yoloai-base-ios-26.1` remains until cleaned

**Cleanup:**
```bash
# Remove old runtime versions
yoloai system runtime remove --older-than latest
```

**Why no Xcode version tracking needed:**
- Runtime versions correlate naturally with Xcode versions
- New Xcode → new runtimes → new bases created automatically
- Old bases either work fine or produce real errors (better than warnings)
- User controls cleanup with `--keep-latest`
- Simpler implementation, less complexity

### Cache Management

**Note:** `yoloai system runtime` commands are only available on macOS hosts (where Tart VMs can use Apple runtimes).

#### List Cached Bases

```bash
$ yoloai system runtime list

Runtime Base Images:
  yoloai-base                        (no runtimes, 20GB)
  yoloai-base-ios-26.1              (iOS 26.1, 25.3GB)
  yoloai-base-ios-26.2              (iOS 26.2, 25.5GB)
  yoloai-base-ios-26.2-tvos-26.1    (iOS 26.2, tvOS 26.1, 35.8GB)
  yoloai-base-ios-26.2-tvos-26.2    (iOS 26.2, tvOS 26.2, 36.0GB)
  yoloai-base-tvos-26.1             (tvOS 26.1, 28.1GB)
  yoloai-base-tvos-26.2             (tvOS 26.2, 28.3GB)

Latest available on host: iOS 26.2, tvOS 26.2, watchOS 26.1

Total cache size: 198GB

Hint: Run 'yoloai system runtime remove --older-than latest' to clean old versions
```

**With filters:**
```bash
# Show only bases with iOS (any version)
yoloai system runtime list ios

# Show only bases with iOS 26.2 specifically
yoloai system runtime list ios:26.2

# Show bases with any runtime at version 26.2
yoloai system runtime list 26.2

# Show bases that have both iOS and tvOS 26.0
yoloai system runtime list ios tvos:26.0

# Show bases with iOS:latest and tvOS (any version)
yoloai system runtime list ios:latest tvos
```

#### Add Base (Pre-warm Cache)

```bash
# Implicit latest
$ yoloai system runtime add ios tvos

Creating runtime base with iOS 26.2, tvOS 26.2...
Found partial match: yoloai-base-ios-26.2 (already has iOS)
Copying tvOS 26.2 runtime... done
Snapshotting as yoloai-base-ios-26.2-tvos-26.2... done

Base created: yoloai-base-ios-26.2-tvos-26.2 (36GB)
```

**Explicit latest:**
```bash
$ yoloai system runtime add ios:latest tvos:latest

Creating runtime base with iOS 26.2, tvOS 26.2...
# (same as above)
```

**Specific versions:**
```bash
$ yoloai system runtime add ios:26.1 tvos:26.2

Creating runtime base with iOS 26.1, tvOS 26.2...
Copying iOS 26.1 runtime... done
Copying tvOS 26.2 runtime... done
Snapshotting as yoloai-base-ios-26.1-tvos-26.2... done

Base created: yoloai-base-ios-26.1-tvos-26.2 (36GB)
```

**Use case:** Pre-create runtime bases before needing them (e.g., CI setup, team onboarding).

#### Remove Bases

**Remove anything older than latest (most common):**
```bash
$ yoloai system runtime remove --older-than latest

Querying host for latest runtimes: iOS 26.2, tvOS 26.2, watchOS 26.1

Found 4 bases with outdated runtime versions:
  yoloai-base-ios-26.0       (iOS 26.0 < 26.2, 25GB)
  yoloai-base-ios-26.1       (iOS 26.1 < 26.2, 25GB)
  yoloai-base-ios-26.1-tvos-26.1 (iOS 26.1 < 26.2, tvOS 26.1 < 26.2, 36GB)
  yoloai-base-tvos-26.1      (tvOS 26.1 < 26.2, 28GB)

WARNING: 2 active sandboxes use bases that would be removed:
  sandbox 'test1' uses yoloai-base-ios-26.1
  sandbox 'test2' uses yoloai-base-ios-26.1-tvos-26.1

Proceed? [y/N]: n
Aborted.
```

**Remove anything older than specific version:**
```bash
$ yoloai system runtime remove --older-than 26.2

Found 3 bases with any runtime older than 26.2:
  yoloai-base-ios-26.0       (iOS 26.0 < 26.2, 25GB)
  yoloai-base-ios-26.1       (iOS 26.1 < 26.2, 25GB)
  yoloai-base-ios-26.1-tvos-26.1 (iOS 26.1 < 26.2, tvOS 26.1 < 26.2, 36GB)

Remove? [y/N]:
```

**Per-platform version constraints:**
```bash
$ yoloai system runtime remove --older-than ios:26.2 --older-than tvos:latest

Querying host for latest tvOS: 26.2

Found 2 bases matching criteria:
  yoloai-base-ios-26.0       (iOS 26.0 < 26.2, 25GB)
  yoloai-base-ios-26.1       (iOS 26.1 < 26.2, 25GB)
  yoloai-base-ios-26.1-tvos-26.1 (iOS 26.1 < 26.2, tvOS 26.1 < 26.2, 36GB)

Remove? [y/N]:
```

**Remove specific runtime:**
```bash
$ yoloai system runtime remove ios:26.1

Found 2 bases with iOS 26.1:
  yoloai-base-ios-26.1       (25GB)
  yoloai-base-ios-26.1-tvos-26.1 (36GB)

Remove? [y/N]: y
Freed 61GB.
```

**Remove all (nuclear option):**
```bash
$ yoloai system runtime remove --all

This will remove ALL runtime bases (7 bases, 198GB).
Base yoloai-base (no runtimes) will be kept.

Remove? [y/N]:
```

**Flags:**
- `--older-than <version>` → Remove bases with any runtime older than version
  - `--older-than latest` → Compare against latest available on host (most common)
  - `--older-than 26.2` → Compare against specific version
- `--older-than <platform:version>` → Per-platform version constraint (repeatable)
  - `--older-than ios:latest` → iOS must be latest
  - `--older-than ios:26.2 --older-than tvos:latest` → Mixed constraints
- `--all` → Remove all runtime bases (keeps yoloai-base)
- `--dry-run` → Show what would be removed without doing it
- `--yes` → Skip confirmation prompts

**Positional argument:**
- `remove [runtime[:version]]` → Remove only bases containing runtime (e.g., `remove ios` or `remove ios:26.1`)

**Note:** `--older-than` flags are mutually exclusive with positional runtime filter.

**Typical workflow after Xcode upgrade:**
```bash
# 1. Check what's cached
yoloai system runtime list

# 2. Check iOS-specific bases
yoloai system runtime list ios

# 3. Remove old versions (optional - they don't hurt)
yoloai system runtime remove --older-than latest

# 4. Or remove a specific old version
yoloai system runtime remove ios:26.1

# 5. Create sandboxes - new bases created automatically as needed
yoloai new test --runtime ios
# → Creates yoloai-base-ios-26.2 on first run
```

## Implementation

See implementation plan: `docs/dev/plans/apple-runtime-caching.md`

### Integration Points

**Base image creation orchestration** should live in the **sandbox package** (`sandbox/create.go`):
1. CLI parses `--runtime` flags and passes to sandbox creation
2. Sandbox package resolves base image name (generate cache key)
3. Sandbox package checks if base exists (call Tart backend to list VMs)
4. If base doesn't exist, sandbox package creates it:
   - Calls Tart backend helper to create temp VM
   - Discovers runtimes on host (via `xcrun simctl`)
   - Calls Tart backend helper to copy runtimes into temp VM
   - Calls Tart backend helper to snapshot temp VM as new base
5. Sandbox package calls `runtime.Create()` with resolved base image name

**Tart-specific logic** lives in the **Tart runtime package** (`runtime/tart/`):
- Runtime discovery on host (`FindRuntimes()`)
- Runtime copying into VM (`CopyRuntimeToVM()`)
- Base image snapshotting (`SnapshotAsBase()`)
- Parent base selection (`FindBestParentBase()`)

**Rationale:** The sandbox package orchestrates the high-level flow (what bases to create), while the Tart package provides the low-level mechanics (how to create them). This separation allows for:
- Platform-specific runtime logic stays in the runtime backend
- Sandbox package remains backend-agnostic (could support other runtimes in future)
- Tart package owns all Tart CLI interactions

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

**Decision:** ✅ **RESOLVED** - Version-based caching handles this automatically (no Xcode tracking needed).
- Runtime versions in cache names naturally handle Xcode upgrades
- New Xcode → new runtimes → new cache names → new bases created
- Old bases remain until manually cleaned with `--keep-latest`
- User controls when to upgrade sandboxes and clean old bases
- Simpler than tracking Xcode versions separately

### 3. Cache Size Limits and Pruning

**Question:** Should we limit number of cached bases?

**Options:**
- **A. No limit** (user manages disk manually)
- **B. Hard limit** (e.g., max 5 bases, LRU eviction)
- **C. Disk space threshold** (e.g., stop caching if <50GB free)
- **D. Warn at threshold** (e.g., "Runtime cache using 100GB")

**Decision:** ✅ **RESOLVED** - Option A (no limit) + smart pruning.
- No automatic limits or eviction
- `yoloai system runtime list` shows cache size and hints to prune if large
- `yoloai system runtime remove --older-than latest` removes old runtime versions
- User has full control over what gets removed
- Warn if total cache exceeds 100GB (in list command output)

### 4. Download Fallback Behavior

**Question:** If host has no runtime, should we download automatically?

**Options:**
- **A. Auto-download always** (slow but convenient)
- **B. Fail with helpful message** (fast feedback, user decides)
- **C. Prompt user** (ask before 15-min download)
- **D. Flag-gated** (`--download-if-missing`)

**Decision:** ✅ **RESOLVED** - Option B (fail with helpful message).
- No automatic downloading
- No `--download-if-missing` flag
- Clear error message directing user to download on host:
  ```
  Error: iOS 26.2 runtime not found on host.

  To fix:
    1. On host Mac: Open Xcode > Settings > Platforms
    2. Download iOS Simulator runtime
    3. Try again: yoloai new sandbox --runtime ios

  Runtime must be on host so all VMs can share it via cached base images.
  ```
- User downloads once on host → all VMs benefit via caching
- No re-downloading for each sandbox (fast 2-3min copy instead of 15min download)

### 5. Concurrent Base Creation

**Question:** What if two users try to create same base simultaneously?

**Options:**
- **A. Lock file** (second waits for first to finish)
- **B. Duplicate work** (both create, last one wins)
- **C. Detect in-progress** (second user waits and uses result)
- **D. Ignore** (rare enough not to worry)

**Decision:** ✅ **RESOLVED** - Option A (lock file).
- Use advisory file locking (same pattern as `sandbox/lock_unix.go`)
- Lock file: `~/.yoloai/tart-base-locks/<base-name>.lock`
- Second process blocks until first completes, then discovers base exists
- No duplicate work, no race conditions
- Automatic cleanup on crash (flock auto-releases)
- Proven pattern already used for sandbox locking

### 6. Multiple Xcode Versions

**Question:** Should we support multiple Xcodes on host?

**Options:**
- **A. Single Xcode only** (simplest, `/Applications/Xcode.app`)
- **B. Detect active Xcode** (via `xcode-select -p`)
- **C. Multiple Xcode support** (mount all, let user choose)
- **D. Xcode path flag** (`--xcode-path /Applications/Xcode-15.app`)

**Decision:** ✅ **RESOLVED** - Option B (detect via xcode-select).
- Respect user's xcode-select setting on host
- Works with Xcodes at any location (standard or custom)

**Implementation:**
```go
// In buildRunArgs(), detect active Xcode:
xcodeDevPath := getCommandOutput("xcode-select", "-p")
// e.g., "/Applications/Xcode-Beta.app/Contents/Developer"

xcodePath := filepath.Dir(filepath.Dir(xcodeDevPath))
// e.g., "/Applications/Xcode-Beta.app"

// Generate VirtioFS mount name from Xcode path
mountName := "m-" + filepath.Base(xcodePath)
// e.g., "m-Xcode-Beta.app"

// Mount to VM at: /Volumes/My Shared Files/m-Xcode-Beta.app
```

**In VM (setup script):**
```python
# Auto-detect any mounted Xcode (supports any name)
xcode_mounts = glob.glob("/Volumes/My Shared Files/m-Xcode*.app")
if xcode_mounts:
    xcode_mount = xcode_mounts[0]  # Use first (should only be one)
    xcode_developer = os.path.join(xcode_mount, "Contents/Developer")
    subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer])
```

**Why this works:**
- VirtioFS can mount any path (standard `/Applications/Xcode.app` or custom `~/Developer/Xcode-Beta.app`)
- Xcode.app is self-contained - works from any location
- Path doesn't need to match between host and VM
- Setup script auto-detects mounted Xcode (no hardcoded paths)

**User workflow:**
```bash
# On host: switch to beta Xcode
sudo xcode-select -s /Applications/Xcode-Beta.app

# Create sandbox (auto-mounts the selected Xcode)
yoloai new test --runtime ios
# → Mounts Xcode-Beta.app
# → VM uses beta Xcode and its runtimes
```

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
- Runtime copying: 95%+ success rate
- Cache invalidation: User-controlled, no surprise deletions
- Version-based caching handles Xcode upgrades automatically
- Concurrent base creation: Fully serialized via file locking (no race conditions)

## Future Enhancements

### Multi-architecture Support

Support both Apple Silicon and Intel Macs:
- Separate base images: `yoloai-base-ios-arm64`, `yoloai-base-ios-x86_64`
- Auto-detect host architecture
- Handle Rosetta 2 translation

### Custom Runtime Sources

Allow runtimes from non-standard locations:
```bash
yoloai new test --runtime ios:~/Downloads/iOS_26.1.dmg
```

### Remote Base Image Registry

Share cached bases across team:
- Push base to registry: `yoloai system push-base yoloai-base-ios`
- Pull from registry: `yoloai new test --runtime ios --pull-base`
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

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

**`--apple-runtime <platform>[:version]`** (repeatable, case-insensitive)

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
yoloai new sandbox1 --apple-runtime ios

# Explicit latest
yoloai new sandbox2 --apple-runtime ios:latest

# Multiple runtimes (repeatable flag)
yoloai new sandbox3 --apple-runtime ios --apple-runtime tvos

# Specific versions
yoloai new sandbox4 --apple-runtime ios:26.0 --apple-runtime tvos:26.1

# Mix of latest and specific
yoloai new sandbox5 --apple-runtime ios:latest --apple-runtime tvos:26.1
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

# Implicit latest
yoloai new test --apple-runtime ios
# → Resolves to iOS 26.2 (latest)
# → Cache name: yoloai-base-ios-26.2

# Explicit latest (same result)
yoloai new test --apple-runtime ios:latest
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
4. Clone yoloai-base-ios → test2
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
- If host has no matching runtime → Fail with helpful error message
- If copy fails → Cleanup partial copy, report error

**Missing runtime error message:**
```
Error: iOS 26.2 runtime not found on host.

To fix:
1. On host Mac: Open Xcode > Settings > Platforms
2. Download iOS Simulator runtime
3. Try again: yoloai new sandbox --apple-runtime ios

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
  "created": "2026-03-26T10:30:00Z",
  "disk_size": "35.2GB"
}
```

**No Xcode version tracking** - runtime versions in cache names handle it automatically.

#### How Xcode Upgrades Work

**Automatic handling via runtime versions:**

1. User upgrades Xcode (e.g., 26.0 → 26.1)
2. New runtime becomes available (e.g., iOS 26.2)
3. User runs: `yoloai new test --apple-runtime ios`
4. Resolves to iOS 26.2 (latest)
5. Cache miss: `yoloai-base-ios-26.2` doesn't exist
6. Creates new base with iOS 26.2 and new Xcode
7. Old `yoloai-base-ios-26.1` remains until cleaned

**Cleanup:**
```bash
# Remove old runtime versions
yoloai system clean-runtime-bases --keep-latest
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
yoloai new test --apple-runtime ios
# → Creates yoloai-base-ios-26.2 on first run
```

## Implementation

See implementation plan: `docs/dev/plans/apple-runtime-caching.md`

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

**Decision:** ✅ **RESOLVED** - Option B (fail with helpful message).
- No automatic downloading
- No `--download-if-missing` flag
- Clear error message directing user to download on host:
  ```
  Error: iOS 26.2 runtime not found on host.

  To fix:
    1. On host Mac: Open Xcode > Settings > Platforms
    2. Download iOS Simulator runtime
    3. Try again: yoloai new sandbox --apple-runtime ios

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

**Decision:** ✅ **RESOLVED** - Option D (document but don't fix).
- Not worth implementing for MVP
- Rare scenario (requires two users creating exact same runtime combo simultaneously)
- Worst case: duplicate work, last one wins (harmless)
- Document as known limitation in implementation notes
- Can add lock file later if users report issues

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
yoloai new test --apple-runtime ios
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

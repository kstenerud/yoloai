# iOS Testing Support Design

**Status:** Design proposal
**Last updated:** 2026-03-26
**Related:** `docs/dev/ios-testing-investigation.md` (investigation and validation)

## Overview

Tart VMs automatically mount Xcode and simulator runtimes from the host, enabling iOS/tvOS/watchOS/visionOS testing with minimal VM disk usage.

### Goals

1. Automatic iOS simulator testing support in Tart VMs
2. Minimize VM disk usage through VirtioFS mounting (~25-30GB vs ~100GB)
3. Zero configuration - just works if host has Xcode
4. Dynamic updates - new host runtimes available on next VM start

### Non-goals

- macOS Seatbelt backend support (fundamentally impossible due to nested sandboxing)
- Docker backend support (no macOS container support)
- Physical iOS device testing (out of scope)
- Xcode UI testing (focus on command-line xcodebuild)
- Downloading tools in VM (user's responsibility to install on host)

## Architecture

### Automatic mounting approach

**For all Tart VMs (no flag needed):**
1. Try to mount standard Apple development paths from host
2. Only mount if directories exist (silently skip if not)
3. Setup script auto-configures if mounts are present
4. No user intervention required

```
┌─────────────────────────────────────────────────────────────┐
│ Tart VM Auto-configuration                                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  On VM creation:                                            │
│    Check host for:                                          │
│      /Applications/Xcode.app         → add mount if exists │
│      /Library/Developer/CoreSimulator/Volumes/ → add mount │
│      /Library/Developer/PrivateFrameworks → add mount      │
│                                                              │
│  On VM start:                                               │
│    Mount configured directories (whatever exists now)       │
│    Setup script detects mounts and configures paths         │
│                                                              │
│  Result:                                                    │
│    - Host has Xcode → iOS testing works automatically      │
│    - Host adds runtime → available on next VM start        │
│    - Host has no Xcode → VM works normally (no iOS)        │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### VM Configuration

**When host has Xcode + simulator runtimes:**

**VM Configuration:**
```
Mounts from host (read-only):
  /Applications/Xcode.app
  /Library/Developer/PrivateFrameworks
  /Library/Developer/CoreSimulator/Volumes/  ← iOS runtimes

Local in VM (writable):
  ~/Library/Developer/CoreSimulator/Devices/  (~600MB - simulator devices)
  /Library/Developer/CoreSimulator/Images/    (~4KB - metadata)
  /Library/Developer/CoreSimulator/Profiles/  (~5MB)
  /opt/homebrew/                              (~2GB - if needed)
  /private/var/                               (~1GB - logs, caches)
```

**Disk usage (what's actually stored IN the VM):**
- Mounted from host: ~27GB (Xcode 11GB + runtime 16GB) - takes 0GB in VM
- Local VM storage: ~11GB (simulator devices, build artifacts, caches, logs)
- **Total VM disk used: ~11GB** (with host tools) vs ~100GB (fully local)

**VM disk image size:**
- Same for all VMs: ~50GB (standard Tart base)
- Available space: ~39GB for builds/work
- No different images needed - mounting is dynamic

**Key discovery:** The 3.8GB dyld cache is NOT needed when runtime is mounted (validated via symlink testing).

**If host has no Xcode:**
- Mounts not created (directories don't exist)
- VM disk used: ~20GB (just macOS + yoloAI files, no iOS tools)
- VM works normally for non-iOS development

**Dynamic behavior:**
- User installs Xcode on host → next VM start mounts it automatically
- User installs new simulator runtime → available on next VM start
- No VM regeneration needed - mounts are runtime configuration

## Implementation

### Phase 1: Tart Runtime Updates

#### 1. Tart Runtime (`internal/runtime/tart/tart.go`)

Update `addSystemMounts()` to always try mounting Apple development tools:

```go
func (r *Runtime) addSystemMounts(cfg *runtime.InstanceConfig) {
    homeDir := config.HomeDir()

    // Mount Xcode.app if it exists on host
    xcodeAppHost := "/Applications/Xcode.app"
    if info, err := os.Stat(xcodeAppHost); err == nil && info.IsDir() {
        cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
            Source:   xcodeAppHost,
            Target:   "/Volumes/My Shared Files/m-Xcode.app",
            ReadOnly: true,
        })
    }

    // Mount all simulator runtimes if directory exists
    runtimePath := "/Library/Developer/CoreSimulator/Volumes"
    if info, err := os.Stat(runtimePath); err == nil && info.IsDir() {
        cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
            Source:   runtimePath,
            Target:   "/Volumes/My Shared Files/m-coresim-runtime",
            ReadOnly: true,
        })
    }

    // Mount PrivateFrameworks if exists
    privateFrameworks := "/Library/Developer/PrivateFrameworks"
    if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
        cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
            Source:   privateFrameworks,
            Target:   "/Volumes/My Shared Files/m-PrivateFrameworks",
            ReadOnly: true,
        })
    }
}
```

**Note:** This runs for all Tart VMs. Mounts only added if directories exist on host.

#### 2. Setup Script Updates (`internal/runtime/monitor/sandbox-setup.py`)

Auto-configure iOS testing if mounts are present:

```python
class TartBackend:
    def setup(self):
        """Existing setup, plus auto-configure iOS if mounted"""

        # Existing setup...

        # Auto-configure iOS testing if Xcode is mounted
        xcode_developer = "/Volumes/My Shared Files/m-Xcode.app/Contents/Developer"
        if os.path.isdir(xcode_developer):
            # Point xcode-select to mounted Xcode
            subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer],
                         capture_output=True)

            # Add to shell profile for persistence
            with open(os.path.expanduser("~/.zprofile"), "a") as f:
                f.write(f'export DEVELOPER_DIR="{xcode_developer}"\n')
                f.write(f'export PATH="{xcode_developer}/usr/bin:$PATH"\n')

        # Symlink mounted runtimes to system location if present
        runtime_mount = "/Volumes/My Shared Files/m-coresim-runtime"
        runtime_target = "/Library/Developer/CoreSimulator/Volumes"

        if os.path.isdir(runtime_mount):
            subprocess.run(["sudo", "rm", "-rf", runtime_target], capture_output=True)
            subprocess.run(["sudo", "ln", "-sfn", runtime_mount, runtime_target],
                         capture_output=True)
```

**Note:** Silent operation - no output unless errors. Mounts either exist or they don't.

#### 3. Agent Context (optional)

Add brief note to sandbox's CLAUDE.md if iOS testing is available:

```python
class TartBackend:
    def setup(self):
        # ... existing setup ...

        # Add iOS testing note to CLAUDE.md if Xcode mounted
        if os.path.isdir("/Volumes/My Shared Files/m-Xcode.app"):
            claude_md = os.path.expanduser("~/.claude/CLAUDE.md")
            os.makedirs(os.path.dirname(claude_md), exist_ok=True)

            with open(claude_md, "a") as f:
                f.write("\n# iOS Simulator Testing\n\n")
                f.write("iOS/tvOS/watchOS/visionOS simulator testing available if host has Xcode installed.\n")
                f.write("Use `xcrun simctl list runtimes` to see available platforms.\n")
```

**Note:** Optional enhancement - agents can discover iOS testing on their own, but this helps them know it's available.

### Phase 2: Documentation

Add note to user-facing documentation:

**docs/GUIDE.md** - Add iOS testing section:

```markdown
## iOS Simulator Testing (Tart only)

Tart VMs automatically mount Xcode and simulator runtimes from your Mac:

### Prerequisites (on host Mac)
- Xcode installed at `/Applications/Xcode.app`
- iOS/tvOS/watchOS/visionOS runtimes (download in Xcode > Settings > Components)

### How it works
1. Create any Tart VM: `yoloai new mysandbox`
2. If host has Xcode → VM automatically mounts it
3. If host has simulator runtimes → VM mounts them all
4. No configuration needed - just works

### Disk usage
- With host Xcode + runtimes: ~25-30GB VM
- Without host tools: ~20GB VM (no iOS testing)

### Adding runtimes
- Install on host: Xcode > Settings > Components
- Restart VM: runtimes available immediately (no VM regeneration needed)

### Example
\`\`\`bash
# On host: Install Xcode and iOS runtime
# Then:
yoloai new embsdk
yoloai exec embsdk -- xcodebuild test -scheme MyApp \\
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro'
\`\`\`
```

### Phase 3: Error Handling

No user-facing errors for iOS testing. The system either works silently or doesn't:

**Host has Xcode + runtimes:**
- Mounts created, setup script configures, iOS testing works
- No messages needed - just works

**Host missing Xcode or runtimes:**
- Mounts not created (directories don't exist)
- Setup script skips iOS configuration
- VM works normally for other development
- User discovers when they try xcodebuild (command not found or no runtimes)

**User action if iOS testing doesn't work:**
1. Install Xcode on host
2. Download simulator runtimes in Xcode > Settings > Components
3. Restart VM (no regeneration needed)
4. iOS testing now works

**Implementation notes:**
- No warnings or prompts during VM creation
- No detection or validation logic needed
- Clean separation: yoloAI provides plumbing, user provides tools

## Testing Strategy

### Unit Tests

- `ios.DetectHostCapabilities()` with mocked filesystem
- Approach selection logic
- Mount configuration generation

### Integration Tests

- Create VM with each approach (A, B, C)
- Verify Xcode tools accessible
- Verify iOS runtime discoverable
- Run sample xcodebuild test command

### Manual Testing Checklist

- [ ] Host with Xcode + runtimes → iOS testing works automatically
- [ ] Host with Xcode only → xcodebuild works, simctl shows no runtimes
- [ ] Host with no Xcode → VM works normally (no iOS testing)
- [ ] Install Xcode on host, restart VM → iOS testing now works
- [ ] Install new runtime on host, restart VM → new runtime available
- [ ] Multiple VMs share host's Xcode simultaneously
- [ ] Setup script silent operation (no output unless errors)
- [ ] xcode-select points to mounted Xcode
- [ ] Symlink to mounted runtimes created correctly

## Documentation Updates

### User-facing docs

1. **`docs/GUIDE.md`** - Add iOS testing section:
   - Prerequisites (Xcode on host)
   - Quick start: `yoloai new sandbox --ios-testing`
   - Approach explanations
   - Troubleshooting

2. **`docs/ROADMAP.md`** - Mark iOS testing as implemented

3. **`README.md`** - Add iOS testing to features list

### Developer docs

1. **`docs/dev/ARCHITECTURE.md`** - Document iOS testing components
2. **`docs/design/README.md`** - Link to this design doc

## Future Enhancements

### None currently planned

The automatic mounting approach covers all expected use cases:
- ✅ All simulator platforms (iOS/tvOS/watchOS/visionOS) work automatically
- ✅ Multiple runtime versions already supported (mount entire Volumes directory)
- ✅ Shared mounts across VMs (VirtioFS handles this)
- ✅ Dynamic updates (new host runtimes available on VM restart)

If additional features are needed in the future, they should maintain the current philosophy:
- No configuration required
- Silent operation
- User provides tools on host, yoloAI provides plumbing

## Resolved Design Questions

**1. Xcode updates while VM running:**
- ✅ **Decision:** Mounts established at VM start time
- Updating Xcode on host while VM is running won't affect VM until restart
- Document: "After updating Xcode on host, restart VM to use new version"
- No detection or version checking needed

**2. Multiple Xcode installations:**
- ✅ **Decision:** Always mount `/Applications/Xcode.app` (standard location)
- Users with multiple Xcodes can use `xcode-select` on host to symlink preferred version
- Keep it simple - don't try to detect or choose between multiple installations

**3. Runtime version management:**
- ✅ **Decision:** Mount entire `/Library/Developer/CoreSimulator/Volumes/` directory
- Includes all installed runtimes (iOS, tvOS, watchOS, visionOS, all versions)
- Let xcodebuild/simctl choose appropriate runtime based on project requirements
- No manual selection or configuration needed

**4. Simulator device persistence:**
- ✅ **Decision:** Devices stored in VM's `~/Library/Developer/CoreSimulator/Devices/`
- Backed by VM disk - persist across VM restarts automatically
- Lost when VM is deleted/recreated (expected behavior)
- No special handling needed

**5. tvOS/watchOS/visionOS support:**
- ✅ **Decision:** Already works! All simulator platforms in same `Volumes/` directory
- Mounting entire `Volumes/` provides all simulator types automatically
- No future enhancement needed - works out of the box

**6. Interactive prompts:**
- ✅ **Decision:** No prompts, warnings, or user interaction
- Silent operation - mounts either exist or they don't
- User discovers iOS testing capability by trying to use it
- Removed from design entirely

## Success Metrics

- VM disk size: ≤30GB (with host Xcode + runtimes) vs 100GB baseline
- Setup time: Instant (just mount configuration, no detection/validation)
- Zero configuration: Works automatically if host has tools
- Dynamic updates: New host runtimes available on VM restart
- Silent operation: No prompts, warnings, or user intervention
- Clear separation: yoloAI provides plumbing, user provides tools
- Just works: iOS testing available if host has Xcode, otherwise gracefully absent

## References

- Investigation: `docs/dev/ios-testing-investigation.md`
- Tart documentation: https://github.com/cirruslabs/tart
- Xcode command-line tools: `man xcodebuild`, `man simctl`
- VirtioFS: https://virtio-fs.gitlab.io/

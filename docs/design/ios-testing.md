# iOS Testing Support Design

**Status:** Design proposal
**Last updated:** 2026-03-26
**Related:** `docs/dev/ios-testing-investigation.md` (investigation and validation)

## Overview

Tart VMs automatically mount Xcode and simulator runtimes from the host, enabling iOS/tvOS/watchOS/visionOS testing with minimal VM disk usage.

### Goals

1. Automatic iOS simulator testing support in Tart VMs
2. Minimize VM disk usage through VirtioFS mounting (~11GB usage vs ~100GB fully local)
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
│  On VM start (every time):                                  │
│    Check host for:                                          │
│      /Applications/Xcode.app                                │
│      /Library/Developer/CoreSimulator/Volumes/              │
│      /Library/Developer/PrivateFrameworks                   │
│    For each path that exists:                               │
│      Add --dir argument to tart run command                 │
│    Tart mounts directories via VirtioFS                     │
│    Setup script detects mounts and configures paths         │
│                                                              │
│  Result:                                                    │
│    - Host has Xcode → iOS testing works automatically       │
│    - Install Xcode later → works on next VM start           │
│    - Install new runtime → available on next VM start       │
│    - Host has no Xcode → VM works normally (no iOS)         │
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
- User installs Xcode on host → next VM start detects and mounts it automatically
- User installs new simulator runtime → available on next VM start
- No VM regeneration needed - paths are checked at every start

## Implementation

### Phase 1: Tart Runtime Updates

#### 1. Tart Runtime (`runtime/tart/tart.go`)

Update `buildRunArgs()` to check for Xcode paths at VM start time (not creation time):

```go
func (r *Runtime) buildRunArgs(vmName, sandboxPath string, mounts []runtime.MountSpec) []string {
    args := []string{"run", "--no-graphics"}

    // Share the sandbox directory into the VM
    args = append(args, "--dir", fmt.Sprintf("%s:%s", sharedDirName, sandboxPath))

    // Build merged mount list: Xcode system paths + user-specified mounts
    // Deduplication: user-specified mounts take precedence over system paths
    mergedMounts := make(map[string]runtime.MountSpec) // key = Source path

    // 1. Add Xcode system paths (checked at every start)
    xcodePaths := []struct {
        host string
        name string
    }{
        {"/Applications/Xcode.app", "m-Xcode.app"},
        {"/Library/Developer/CoreSimulator/Volumes", "m-coresim-runtime"},
        {"/Library/Developer/PrivateFrameworks", "m-PrivateFrameworks"},
    }

    for _, p := range xcodePaths {
        if info, err := os.Stat(p.host); err == nil && info.IsDir() {
            mergedMounts[p.host] = runtime.MountSpec{
                Source:   p.host,
                Target:   "/Volumes/My Shared Files/" + p.name,
                ReadOnly: true,
            }
        }
    }

    // 2. Add user-specified mounts (override system paths if same Source)
    for _, m := range mounts {
        // Skip anything under the sandbox dir (already shared)
        if strings.HasPrefix(m.Source, sandboxPath+"/") || m.Source == sandboxPath {
            continue
        }
        // Skip files — VirtioFS only supports directories
        if info, err := os.Stat(m.Source); err != nil || !info.IsDir() {
            continue
        }
        mergedMounts[m.Source] = m  // Overwrites system path if duplicate
    }

    // 3. Build --dir arguments from merged list
    for _, m := range mergedMounts {
        dirName := mountDirName(m.Source)
        dirSpec := fmt.Sprintf("%s:%s", dirName, m.Source)
        if m.ReadOnly {
            dirSpec += ":ro"
        }
        args = append(args, "--dir", dirSpec)
    }

    return append(args, vmName)
}
```

**Integration with existing runtime:**
- Called from `Start()` at line 238 (already exists)
- Receives `cfg.Mounts` loaded from instance.json (line 227-235)
- Returns `--dir` arguments passed to `tart run` (line 251)

**Deduplication strategy:**
- User-specified mounts **override** system Xcode paths if same Source
- Example: User mounts `/Applications/Xcode.app:rw` → takes precedence over system's `:ro`
- Prevents duplicate `--dir` arguments for the same path

**Key points:**
- Xcode paths checked at **every VM start** (not stored in instance.json)
- Paths probed regardless of whether they existed at creation time
- User installs Xcode later → automatically detected on next start
- No VM recreation needed

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
            result = subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer],
                                  capture_output=True, text=True)
            if result.returncode != 0:
                syslog.syslog(syslog.LOG_ERR, f"Failed to configure xcode-select: {result.stderr}")

            # Add to both zsh and bash profiles for persistence
            for profile in ["~/.zprofile", "~/.bash_profile"]:
                profile_path = os.path.expanduser(profile)
                with open(profile_path, "a") as f:
                    f.write(f'export DEVELOPER_DIR="{xcode_developer}"\n')
                    f.write(f'export PATH="{xcode_developer}/usr/bin:$PATH"\n')

        # Symlink mounted runtimes to system location if present
        runtime_mount = "/Volumes/My Shared Files/m-coresim-runtime"
        runtime_target = "/Library/Developer/CoreSimulator/Volumes"

        if os.path.isdir(runtime_mount):
            # Only remove if it's already a symlink (safe)
            if os.path.islink(runtime_target):
                result = subprocess.run(["sudo", "rm", runtime_target],
                                      capture_output=True, text=True)
                if result.returncode != 0:
                    syslog.syslog(syslog.LOG_ERR, f"Failed to remove runtime symlink: {result.stderr}")

            # Create symlink (ln -sfn handles overwriting existing symlinks)
            result = subprocess.run(["sudo", "ln", "-sfn", runtime_mount, runtime_target],
                                  capture_output=True, text=True)
            if result.returncode != 0:
                syslog.syslog(syslog.LOG_ERR, f"Failed to create runtime symlink: {result.stderr}")
```

**Note:** Silent operation - no output unless errors. Mounts either exist or they don't.

#### 3. Agent Context (optional)

Add iOS testing info to sandbox's CLAUDE.md if available:

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
                f.write("iOS/tvOS/watchOS/visionOS simulator testing is available.\n\n")
                f.write("Check available runtimes:\n")
                f.write("```bash\n")
                f.write("xcrun simctl list runtimes\n")
                f.write("```\n\n")
                f.write("Run tests:\n")
                f.write("```bash\n")
                f.write("xcodebuild test -scheme YourScheme \\\n")
                f.write("  -destination 'platform=iOS Simulator,name=iPhone 17 Pro'\n")
                f.write("```\n")
```

**Note:** Optional enhancement - agents can discover iOS testing on their own, but including examples directly in CLAUDE.md helps them get started quickly.

### Phase 2: Documentation

Add iOS testing section to user-facing documentation:

**docs/GUIDE.md** - Add iOS testing section:

```markdown
## iOS Simulator Testing (Tart only)

Tart VMs automatically mount Xcode and simulator runtimes from your Mac.

### Prerequisites (on host Mac)
- Xcode installed at `/Applications/Xcode.app`
- iOS/tvOS/watchOS/visionOS runtimes (download in Xcode > Settings > Components)

### How it works
1. Create any Tart VM: `yoloai new mysandbox`
2. If host has Xcode → VM automatically mounts it
3. If host has simulator runtimes → VM mounts them all
4. No configuration needed - just works

### Example: Running iOS tests

\`\`\`bash
# Create sandbox (automatic iOS testing if Xcode on host)
yoloai new embsdk ~/Projects/my-ios-app

# Check what simulator runtimes are available
yoloai exec embsdk -- xcrun simctl list runtimes

# Run iOS unit tests
yoloai exec embsdk -- xcodebuild test \\
  -scheme MyApp \\
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' \\
  -resultBundlePath /tmp/test-results

# Run tests on multiple platforms
yoloai exec embsdk -- xcodebuild test -scheme MyApp \\
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' \\
  -destination 'platform=tvOS Simulator,name=Apple TV 4K'
\`\`\`

### Adding more simulator runtimes

1. On host Mac: Open Xcode > Settings > Components
2. Download desired runtimes (iOS, tvOS, watchOS, visionOS)
3. Restart sandbox: `yoloai exec embsdk -- exit` then run again
4. New runtimes available immediately (no VM regeneration needed)

### Disk usage
- With host Xcode + runtimes: ~11GB used (in 50GB VM)
- Without host tools: ~20GB used (normal development, no iOS testing)

### Troubleshooting

**iOS tests don't work:**
1. Verify Xcode installed on host: `ls /Applications/Xcode.app`
2. Check runtimes on host: `xcrun simctl list runtimes` (run on host)
3. Install missing tools on host, then restart VM

**Specific runtime missing:**
- Install on host in Xcode > Settings > Components
- Restart VM to pick up new runtime
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
1. Install Xcode on host at `/Applications/Xcode.app` (default location)
2. Download simulator runtimes in Xcode > Settings > Platforms & SDKs
3. Restart VM (no regeneration needed)
4. iOS testing now works

**Xcode installation quality:**
- yoloAI checks `os.path.isdir()` on `/Applications/Xcode.app`
- Does NOT validate that Xcode is complete, uncorrupted, or functional
- User responsibility to ensure Xcode is properly installed and licensed
- Incomplete/corrupted Xcode will mount but xcodebuild will fail with errors
- Troubleshooting: User should verify Xcode works on host before filing issues

**Implementation notes:**
- No warnings or prompts during VM creation
- No detection or validation logic needed
- Clean separation: yoloAI provides plumbing, user provides tools

## Testing Strategy

### Unit Tests

- Mount configuration in `buildRunArgs()` (all Xcode paths checked at start)
- Setup script iOS detection and configuration
- CLAUDE.md generation when Xcode present

### Integration Tests

- Create VM, verify no Xcode mount specs in instance.json (checked at start, not creation)
- Start VM with Xcode on host → iOS testing works
- Start VM without Xcode on host → VM works normally (no iOS)
- Install Xcode on host, restart VM → iOS testing now works (no recreation needed)
- Install new runtime on host, restart VM → new runtime available

### Manual Testing Checklist

**Basic scenarios:**
- [ ] Host with Xcode + runtimes → iOS testing works automatically
- [ ] Host with Xcode only → xcodebuild works, simctl shows no runtimes
- [ ] Host with no Xcode → VM works normally (no iOS testing)
- [ ] Install Xcode on host, restart VM → iOS testing now works
- [ ] Install new runtime on host, restart VM → new runtime available
- [ ] Multiple VMs share host's Xcode simultaneously

**Dynamic changes:**
- [ ] Remove Xcode on host while VM running → VM continues working (mount stays active)
- [ ] Restart VM after Xcode removed → VM starts normally (no iOS testing, no errors)
- [ ] Update Xcode on host while VM running → VM uses old version until restart
- [ ] Restart VM after Xcode update → VM uses new Xcode version

**Deduplication:**
- [ ] User explicitly mounts `/Applications/Xcode.app:rw` → user mount takes precedence
- [ ] No duplicate --dir arguments in tart run command

**Setup and configuration:**
- [ ] Setup script silent operation (no output unless errors)
- [ ] xcode-select points to mounted Xcode
- [ ] Symlink to mounted runtimes created correctly
- [ ] Error logging to syslog when xcode-select or symlink commands fail

## Documentation Updates

### User-facing docs

1. **`docs/GUIDE.md`** - Add iOS testing section:
   - Prerequisites (Xcode on host)
   - Automatic mounting behavior (no flags needed)
   - How to add simulator runtimes
   - Example usage

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
- ✅ **Decision:** Xcode paths checked and mounted at every VM start
- Updating Xcode on host while VM is running won't affect VM until restart
- Installing Xcode after VM creation works automatically on next start
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

- VM disk usage: ~11GB (with host tools mounted) vs ~100GB (fully local)
- VM image size: 50GB (standard Tart base, regardless of iOS testing)
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

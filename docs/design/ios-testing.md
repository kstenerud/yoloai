# iOS Testing Support Design

**Status:** Design proposal
**Last updated:** 2026-03-26
**Related:** `docs/dev/ios-testing-investigation.md` (investigation and validation)

## Overview

Enable iOS simulator testing in yoloAI sandboxes using Tart VMs with efficient disk usage through VirtioFS directory mounting.

### Goals

1. Support iOS simulator testing for Xcode projects
2. Minimize VM disk usage through intelligent mounting
3. Provide opt-in experience (don't force large VMs on all users)
4. Auto-detect host capabilities and choose best approach
5. Graceful degradation when host lacks iOS development tools

### Non-goals

- macOS Seatbelt backend support (fundamentally impossible due to nested sandboxing)
- Docker backend support (no macOS container support)
- Physical iOS device testing (out of scope)
- Xcode UI testing (focus on command-line xcodebuild)

## Architecture

### Single approach: Mount what's available

When `--ios-testing` is specified:
1. Check what's available on host (Xcode, iOS runtime)
2. Mount whatever is found
3. Warn about anything missing
4. Let user decide whether to install tools and regenerate

```
┌─────────────────────────────────────────────────────────────┐
│ iOS Testing Setup Flow                                      │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Check host:                                                │
│    /Applications/Xcode.app         → mount if exists       │
│    /Library/Developer/CoreSimulator/Volumes/ → mount if exists │
│    /Library/Developer/PrivateFrameworks      → mount if exists │
│                                                              │
│  Warn if missing:                                           │
│    ⚠ Xcode not found - install on host for best performance│
│    ⚠ iOS runtime not found - will need to download in VM   │
│                                                              │
│  User options:                                              │
│    1. Continue with what's available                        │
│    2. Install missing tools on host, recreate sandbox       │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### Configuration: Best case (Xcode + Runtime on host)

**When host has Xcode + iOS runtime:**

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

**Disk usage:**
- Mounted: ~27GB (Xcode 11GB + runtime 16GB, on host)
- Local: ~11GB actual usage
- **VM size needed: 25-30GB**
- **Savings: ~70GB per VM** vs fully local

**Key discovery:** The 3.8GB dyld cache is NOT needed when runtime is mounted (validated via symlink testing).

### Missing components handling

**If host has no Xcode:**
```
⚠ Warning: Xcode not found at /Applications/Xcode.app

To enable iOS testing:
  1. Install Xcode from App Store or Apple Developer
  2. Recreate sandbox: yoloai delete <name> && yoloai new <name> --ios-testing

Continuing without Xcode - iOS testing will not be available.
```

**If host has Xcode but no iOS runtime:**
```
⚠ Warning: iOS runtime not found in /Library/Developer/CoreSimulator/Volumes/

Xcode will be mounted, but you'll need to download iOS runtime in VM:
  - This will use ~16GB of VM disk space
  - Download: yoloai exec <name> -- xcodebuild -downloadPlatform iOS

Or install runtime on host and recreate sandbox for better disk efficiency.

VM size: ~40-50GB (vs ~25-30GB with host runtime)
```

**User choice:**
- Continue anyway and work with what's available
- Ctrl+C, install missing components, recreate sandbox
- User decides - no automatic fallbacks or hidden behavior

## Implementation

### Phase 1: Core Infrastructure

#### 1. Detection Module (`internal/ios/detect.go`)

```go
package ios

// HostCapabilities represents what iOS development tools are available on host
type HostCapabilities struct {
    HasXcode          bool
    XcodePath         string
    XcodeVersion      string
    HasIOSRuntime     bool
    IOSRuntimeVersions []string
    RuntimePath       string
}

// DetectHostCapabilities checks what iOS development tools are available
func DetectHostCapabilities() (*HostCapabilities, error) {
    // Check for Xcode.app
    // Check for iOS runtimes in /Library/Developer/CoreSimulator/Volumes/
    // Parse versions
    // Return what's found - don't make decisions
}

// Warnings returns user-facing warnings about missing components
func (hc *HostCapabilities) Warnings() []string {
    var warnings []string
    if !hc.HasXcode {
        warnings = append(warnings, "Xcode not found - install on host for iOS testing")
    }
    if !hc.HasIOSRuntime {
        warnings = append(warnings, "iOS runtime not found - will need to download in VM (~16GB)")
    }
    return warnings
}
```

#### 2. Tart Runtime Updates (`internal/runtime/tart/tart.go`)

Update `addSystemMounts()` to include iOS testing mounts when enabled:

```go
func (r *Runtime) addSystemMounts(cfg *runtime.InstanceConfig, iosTestingEnabled bool) {
    homeDir := config.HomeDir()

    // Existing: Xcode.app mount (always check if ios-testing enabled)
    if iosTestingEnabled {
        xcodeAppHost := "/Applications/Xcode.app"
        if info, err := os.Stat(xcodeAppHost); err == nil && info.IsDir() {
            cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
                Source:   xcodeAppHost,
                Target:   "/Volumes/My Shared Files/m-Xcode.app",
                ReadOnly: true,
            })
        }

        // NEW: System CoreSimulator runtime mount (if exists)
        runtimePath := "/Library/Developer/CoreSimulator/Volumes"
        if info, err := os.Stat(runtimePath); err == nil && info.IsDir() {
            cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
                Source:   runtimePath,
                Target:   "/Volumes/My Shared Files/m-coresim-runtime",
                ReadOnly: true,
            })
        }

        // PrivateFrameworks mount (if exists)
        privateFrameworks := "/Library/Developer/PrivateFrameworks"
        if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
            cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
                Source:   privateFrameworks,
                Target:   "/Volumes/My Shared Files/m-PrivateFrameworks",
                ReadOnly: true,
            })
        }
    }
}
```

#### 3. Setup Script Updates (`internal/runtime/monitor/sandbox-setup.py`)

Add iOS testing setup logic:

```python
class TartBackend:
    def setup_ios_testing(self):
        """Configure iOS testing with whatever's available from mounts"""

        # Check if Xcode is mounted
        xcode_developer = "/Volumes/My Shared Files/m-Xcode.app/Contents/Developer"
        if os.path.isdir(xcode_developer):
            # Use mounted Xcode
            subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer])

            # Add to shell profile
            with open(os.path.expanduser("~/.zprofile"), "a") as f:
                f.write(f'export DEVELOPER_DIR="{xcode_developer}"\n')
                f.write(f'export PATH="{xcode_developer}/usr/bin:$PATH"\n')

            print("✓ Using Xcode from host mount")
        else:
            print("⚠ Xcode not mounted - iOS testing not available")
            return

        # Check if runtime is mounted
        runtime_mount = "/Volumes/My Shared Files/m-coresim-runtime"
        runtime_target = "/Library/Developer/CoreSimulator/Volumes"

        if os.path.isdir(runtime_mount):
            # Symlink mounted runtime to system location
            subprocess.run(["sudo", "rm", "-rf", runtime_target])
            subprocess.run(["sudo", "ln", "-sfn", runtime_mount, runtime_target])
            print("✓ Using iOS runtime from host mount")
        else:
            print("⚠ iOS runtime not mounted - download in VM with:")
            print("  xcodebuild -downloadPlatform iOS")
            print("  (This will use ~16GB of VM disk space)")
```

### Phase 2: CLI Integration

#### New Command: `yoloai new` flag

Add `--ios-testing` flag to `yoloai new`:

```go
// internal/cli/new.go

type NewOptions struct {
    // Existing fields...
    IOSTesting bool `flag:"--ios-testing" desc:"Enable iOS simulator testing support"`
}

func (o *NewOptions) Run() error {
    // Existing validation...

    if o.IOSTesting {
        // Detect what's available on host
        caps, err := ios.DetectHostCapabilities()
        if err != nil {
            return fmt.Errorf("failed to detect iOS capabilities: %w", err)
        }

        // Show what we found
        fmt.Println("Checking host for iOS development tools...")
        if caps.HasXcode {
            fmt.Printf("✓ Found Xcode %s\n", caps.XcodeVersion)
        }
        if caps.HasIOSRuntime {
            fmt.Printf("✓ Found iOS runtime: %s\n", strings.Join(caps.IOSRuntimeVersions, ", "))
        }

        // Show warnings about missing components
        warnings := caps.Warnings()
        for _, warning := range warnings {
            fmt.Printf("⚠ %s\n", warning)
        }

        // Let user decide whether to continue
        if len(warnings) > 0 {
            fmt.Println("\nContinue anyway? (y/N)")
            // Read user input, abort if not 'y'
        }

        // Suggest appropriate VM size based on what's available
        suggestedDiskSize := 30 // Default for full mount
        if !caps.HasIOSRuntime {
            suggestedDiskSize = 50 // Need space for runtime download
        }
        if !caps.HasXcode {
            suggestedDiskSize = 100 // Need space for everything
        }
        if diskSize < suggestedDiskSize {
            fmt.Printf("⚠ Suggested VM size for this configuration: %dGB (you specified %dGB)\n",
                suggestedDiskSize, diskSize)
        }

        // Store capability info in metadata for setup script
        metadata.IOSTestingEnabled = true
        metadata.HostHasXcode = caps.HasXcode
        metadata.HostHasRuntime = caps.HasIOSRuntime
    }

    // Continue with VM creation...
}
```

#### User Experience

```bash
# Best case: Host has everything
$ yoloai new embsdk --ios-testing
Checking host for iOS development tools...
✓ Found Xcode 26.1.1
✓ Found iOS runtime: 26.1

Creating VM with iOS testing support...
VM size: 30GB
Xcode and runtime will be mounted from host.

# Missing runtime
$ yoloai new embsdk --ios-testing
Checking host for iOS development tools...
✓ Found Xcode 26.1.1
⚠ iOS runtime not found - will need to download in VM (~16GB)

Continue anyway? (y/N) y

Creating VM with iOS testing support...
VM size: 50GB
After VM creation, download runtime with:
  yoloai exec embsdk -- xcodebuild -downloadPlatform iOS

# Missing Xcode
$ yoloai new embsdk --ios-testing
Checking host for iOS development tools...
⚠ Xcode not found - install on host for iOS testing

To install Xcode:
  1. Download from App Store or https://developer.apple.com
  2. Run: sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer
  3. Recreate sandbox: yoloai new embsdk --ios-testing

Abort? (Y/n) y
Cancelled.
```

### Phase 3: Configuration

#### Profile-based iOS testing

Allow profiles to specify iOS testing preferences:

```yaml
# ~/.yoloai/profiles/ios-dev/config.yaml
tart:
  disk_size: 30  # Adjust based on what's available on host

ios_testing:
  enabled: true
  # Mounts whatever is available on host
  # Shows warnings if components missing

env:
  - DEVELOPER_DIR=/Volumes/My Shared Files/m-Xcode.app/Contents/Developer
```

### Phase 4: Error Handling

#### Common failure scenarios

1. **No Xcode on host**
   ```
   ⚠ Xcode not found at /Applications/Xcode.app

   iOS testing requires Xcode on host. To fix:
   1. Install Xcode from App Store or https://developer.apple.com
   2. Run: sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer
   3. Recreate sandbox: yoloai new <name> --ios-testing

   Continue without Xcode? iOS testing will not work. (y/N)
   ```

2. **No iOS runtime on host**
   ```
   ⚠ iOS runtime not found in /Library/Developer/CoreSimulator/Volumes/

   Options:
   1. Install on host (recommended):
      - Open Xcode > Settings > Components
      - Download iOS runtime
      - Recreate sandbox for better disk efficiency
   2. Download in VM (uses ~16GB VM disk):
      - yoloai exec <name> -- xcodebuild -downloadPlatform iOS

   Continue with in-VM download? (y/N)
   ```

3. **Disk space insufficient**
   ```
   ⚠ Specified VM size (20GB) may be insufficient for iOS testing

   Recommended sizes:
   - With host runtime: 30GB
   - Without host runtime: 50GB (need space for ~16GB runtime download)

   Continue anyway? (y/N)
   ```

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

- [ ] Host with Xcode + runtime → both mounted, no warnings
- [ ] Host with Xcode only → Xcode mounted, warning about runtime
- [ ] Host with no Xcode → warning, offer to abort
- [ ] User can choose to continue despite warnings
- [ ] Suggested VM sizes match configuration
- [ ] VM restart preserves iOS testing configuration
- [ ] Multiple VMs can share host's Xcode
- [ ] Warning messages are clear and actionable
- [ ] Setup script correctly detects mounts and configures paths

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

### Multiple iOS runtime support

Allow mounting multiple iOS versions:

```yaml
ios_testing:
  runtimes:
    - version: "26.1"
      mount: true  # Mount from host
    - version: "25.0"
      download: true  # Download in VM
```

### Runtime version selection

Auto-detect required iOS version from Xcode project:

```go
// Parse .xcodeproj to find IPHONEOS_DEPLOYMENT_TARGET
// Select matching runtime
// Download if not available
```

### Xcode version management

Support multiple Xcode versions on host:

```bash
# Use specific Xcode version
yoloai new embsdk --ios-testing --xcode-version 15.4
```

### Performance optimization

- Parallel runtime downloads
- Cached runtime downloads in yoloAI cache directory
- Shared runtime mounts across multiple VMs

## Open Questions

1. **Xcode updates:**
   - What happens when host Xcode is updated while VM is running?
   - **Proposed:** Document that VM restart required after host Xcode update
   - Could detect version mismatch on VM start and warn?

2. **Multiple Xcode installations:**
   - Some developers have multiple Xcode versions (Xcode.app, Xcode-beta.app)
   - **Proposed:** Always use `/Applications/Xcode.app`, ignore others
   - Or use `xcode-select -p` to find active version?

3. **Runtime version management:**
   - If host has multiple iOS runtimes, which to mount?
   - **Proposed:** Mount entire Volumes directory (includes all runtimes)
   - Let xcodebuild/simctl choose appropriate version

4. **Simulator device persistence:**
   - Simulator devices are stored in VM's CoreSimulator/Devices (backed by disk)
   - Persist across VM restarts automatically
   - Recreating VM loses devices (expected behavior)

5. **tvOS/watchOS/visionOS support:**
   - Same approach should work for other simulator platforms
   - Runtimes all in same /Library/Developer/CoreSimulator/Volumes/
   - **Proposed:** Future enhancement, same `--ios-testing` flag covers all

6. **Interactive prompts:**
   - Should warnings require user confirmation or just display?
   - **Proposed:** Display warnings, let user Ctrl+C if they want to abort
   - Avoid blocking prompts unless critically necessary

## Success Metrics

- VM disk size for iOS testing: ≤30GB (with host runtime) vs 100GB baseline
- Setup time: <5 minutes (with host Xcode + runtime)
- Clear warnings when components missing
- User understands what they need to install
- User can run iOS tests immediately after `yoloai new` (if host has all components)
- No hidden fallbacks or surprising behavior

## References

- Investigation: `docs/dev/ios-testing-investigation.md`
- Tart documentation: https://github.com/cirruslabs/tart
- Xcode command-line tools: `man xcodebuild`, `man simctl`
- VirtioFS: https://virtio-fs.gitlab.io/

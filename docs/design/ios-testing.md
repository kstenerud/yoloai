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

### Three-tier approach

Based on what's available on the host, automatically select the best option:

```
┌─────────────────────────────────────────────────────────────┐
│ Host Capabilities Check                                     │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Host has Xcode + iOS runtime?                              │
│    ↓ YES: Option B (mount both) → ~25-30GB VM              │
│    ↓ NO                                                      │
│                                                              │
│  Host has Xcode (no runtime)?                               │
│    ↓ YES: Option A (mount Xcode only) → ~40-50GB VM        │
│    ↓ NO                                                      │
│                                                              │
│  Host has no Xcode?                                         │
│    ↓ Fully local install → ~100GB VM                        │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### Option B: Mount Xcode + Runtime (Recommended)

**Best case:** Host has Xcode and iOS runtime installed.

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

### Option A: Mount Xcode Only (Fallback)

**Case:** Host has Xcode but no iOS runtime.

**VM Configuration:**
```
Mounts from host (read-only):
  /Applications/Xcode.app
  /Library/Developer/PrivateFrameworks

Local in VM (writable):
  /Library/Developer/CoreSimulator/           (~20GB - runtime + devices)
  /opt/homebrew/                              (~2GB)
  /private/var/                               (~1GB)
```

**Disk usage:**
- Mounted: ~13GB (on host)
- Local: ~24GB
- **VM size needed: 40-50GB**
- **Savings: ~50GB per VM** vs fully local

**Setup:** Download iOS runtime in VM via `xcodebuild -downloadPlatform iOS`

### Option C: Fully Local (Last Resort)

**Case:** Host has no Xcode installed.

**VM Configuration:**
```
All local in VM:
  /Applications/Xcode.app                     (~11GB)
  /Library/Developer/CoreSimulator/           (~20GB)
  /Library/Developer/PrivateFrameworks        (~2GB)
  /opt/homebrew/                              (~2GB)
  /private/var/                               (~1GB)
```

**Disk usage:**
- Local: ~36GB minimum
- **VM size needed: 100GB** (with buffer for builds)
- **Savings: None**

**Setup:**
1. Copy Xcode from mounted location (if available) or prompt user to install
2. Download iOS runtime via `xcodebuild -downloadPlatform iOS`

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
}

// RecommendedApproach returns which option to use based on host capabilities
func (hc *HostCapabilities) RecommendedApproach() ApproachType {
    if hc.HasXcode && hc.HasIOSRuntime {
        return ApproachB // Mount both
    }
    if hc.HasXcode {
        return ApproachA // Mount Xcode only
    }
    return ApproachC // Fully local
}
```

#### 2. Tart Runtime Updates (`internal/runtime/tart/tart.go`)

Update `addSystemMounts()` to optionally include iOS runtime:

```go
func (r *Runtime) addSystemMounts(cfg *runtime.InstanceConfig, iosTestingMode IOSTestingMode) {
    homeDir := config.HomeDir()

    // Existing: Xcode.app mount
    if iosTestingMode >= IOSTestingModeA {
        xcodeAppHost := "/Applications/Xcode.app"
        if info, err := os.Stat(xcodeAppHost); err == nil && info.IsDir() {
            cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
                Source:   xcodeAppHost,
                Target:   "/Volumes/My Shared Files/m-Xcode.app",
                ReadOnly: true,
            })
        }
    }

    // NEW: System CoreSimulator runtime mount (Option B)
    if iosTestingMode == IOSTestingModeB {
        runtimePath := "/Library/Developer/CoreSimulator/Volumes"
        if info, err := os.Stat(runtimePath); err == nil && info.IsDir() {
            cfg.Mounts = append(cfg.Mounts, runtime.MountSpec{
                Source:   runtimePath,
                Target:   "/Volumes/My Shared Files/m-coresim-runtime",
                ReadOnly: true,
            })
        }
    }

    // Existing: PrivateFrameworks mount
    // ...
}
```

#### 3. Setup Script Updates (`internal/runtime/monitor/sandbox-setup.py`)

Add iOS testing setup logic:

```python
class TartBackend:
    def setup_ios_testing(self, approach: str):
        """Configure iOS testing based on selected approach"""

        if approach in ['A', 'B']:
            # Point to mounted Xcode
            xcode_developer = "/Volumes/My Shared Files/m-Xcode.app/Contents/Developer"
            if os.path.isdir(xcode_developer):
                subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer])

                # Add to shell profile
                with open(os.path.expanduser("~/.zprofile"), "a") as f:
                    f.write(f'export DEVELOPER_DIR="{xcode_developer}"\n')
                    f.write(f'export PATH="{xcode_developer}/usr/bin:$PATH"\n')

        if approach == 'B':
            # Symlink mounted runtime to system location
            runtime_mount = "/Volumes/My Shared Files/m-coresim-runtime"
            runtime_target = "/Library/Developer/CoreSimulator/Volumes"

            if os.path.isdir(runtime_mount):
                subprocess.run(["sudo", "rm", "-rf", runtime_target])
                subprocess.run(["sudo", "ln", "-sfn", runtime_mount, runtime_target])

        elif approach == 'A':
            # Download iOS runtime locally
            subprocess.run(["xcodebuild", "-downloadPlatform", "iOS"])

        elif approach == 'C':
            # Copy Xcode locally if available from mount
            xcode_mount = "/Volumes/My Shared Files/m-Xcode.app"
            if os.path.isdir(xcode_mount):
                subprocess.run(["sudo", "ditto", xcode_mount, "/Applications/Xcode.app"])
                subprocess.run(["sudo", "xcode-select", "--switch",
                              "/Applications/Xcode.app/Contents/Developer"])

            # Download iOS runtime
            subprocess.run(["xcodebuild", "-downloadPlatform", "iOS"])
```

### Phase 2: CLI Integration

#### New Command: `yoloai new` flag

Add `--ios-testing` flag to `yoloai new`:

```go
// internal/cli/new.go

type NewOptions struct {
    // Existing fields...
    IOSTesting bool   `flag:"--ios-testing" desc:"Enable iOS simulator testing support"`
    IOSApproach string `flag:"--ios-approach" desc:"Force specific approach: auto, A, B, or C (default: auto)"`
}

func (o *NewOptions) Run() error {
    // Existing validation...

    if o.IOSTesting {
        // Detect host capabilities
        caps, err := ios.DetectHostCapabilities()
        if err != nil {
            return fmt.Errorf("failed to detect iOS capabilities: %w", err)
        }

        // Choose approach
        approach := caps.RecommendedApproach()
        if o.IOSApproach != "" && o.IOSApproach != "auto" {
            approach = parseApproach(o.IOSApproach)
        }

        // Validate disk size for chosen approach
        requiredDisk := approach.RequiredDiskSize()
        if diskSize < requiredDisk {
            return fmt.Errorf("iOS testing with approach %s requires at least %dGB disk, got %dGB",
                approach, requiredDisk, diskSize)
        }

        // Store approach in metadata for setup script
        metadata.IOSTestingApproach = approach.String()

        // Configure mounts
        addIOSMounts(instanceConfig, approach)
    }

    // Continue with VM creation...
}
```

#### User Experience

```bash
# Auto-detect and use best approach
$ yoloai new embsdk --ios-testing
Detecting host iOS development tools...
✓ Found Xcode 26.1.1 at /Applications/Xcode.app
✓ Found iOS 26.1 runtime
Using approach B: mount Xcode + runtime
VM size: 30GB (saving ~70GB vs fully local)
Creating VM...

# Force specific approach
$ yoloai new embsdk --ios-testing --ios-approach A
Using approach A: mount Xcode only (local runtime)
VM size: 50GB
Downloading iOS runtime in VM...

# Host has no Xcode
$ yoloai new embsdk --ios-testing
Detecting host iOS development tools...
✗ No Xcode found on host
Using approach C: fully local installation
VM size: 100GB
⚠ Warning: Large VM required. Consider installing Xcode on host to reduce VM size.
Creating VM...
```

### Phase 3: Configuration

#### Profile-based iOS testing

Allow profiles to specify iOS testing preferences:

```yaml
# ~/.yoloai/profiles/ios-dev/config.yaml
tart:
  disk_size: 30  # Smaller for Option B

ios_testing:
  enabled: true
  approach: auto  # or force: A, B, C
  runtime_version: "26.1"  # Optional: specific iOS version

env:
  - DEVELOPER_DIR=/Volumes/My Shared Files/m-Xcode.app/Contents/Developer
```

### Phase 4: Error Handling

#### Common failure scenarios

1. **Host Xcode version mismatch**
   ```
   Error: iOS runtime in VM (26.1) requires Xcode 26.x, but host has Xcode 15.4

   Options:
   - Upgrade host Xcode to 26.x
   - Use --ios-approach C to install Xcode in VM
   - Use different iOS runtime version
   ```

2. **Runtime download failure**
   ```
   Error: Failed to download iOS 26.1 runtime (network error)

   Options:
   - Retry: yoloai exec embsdk -- xcodebuild -downloadPlatform iOS
   - Use different runtime: xcodebuild -downloadPlatform iOS -platform iOS26.0
   - Check network connection
   ```

3. **Disk space insufficient**
   ```
   Error: VM disk full during runtime download (need 8GB, have 2GB available)

   Options:
   - Recreate VM with larger disk: yoloai delete embsdk && yoloai new embsdk --disk-size 50
   - Use approach B to mount runtime from host
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

- [ ] Host with Xcode + runtime → Option B selected
- [ ] Host with Xcode only → Option A selected
- [ ] Host with no Xcode → Option C selected
- [ ] Force approach A → works correctly
- [ ] Force approach B → works correctly
- [ ] Force approach C → works correctly
- [ ] VM restart preserves iOS testing configuration
- [ ] Multiple VMs can share host's Xcode (Option B)
- [ ] Error messages are helpful and actionable

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

1. **Runtime version matching:**
   - What if host has iOS 26.1 but project needs iOS 25.0?
   - Auto-download in VM? Prompt user?
   - **Proposed:** Detect from project, download in VM if mismatch

2. **Xcode updates:**
   - What happens when host Xcode is updated while VM is running?
   - **Proposed:** Document that VM restart required after host Xcode update

3. **Multiple Xcode installations:**
   - Some developers have multiple Xcode versions (Xcode.app, Xcode-beta.app)
   - **Proposed:** Use `xcode-select -p` to find active version

4. **Simulator device persistence:**
   - Should simulator devices persist across VM recreations?
   - **Proposed:** Yes, stored in VM's CoreSimulator/Devices (backed by disk)

5. **tvOS/watchOS/visionOS support:**
   - Same approach should work for other simulator platforms
   - **Proposed:** Future enhancement, same `--ios-testing` flag covers all

## Success Metrics

- VM disk size for iOS testing: ≤30GB (Option B) vs 100GB baseline
- Setup time: <5 minutes (Option B, runtime already on host)
- Zero manual configuration required (auto-detect works)
- User can run iOS tests immediately after `yoloai new`

## References

- Investigation: `docs/dev/ios-testing-investigation.md`
- Tart documentation: https://github.com/cirruslabs/tart
- Xcode command-line tools: `man xcodebuild`, `man simctl`
- VirtioFS: https://virtio-fs.gitlab.io/

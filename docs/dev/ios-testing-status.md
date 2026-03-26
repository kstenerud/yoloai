# iOS Testing Implementation Status

**Date:** 2026-03-26
**Status:** Design complete, pending critical test before implementation

## Current State

Design document is complete at `docs/design/ios-testing.md` with commit history:
- `fc20a92` - Include iOS testing examples directly in CLAUDE.md
- `32afbeb` - Resolve open questions for iOS testing
- `178798b` - Clarify VM disk usage vs image size
- `f120eab` - Add concrete iOS testing examples to guide
- And earlier commits establishing the automatic mounting approach

## Critical Open Question: Tart Mount Behavior ✅ RESOLVED

**Question:** What happens when you pass `tart run --dir=name:/NonExistent/Path:ro` where the source path doesn't exist?

**Test result (2026-03-26):**
```bash
$ tart run yoloai-embsdk --no-graphics --dir=testmount:/NonExistent/FakePath:ro
Error Domain=VZErrorDomain Code=2 "A directory sharing device configuration is invalid."
UserInfo={NSLocalizedFailure=Invalid virtual machine configuration.,
NSLocalizedFailureReason=A directory sharing device configuration is invalid.,
NSUnderlyingError=0x87102f180 {Error Domain=NSPOSIXErrorDomain Code=2 "No such file or directory"}}
```

**Answer: Option A - VM fails to start**

Tart/Virtualization.framework validates mount paths at VM start time. If any `--dir` path doesn't exist, the VM fails to start with `NSPOSIXErrorDomain Code=2`.

**Implications for design:**

**Original assumption (WRONG):** Check existence at VM creation, store mount specs in instance.json
- ❌ Problem: User installs Xcode later → Must recreate VM

**Better approach (CORRECT):** Check existence at VM **start time**, always probe for Xcode paths
- ✅ User creates VM without Xcode → VM works fine
- ✅ User installs Xcode later → Next VM start picks it up automatically
- ✅ No VM recreation needed!

**Implementation:**
- Don't store Xcode mount specs in instance.json
- At VM start: Always check for standard Xcode paths, add `--dir` if they exist
- Leverage existing `os.Stat()` check in `buildRunArgs()` (line 450)

**Verification tests:**
```bash
# VM starts normally without mount
$ tart run yoloai-embsdk --no-graphics
✅ Success

# VM fails with non-existent mount path
$ tart run yoloai-embsdk --no-graphics --dir=test:/NonExistent:ro
❌ Error: NSPOSIXErrorDomain Code=2 "No such file or directory"

# VM starts with valid mount path
$ tart run yoloai-embsdk --no-graphics --dir=test:/tmp:ro
✅ Success
```

## Design Critique Issues (Must Fix Before Implementation)

### Priority 1: Critical Issues

#### 1. VM Disk Size Numbers Are Inconsistent

**Current state:**
- Goal #2 says: "~25-30GB vs ~100GB"
- Success Metrics say: "≤30GB"
- Actual design says: "~11GB used in 50GB VM image"

**Problem:** Mixing up VM image size vs actual disk usage

**Fix needed:**
```markdown
Goals:
2. Minimize VM disk USAGE through VirtioFS mounting (~11GB usage vs ~100GB fully local)

Success Metrics:
- VM disk usage: ~11GB (with host tools) vs ~100GB (fully local)
- VM image size: 50GB (standard Tart base, regardless of iOS testing)
```

**Files to update:**
- `docs/design/ios-testing.md` - Goals section (line 14)
- `docs/design/ios-testing.md` - Success Metrics section (line 400)

#### 2. Mount Timing - DESIGN CHANGE REQUIRED ✅

**Original design (WRONG):**
- Architecture says: "On VM creation: Check host for: ... → add mount if exists"
- Stores mount specs in `instance.json` at creation time
- Problem: Installing Xcode later requires VM recreation

**Correct approach (based on Tart test + code review):**
- `--dir` is a `tart run` argument (start time, not creation time)
- Tart validates mount paths at VM start, fails if any don't exist
- **Solution:** Check for Xcode paths at VM **start time**, not creation time

**New architecture:**
```
VM Creation:
- No Xcode-specific mount specs stored in instance.json
- Only user-specified mounts go in config

VM Start (buildRunArgs):
1. Load user mounts from instance.json
2. Additionally probe for Xcode paths (always, regardless of config):
   - /Applications/Xcode.app
   - ~/Library/Developer/CoreSimulator/Caches/dyld
   - ~/Library/Developer/Xcode/UserData
3. For each existing path: add --dir argument
4. Start VM with combined mount list
```

**Benefit:** Installing Xcode after VM creation works automatically on next VM start!

**Files to update:**
- `docs/design/ios-testing.md` - Architecture section (lines 40-49)
- `docs/design/ios-testing.md` - Implementation Details for tart.go
- `docs/design/ios-testing.md` - Remove all "VM recreation required" notes

#### 3. Testing Section Completely Outdated

**Lines 305-314 reference removed concepts:**
- `ios.DetectHostCapabilities()` - doesn't exist in final design
- "Approach selection logic" - removed
- "Create VM with each approach (A, B, C)" - no longer exists

**Fix needed:**
```markdown
### Unit Tests
- Mount configuration in tart.go (all paths added)
- Setup script iOS detection and configuration
- CLAUDE.md generation when Xcode present

### Integration Tests
- Create VM, verify mount specs in instance.json
- Start VM with Xcode on host → iOS testing works
- Start VM without Xcode on host → VM works normally (no iOS)
- Install Xcode on host, restart VM → iOS testing now works (if speculative mounts)

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
```

**Files to update:**
- `docs/design/ios-testing.md` - Testing Strategy section (lines 301-326)

#### 4. Dangerous `rm -rf` in Setup Script

**Current code (line 174):**
```python
subprocess.run(["sudo", "rm", "-rf", runtime_target], capture_output=True)
subprocess.run(["sudo", "ln", "-sfn", runtime_mount, runtime_target],
             capture_output=True)
```

**Problem:** Blindly deletes `/Library/Developer/CoreSimulator/Volumes/` before symlinking

**Scenarios:**
- What if it's already a symlink pointing elsewhere?
- What if there's local content there (user downloaded runtime in VM)?
- What if symlink command fails after deletion?

**Fix needed:**
```python
# Check if runtime is mounted and target exists
runtime_mount = "/Volumes/My Shared Files/m-coresim-runtime"
runtime_target = "/Library/Developer/CoreSimulator/Volumes"

if os.path.isdir(runtime_mount):
    # Remove existing symlink if present
    if os.path.islink(runtime_target):
        subprocess.run(["sudo", "rm", runtime_target], capture_output=True)
    # Create symlink (ln -sfn handles overwriting)
    subprocess.run(["sudo", "ln", "-sfn", runtime_mount, runtime_target],
                 capture_output=True)
```

**Files to update:**
- `docs/design/ios-testing.md` - Setup Script section (lines 169-176)

### Priority 2: Should Fix

#### 5. Shell Profile Assumption

**Current code (line 166):**
```python
with open(os.path.expanduser("~/.zprofile"), "a") as f:
```

**Problem:** Assumes zsh. Users might have bash.

**Fix needed:**
```python
# Write to both zsh and bash profiles
for profile in ["~/.zprofile", "~/.bash_profile"]:
    profile_path = os.path.expanduser(profile)
    with open(profile_path, "a") as f:
        f.write(f'export DEVELOPER_DIR="{xcode_developer}"\n')
        f.write(f'export PATH="{xcode_developer}/usr/bin:$PATH"\n')
```

**Files to update:**
- `docs/design/ios-testing.md` - Setup Script section (line 165-167)

#### 6. Error Handling Missing

**Current code:**
```python
subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer],
             capture_output=True)
```

**Problem:** No error checking. What if it fails?

**Fix needed:**
```python
result = subprocess.run(["sudo", "xcode-select", "--switch", xcode_developer],
                      capture_output=True, text=True)
if result.returncode != 0:
    # Log error somewhere visible (syslog? stderr?)
    syslog.syslog(syslog.LOG_ERR, f"Failed to configure xcode-select: {result.stderr}")
```

**Files to update:**
- `docs/design/ios-testing.md` - Setup Script section (lines 160-176)
- Add note about error logging strategy

#### 7. Document VM Regeneration Behavior ✅ NOT NEEDED

**With new start-time checking approach:**
- VM regeneration is NOT required when installing Xcode
- Xcode paths are checked at every VM start
- This issue is resolved by the design change in #2

**No documentation needed** - the start-time checking design makes this automatic.

### Priority 3: Nice to Have

#### 8. Xcode License Acceptance ✅ VERIFIED NOT NEEDED

**Question:** Investigation doc mentioned `sudo xcodebuild -license accept` but it's not in final design.

**Test performed (2026-03-26):**
```bash
# Start VM with Xcode mounted
$ tart run yoloai-embsdk --no-graphics --dir=m-Xcode:/Applications/Xcode.app:ro

# Test xcodebuild without accepting license
$ tart exec yoloai-embsdk /Volumes/My\ Shared\ Files/m-Xcode/Contents/Developer/usr/bin/xcodebuild -version
Xcode 26.1.1
Build version 17B100

# Test full functionality
$ tart exec yoloai-embsdk /Volumes/My\ Shared\ Files/m-Xcode/Contents/Developer/usr/bin/xcodebuild -showsdks
[Shows all SDKs: iOS, tvOS, watchOS, visionOS, macOS - no license prompt]
```

**Answer:** ✅ License acceptance NOT needed in VM
- Xcode license state is shared from the host
- xcodebuild works fully without `sudo xcodebuild -license accept` in VM
- No additional configuration needed in design or setup script

## Next Steps

1. ✅ ~~**Run critical Tart test**~~ (COMPLETED - Tart fails on missing paths)
2. ✅ ~~**Determine start-time checking approach**~~ (COMPLETED - Better UX!)
3. ✅ ~~**Update design document**~~ (COMPLETED - All Priority 1 & 2 issues fixed):
   - ✅ Issue #2 (MAJOR): Changed from creation-time to start-time checking
   - ✅ Issue #1: Fixed VM disk size numbers (lines 14, 419)
   - ✅ Issue #3: Updated testing section with current design
   - ✅ Issue #4: Fixed dangerous `rm -rf` to check for symlink first
   - ✅ Issue #5: Support both bash and zsh profiles
   - ✅ Issue #6: Added error handling with syslog
   - ✅ Issue #7: Not needed with start-time checking
4. **Begin implementation** following updated design

**Design changes summary:**
- Architecture diagram updated to show start-time checking
- `buildRunArgs()` implementation checks Xcode paths at every VM start
- Setup script fixes: safe symlink handling, bash+zsh support, error logging
- Testing section updated to match final design
- Success metrics clarified: ~11GB usage vs 50GB image size

**Key insight from testing:**
- Tart validates paths at start time and fails if missing
- This enables **start-time checking** instead of creation-time
- **Huge UX win:** Installing Xcode later works automatically on next VM start!

## Implementation Phases (After Design Fixes)

### Phase 1: Tart Runtime
- Update `runtime/tart/tart.go` - `addSystemMounts()`
- Based on test: either always add mounts, or check existence

### Phase 2: Setup Script
- Update `runtime/monitor/sandbox-setup.py` - `TartBackend.setup()`
- Add iOS configuration logic
- Fix rm -rf issue
- Add error handling
- Support bash and zsh profiles

### Phase 3: Agent Context (Optional)
- Add CLAUDE.md generation with iOS examples
- Only if Xcode mount detected

### Phase 4: Documentation
- Update `docs/GUIDE.md` with iOS testing section
- Update `docs/ROADMAP.md` (mark as implemented)
- Update `README.md` (add to features)
- Update `docs/dev/ARCHITECTURE.md` (document components)

### Phase 5: Testing
- Write unit tests
- Run integration tests
- Complete manual testing checklist
- Validate on real iOS project (Embrace SDK)

## Files Modified So Far

Investigation and design only - no code changes yet:
- `docs/dev/ios-testing-investigation.md` (new)
- `docs/design/ios-testing.md` (new)
- `runtime/tart/tart.go` (experimental code from investigation, needs cleanup)

## Key Decisions Made

1. **No --ios-testing flag** - Automatic for all Tart VMs
2. **Silent operation** - No prompts, warnings, or validation
3. **User provides tools** - yoloAI provides plumbing only
4. **Mount entire Volumes/** - All simulator platforms automatically
5. **No version management** - Let xcodebuild/simctl choose runtime
6. **Agent context optional** - Include examples in CLAUDE.md

## Resources

- Investigation: `docs/dev/ios-testing-investigation.md`
- Design: `docs/design/ios-testing.md`
- Tart docs: https://github.com/cirruslabs/tart
- VirtioFS: https://virtio-fs.gitlab.io/

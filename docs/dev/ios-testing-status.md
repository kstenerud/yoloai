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

## Critical Open Question: Tart Mount Behavior

**Question:** What happens when you pass `tart run --dir=/NonExistent/Path:name:ro` where the source path doesn't exist?

**Why this matters:**
- If Tart silently skips missing mounts → We can speculatively add all mount specs at VM creation
- User installs Xcode later → Automatically works on next VM start (no VM recreation needed)
- If Tart fails to start → We must check path existence at VM creation time
- User installs Xcode later → Must recreate VM to get iOS testing

**Test to run:**
```bash
# Use an existing VM
tart run yoloai-embsdk --no-graphics --dir=/NonExistent/FakePath:testmount:ro &

# Expected outcomes:
# A) VM fails to start → Must check existence at creation time
# B) VM starts, mount silently skipped → Can speculatively add all mounts ✅
# C) VM starts, empty mount created → Need to investigate further
```

**After testing:**
- If B: Update design to remove existence checks, always add mount specs
- If A or C: Keep current design with existence checks

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

#### 2. Mount Timing Ambiguity

**Current state:**
- Architecture says: "On VM creation: Check host for: ... → add mount if exists"
- Resolved Questions say: "Mounts established at VM start time"

**Problem:** Unclear when mounts are CONFIGURED vs when they're MOUNTED

**Reality (from code review):**
- VM creation: yoloAI stores mount specs in `instance.json`
- VM start: yoloAI reads `instance.json`, passes `--dir` args to `tart run`
- Tart mounts the directories at start time

**Current issue:** If mount specs are created based on what exists at VM creation time, installing Xcode later won't help (mount spec was never added).

**Resolution depends on Tart test above:**
- If Tart skips missing mounts: Add all mount specs speculatively (solves this issue)
- If Tart fails on missing mounts: Document that VM must be recreated after installing Xcode

**Files to update:**
- `docs/design/ios-testing.md` - Architecture section (lines 40-49)
- `docs/design/ios-testing.md` - Resolved Questions section (lines 364-368)

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

#### 7. Document VM Regeneration Behavior

**Missing from design:**
What happens when user does `yoloai delete embsdk && yoloai new embsdk`?

**Answer (needs documentation):**
- Mount specs are created fresh at VM creation
- If host gained Xcode in the meantime, new VM gets it
- This is automatic, no special handling needed

**Add to design:**
```markdown
### VM Regeneration

When a VM is deleted and recreated:
- Mount specs are evaluated fresh based on current host state
- If host gained/lost Xcode since last creation, new VM reflects this
- No special handling needed - automatic
```

**Files to update:**
- `docs/design/ios-testing.md` - Add new subsection in Architecture

### Priority 3: Nice to Have

#### 8. Xcode License Acceptance

**Question:** Investigation doc mentioned `sudo xcodebuild -license accept` but it's not in final design.

**Investigation needed:**
- If Xcode is mounted from host (where license already accepted), does VM need to accept again?
- Test: Run `xcodebuild` in VM without accepting license, see if it prompts

**Likely answer:** Not needed (shared Xcode = shared license state)

**Action:** Test and document result in design

## Next Steps

1. **Run critical Tart test** (see above)
2. **Based on test result:**
   - Option B (skips missing mounts): Update design to remove existence checks
   - Option A (fails on missing mounts): Keep existence checks, document recreation needed
3. **Fix all Priority 1 critique issues** in design document
4. **Fix Priority 2 issues** if time permits
5. **Begin implementation** following updated design

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

# iOS Runtime Download Approach Verification Report

**Date:** 2026-03-27
**Test VM:** yoloai-test-download
**Runtime:** iOS 26.4 (26.4 - 23E244)

---

## Summary

The `xcodebuild -downloadPlatform iOS` approach **successfully resolves** the simulator boot failures encountered with the ditto copy approach.

---

## Verification Steps

| Step | Result | Notes |
|------|--------|-------|
| Configure Xcode in VM | ✅ Success | Symlinked, selected, licensed, first-launch |
| Download iOS 26.4 runtime | ✅ Success | 8.46 GB download completed |
| Runtime recognized by simctl | ✅ Success | `iOS 26.4 (26.4 - 23E244) - com.apple.CoreSimulator.SimRuntime.iOS-26-4` |
| Runtime installed location | ✅ Verified | `/Library/Developer/CoreSimulator/Volumes/iOS_23E244/Library/.../iOS 26.4.simruntime` |
| Runtime size | ✅ Complete | 16 GB (full installation) |
| Simulator device created | ✅ Success | Used runtime identifier `com.apple.CoreSimulator.SimRuntime.iOS-26-4` |
| Simulator boots | ✅ Success | **No launchd_sim error!** |
| Simulator responsive | ✅ Verified | Can query app info, device shows as Booted |
| Simulator shuts down cleanly | ✅ Success | No errors |

---

## Comparison: Ditto vs Download

### Ditto Approach (Failed)
- **Method:** Copy runtime from VirtioFS mount using `ditto`
- **Problems:**
  - Missing Info.plist (required manual fix)
  - Missing or incomplete dyld cache
  - Simulator boot failure: `Failed to start launchd_sim: could not bind to session`
  - Runtime incomplete despite 15GB/16GB copied

### Download Approach (Success)
- **Method:** Run `xcodebuild -downloadPlatform iOS` inside VM
- **Advantages:**
  - Complete runtime installation (all components)
  - Installs directly to proper location in `/Volumes/iOS_23E244/...`
  - Simulator boots successfully
  - No launchd_sim errors
  - Officially supported Apple workflow

---

## Critical Finding

The downloaded runtime is installed at the **exact same path** that ditto was trying to copy FROM:
`/Library/Developer/CoreSimulator/Volumes/iOS_23E244/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.4.simruntime`

This proves the issue wasn't the installation location, but rather:
1. **Incomplete copy:** Ditto couldn't copy protected files (modelmanagerd, possibly others)
2. **Missing components:** The copied runtime lacked critical components needed for boot
3. **Proper installation:** `xcodebuild -downloadPlatform` performs a complete, validated installation

---

## Recommendation

**Proceed with implementing the download approach** in `runtime/tart/runtime_copy.go`:

1. Replace ditto-based copying with `xcodebuild -downloadPlatform <platform>`
2. Runtime will be downloaded and installed directly into the VM
3. Verification step confirms runtime is recognized by simctl
4. No need for Info.plist workarounds or permission handling

**Implementation notes:**
- Download occurs inside the VM (not on host)
- Requires Xcode to be mounted and configured first
- Download size: ~8.5 GB (expands to 16 GB installed)
- Single command: `xcodebuild -downloadPlatform iOS` (downloads latest)
- For specific version: Would need to investigate xcodebuild download options

---

## Next Steps

1. Implement download approach in `runtime/tart/runtime_copy.go`
2. Update `CopyRuntimeToVM` function to use `xcodebuild -downloadPlatform`
3. Remove ditto-based code and permission workarounds
4. Test with actual iOS project (e.g., embrace-apple-sdk)
5. Update documentation to reflect new approach

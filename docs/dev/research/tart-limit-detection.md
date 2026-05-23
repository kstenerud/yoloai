# Tart Concurrent macOS VM Limit Detection

Research question: how can yoloAI detect Apple's concurrent-macOS-VM limit from
the `tart` binary itself, rather than hard-coding the number 2?

---

## 1. Recommendation

Match the log output of `tart run` against the stable substring
`"The number of VMs exceeds the system limit"`. Tart already detects the
`VZError.Code.virtualMachineLimitExceeded` error from Apple's Virtualization
framework (error code 6), wraps it into its own `RuntimeError.VirtualMachineLimitExceeded`,
and writes the message to stderr before exiting with code 1. The message has
appeared verbatim in multiple independent user reports and GitHub issues going back
to at least 2023, and the source code in `Run.swift` shows it is constructed from a
fixed string plus an optional `" (other running VMs: ...)"` suffix. This means the
fixed prefix is stable even if Apple changes what VMs are named. No hard-coded
number "2" is needed: yoloAI simply checks whether tart itself said the limit was
hit, whatever that limit happens to be.

---

## 2. How Tart Surfaces the Limit

### Error message (confirmed by source code and multiple user reports)

When `tart run` hits the concurrent-VM ceiling, stderr contains:

```
The number of VMs exceeds the system limit (other running VMs: <name1>, <name2>)
```

The parenthetical suffix is omitted if Tart cannot determine which VMs are running,
so the reliable detection anchor is the **prefix**:

```
The number of VMs exceeds the system limit
```

### Exit code

`tart run` exits with **code 1** for all fatal errors. There is no distinct exit
code for the VM-limit case; the only distinguishing signal is the stderr text.

### Source code path (cirruslabs/tart, `main` branch as of May 2026)

File: `Sources/tart/Commands/Run.swift`, lines approx. 780-800

```swift
} catch let error as VZError {
  if error.code == .virtualMachineLimitExceeded {
    var hint = ""
    do {
      let runningVMs: [String] = try localStorage.list().compactMap { (name, vmDir) in
        if try !vmDir.running() { return nil }
        return name
      }
      if !runningVMs.isEmpty {
        let runningVMsJoined = runningVMs.joined(separator: ", ")
        hint = " (other running VMs: \(runningVMsJoined))"
      }
    } catch {
      // we can't provide any hint
    }
    throw RuntimeError.VirtualMachineLimitExceeded(hint)
  }
  throw error
}
```

The outer catch at approx. lines 880-887 then writes the error to stderr and exits:

```swift
} catch {
  OpenTelemetry.instance.contextProvider.activeSpan?.recordException(error)
  fputs("\(error)\n", stderr)
  OTel.shared.flush()
  Foundation.exit(1)
}
```

The `RuntimeError.VirtualMachineLimitExceeded` type's `description` or
`errorDescription` string is defined in a separate file not yet located (it is
not in `Run.swift`, `VM.swift`, `VMDirectory.swift`, `VMConfig.swift`, `Utils.swift`,
`Serial.swift`, `Fetcher.swift`, `Config.swift`, or `VMStorageLocal.swift`). The
exact pre-message text of that type is therefore unconfirmed. However, it is
confirmed from user reports and the GitLab Tart executor issue tracker that the
final stderr line the user sees is:

```
The number of VMs exceeds the system limit (other running VMs: gitlab-8437130944, gitlab-8438646366)
```

So the stable detection prefix is embedded in the `RuntimeError` message itself,
not as a prefix added by the outer catch.

### Probe / capacity query

Tart has no `tart capacity`, `tart limits`, `tart status`, or dry-run mode.
`tart list` shows VM state (running/stopped) but not a running-count vs.
system-limit pair. There is no pre-flight check that does not attempt to actually
start a VM.

---

## 3. Apple Virtualization.framework

### Error code

`VZError.Code.virtualMachineLimitExceeded` is documented at:
https://developer.apple.com/documentation/virtualization/vzerror/code/virtualmachinelimitexceeded

The numeric value is **6** (from user-observed `VZErrorDomain Code=6` in crash
reports and issue comments). Apple's description: "Unable to create an additional
VM."

The framework returns the message: `"The maximum supported number of active virtual
machines has been reached."` (observed in issue reports; this is Apple's string,
not Tart's).

### The limit value

The limit is **2 concurrent macOS guest VMs per host**. This number is grounded in
macOS licensing terms, not hardware capability. The macOS EULA since Lion (2011),
section 2B(iii), permits "up to two (2) additional copies or instances of the Apple
Software within virtual operating system environments" on a single Apple-branded
computer.

The limit is enforced in the closed-source portion of XNU using a kernel variable
`hv_apple_isa_vm_quota`. It is not a tunable sysctl. Apple has not publicly changed
the limit across macOS 12 (Monterey), 13 (Ventura), 14 (Sonoma), or 15 (Sequoia);
all reports confirm the value remains 2.

### macOS version history

No evidence of any change to this limit across macOS 12–15. The Eclectic Light
Company documented it in August 2022 (macOS 12), and issue reports through early
2026 still cite the same limit. macOS 26 (Tahoe, announced WWDC 2026) has not yet
been released; tart issue #1146 references Tahoe images but not a limit change.

### Linux guests

The limit appears to apply only to macOS guests. Linux VMs via Virtualization.framework
do not consume the macOS-guest quota; the limit is OS-type-specific. This distinction
is relevant: yoloAI's Tart backend specifically uses macOS guest images.

---

## 4. Tart Project Signals

### Issues and discussions

- **Discussion #1054** ("So tart can do only 2 virtual macos?") — Users confirm
  the 2-VM cap and the error message. Tart maintainers confirm it is an Apple
  constraint Tart cannot override. No plans to add a structured error code.
  URL: https://github.com/cirruslabs/tart/discussions/1054

- **Issue #967** ("VMs are not actually stopping/stopped", Dec 2024) — User hits
  `"The number of VMs exceeds the system limit"` because `tart list` showed a VM
  as stopped while it was still running. This documents a correctness hazard: stale
  VM processes can consume the quota invisibly.
  URL: https://github.com/cirruslabs/tart/issues/967

- **gitlab-tart-executor issue #93** ("executor logs and concurrency issues",
  Nov 2024) — Executor running 3 concurrent jobs sees the third stuck with
  `"The number of VMs exceeds the system limit (other running VMs: gitlab-...,
  gitlab-...)"`. This provides confirmed exact verbatim stderr text from a real
  system.
  URL: https://github.com/cirruslabs/gitlab-tart-executor/issues/93

### Structured error output

Tart has no JSON output mode for `tart run`. The only machine-readable signal is
the exit code (always 1 for fatal errors) plus the stderr text. There is no
documented plan to add structured error reporting for this case.

### Relevant commits

No specific commit introducing the `VirtualMachineLimitExceeded` handling was
identified by commit hash; the code structure in `Run.swift` appears stable across
the observable history. The feature was present at least by mid-2023 based on
issue dates.

---

## 5. Recommended Implementation for yoloAI's Tart Backend

### Where to intercept

The natural place is in `Start()` in `runtime/tart/tart.go`. Currently `Start()`
redirects `tart run` stdout+stderr to `vm.log`, then calls `waitForBoot()`. When
`waitForBoot()` returns an error because the process exited early, the caller
(lines 274-283) reads `vm.log` and appends it to the returned error's detail
string.

The simplest approach: after `waitForBoot()` returns a non-nil error, read
`vm.log` and check whether it contains the limit-exceeded substring. If yes, return
a typed `ResourceLimitError` (new type in `yoerrors`) instead of a generic error.

```go
const tartVMLimitSubstr = "The number of VMs exceeds the system limit"

if logData, readErr := os.ReadFile(logPath); readErr == nil {
    if strings.Contains(string(logData), tartVMLimitSubstr) {
        return yoerrors.NewResourceLimitError(
            "macOS concurrent VM limit reached: %s",
            strings.TrimSpace(string(logData)),
        )
    }
}
```

Alternatively this detection could live in `waitForBoot()` itself when
`procDone` fires (the process-exited-early path), since the log file is flushed
before the goroutine closes `logFile`. But the log-read-after-boot-failure path
in `Start()` is cleaner because it already has the log-read logic.

### New error type

`ResourceLimitError` belongs in `internal/yoerrors/errors.go`. It should get its
own exit code (currently codes 2-8 are taken). This is a host-resource exhaustion
condition distinct from `PlatformError` (wrong OS) or `DependencyError` (missing
binary).

### Match stability

The substring `"The number of VMs exceeds the system limit"` is:
- Produced by Tart's own `RuntimeError.VirtualMachineLimitExceeded` Swift type.
- Present in multiple independent user reports from 2023–2025 with identical
  phrasing.
- Not dependent on the numeric limit value (it says "the system limit", not "2").

This means it will remain correct if Apple raises the limit to 3 or more, or if
Tart adds it to a future structured-error format.

The risk of breakage: if Tart renames the error type and changes its description
string. This has not happened in at least three years. A regression test that
parses a known-bad `vm.log` fixture can catch this.

### Pre-flight VM count check (not recommended)

An alternative is to run `tart list --format json` before starting, count running
macOS VMs, and reject if count >= 2. This approach has three problems:
1. It hard-codes the number 2 — exactly what this research is trying to avoid.
2. It races: another process can start a VM between the check and `tart run`.
3. It misses the stale-VM case documented in issue #967 where `tart list` shows a
   VM as stopped but the process is still running.

The post-failure log-scan approach avoids all three problems.

---

## 6. What Needs Testing on a Real Mac Before Committing

1. **Confirm the exact stderr prefix.** Run `tart run` with two macOS VMs already
   started (manually or via script), capture full stderr, and verify the string
   starts with `"The number of VMs exceeds the system limit"`. The user reports are
   consistent but secondhand.

2. **Confirm exit code is 1.** Verify `echo $?` after the limit-exceeded failure.
   The source code shows `Foundation.exit(1)` for all fatal errors, but confirm
   there is no special-casing for VZError codes in the exit path.

3. **Confirm stderr is flushed before process exit.** Since yoloAI captures stderr
   to `vm.log` via the `cmd.Stderr = logFile` assignment, verify the log contains
   the error line before the goroutine calls `cmd.Wait()`. Swift's `fputs` to
   stderr is unbuffered, so this should be fine, but it warrants a check.

4. **Confirm the log-read race is safe.** In `Start()`, `logFile.Close()` is called
   inside the goroutine `go func() { procDone <- cmd.Wait(); logFile.Close() }()`.
   The log-read in `Start()` happens after `waitForBoot()` returns. Verify
   `logFile.Close()` has completed (and thus the write has flushed) before the
   read, or add an explicit `logFile.Sync()` + `Close()` before reading. In
   practice Go's `cmd.Wait()` does not return until the process has exited and all
   stdio has been flushed, so this should be safe.

5. **Stale VM edge case.** Reproduce issue #967: start a VM, kill its process
   directly, confirm `tart list` shows it stopped, then start two more VMs and
   confirm whether the quota is still consumed. This determines whether yoloAI needs
   to warn on stale PIDs before starting.

---

## 7. Source Links

- Tart `Run.swift` (VZError catch block):
  https://github.com/cirruslabs/tart/blob/main/Sources/tart/Commands/Run.swift

- Apple documentation for `VZError.Code.virtualMachineLimitExceeded`:
  https://developer.apple.com/documentation/virtualization/vzerror/code/virtualmachinelimitexceeded

- Tart Discussion #1054 (2-VM limit confirmation):
  https://github.com/cirruslabs/tart/discussions/1054

- Tart Issue #967 (stale VM / list-vs-running mismatch):
  https://github.com/cirruslabs/tart/issues/967

- gitlab-tart-executor Issue #93 (verbatim stderr text from production):
  https://github.com/cirruslabs/gitlab-tart-executor/issues/93

- Eclectic Light Company: Apple VM limit mechanism (kernel quota variable):
  https://eclecticlight.co/2022/08/04/virtualisation-on-apple-silicon-macs-8-how-apple-limits-vms/

- Blog post on Apple Silicon VM limit internals (hv_apple_isa_vm_quota):
  https://khronokernel.com/macos/2023/08/08/AS-VM.html

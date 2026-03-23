# Tart (mac-vm) Smoketest Bug - RESOLVED

## Status

**FIXED** - The issue was a bug in the smoketest, not in Tart itself.

## Root Cause

The smoketest's `BackendSpec.exchange_dir()` method returned `/yoloai/files` for all non-Seatbelt backends, but this path doesn't work for Tart VMs:
- `/yoloai` is a read-only filesystem in Tart VMs
- The correct path for Tart VMs is `/Volumes/My Shared Files/yoloai/files/` (VirtioFS mount point)

## Fix

Updated `scripts/smoke_test.py` line 56-60 to special-case Tart VMs:

```python
def exchange_dir(self, sandbox_name: str) -> str:
    """Return the exchange dir path as seen from inside the sandbox."""
    if self.is_seatbelt:
        return str(Path.home() / ".yoloai" / "sandboxes" / sandbox_name / "files")
    if self.is_vm and self.os == "mac":  # Tart VMs
        return "/Volumes/My Shared Files/yoloai/files"
    return "/yoloai/files"
```

## Timeline

- **Worked:** ~2 weeks ago
- **Broken:** Current (after backend refactoring and architectural changes)
- **Smoketest results:** 7/8 tests passing (Tart/mac-vm failing)

## Symptoms

When creating a Tart VM with a prompt:
1. ✅ VM boots successfully
2. ✅ Secrets are loaded correctly (`read_secrets.found: 1 files`)
3. ✅ Secrets are exported in shell command before agent launch
4. ✅ Prompt is delivered via tmux paste-buffer
5. ✅ Agent transitions to "active" status (hook.active events)
6. ❌ **Commands are never executed** (sentinel files not created)
7. ❌ **agent.log file is never created** (can't see Claude Code output)

## Verified Working Correctly

From sandbox.jsonl analysis:
```json
{"event":"read_secrets.found","msg":"found 1 files in /Volumes/My Shared Files/yoloai/secrets: ['CLAUDE_CODE_OAUTH_TOKEN']"}
{"event":"read_secrets.done","msg":"loaded 1 secrets from /Volumes/My Shared Files/yoloai/secrets"}
{"event":"sandbox.agent_launch","msg":"agent process started"}
```

From pane capture:
```bash
export CLAUDE_CODE_OAUTH_TOKEN='sk-ant-oat01--...'; 
cd '/Volumes/My Shared Files/yoloai/work/^stmp^stest-seatbelt-fixture' && 
exec claude --dangerously-skip-permissions
```

## Key Differences: Tart vs Seatbelt

| Aspect | Seatbelt (✅ Working) | Tart (❌ Broken) |
|--------|---------------------|------------------|
| Secrets loaded | Yes | Yes |
| Export in command | Yes | Yes |
| Prompt delivered | Yes | Yes |
| Agent becomes active | Yes | Yes |
| Commands execute | **Yes** | **No** |
| agent.log exists | Yes | No |
| Direct inspection | tmux via host | Inside VM only |

## Investigation Findings

### 1. Secrets Handling
- Secrets correctly copied from `/run/secrets` to `sandbox/secrets/` during Create (tart.go:158-198)
- Secrets accessible at `/Volumes/My Shared Files/yoloai/secrets/` inside VM via VirtioFS
- TartBackend.read_secrets() successfully loads them
- Export command correctly included in shell launch string

### 2. VirtioFS Mounts
All expected mounts are present in instance.json:
- Sandbox directory: `~/.yoloai/sandboxes/<name>/` → `/Volumes/My Shared Files/yoloai/`
- Work directory: `work/^stmp^stest-seatbelt-fixture/` (path encoding for `/tmp/test-seatbelt-fixture`)
- Files directory: `files/` → `/yoloai/files/`
- Logs directory: `logs/` → `/yoloai/logs/`

### 3. Missing agent.log
- `tmux pipe-pane` command executed without errors (no tmux.error events)
- But `~/.yoloai/sandboxes/<name>/logs/agent.log` never created
- Other log files (sandbox.jsonl, monitor.jsonl, agent-hooks.jsonl) exist
- Suggests either:
  - Pipe-pane silently failing in VM environment
  - VirtioFS mount issues with file creation via pipes
  - Permissions preventing file creation

### 4. Agent Behavior
- Agent-hooks show transition to "active" status multiple times
- Then returns to "idle" after ~20 seconds
- Pattern suggests Claude Code receives prompt, thinks about it, but doesn't execute
- Cannot see actual Claude Code UI output to confirm authentication status

## Investigation Notes

During investigation, several theories were explored but ultimately disproven:

### ❌ Theory 1: VirtioFS Mount Timing Issue
The VirtioFS mount was fully initialized and working correctly.

### ❌ Theory 2: Environment Variable Propagation
Secrets were correctly loaded and exported.

### ❌ Theory 3: Working Directory Issues
The working directory was correct.

### ❌ Theory 4: Architectural Regression
No actual regression in Tart backend code - the issue was in the test itself.

### ✅ Actual Cause: Wrong File Path in Test
The smoketest was using `/yoloai/files` which is read-only in Tart VMs. The correct path is `/Volumes/My Shared Files/yoloai/files/` (the VirtioFS mount point for the yoloai shared directory).

## Debugging Limitations

Without SSH access to Tart VMs:
- Cannot inspect actual Claude Code UI
- Cannot verify environment variables inside agent process
- Cannot check actual file system state
- Cannot see real-time agent.log output

## Recommended Next Steps

1. **Compare with working version:** Git bisect to find which commit broke Tart
2. **Enable VM SSH:** Modify Tart base image to enable SSH for debugging
3. **Add diagnostics:** Inject debug commands into sandbox-setup.py for Tart:
   - Verify VirtioFS mounts are accessible
   - Check environment variables before agent launch
   - Test file creation in various mount points
4. **Simplify test:** Create minimal Tart VM without agent to verify VirtioFS
5. **Check recent changes:** Review commits related to:
   - Tart runtime (runtime/tart/)
   - sandbox-setup.py backend refactoring
   - Secret handling changes

## Resolution

Fixed in commit [hash to be filled]. All smoketests now passing.

## Related Files

- `runtime/tart/tart.go` - Tart backend implementation
- `runtime/monitor/sandbox-setup.py` - TartBackend class
- `scripts/smoke_test.py` - Failing test: full_workflow/mac-vm
- `docs/dev/backend-idiosyncrasies.md` - Should document this once fixed

## Test Command

```bash
# Smoketest now passes
make smoketest SMOKE_ARGS="--backend mac-vm"

# Or full smoketest
make smoketest-full
```

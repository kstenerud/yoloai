# VM Isolation (`--isolation vm`) End-to-End Debug Plan

**Goal:** `yoloai new x . -a --replace --isolation vm` reliably creates a Kata Containers VM,
runs the agent inside it, and attaches the host terminal to the tmux session.

**Environment:** Ubuntu 24.04, containerd v2.2.2, Kata 3.28, Dragonball VMM, overlayfs snapshotter.

---

## Complete Flow

Each step is numbered; known-working steps are marked ✓, broken/unverified are marked ✗ or ?.

```
[host: yoloai new]
  │
  ├─ 1. EnsureImage        build Docker image; link/import into containerd ns "yoloai"
  ├─ 2. Destroy (--replace) Stop → Remove old container + CNI teardown
  ├─ 3. Workdir setup       copy host workdir into sandboxDir/work/
  ├─ 4. CNI setup           create netns; run CNI ADD (bridge plugin, IPAM)
  ├─ 5. Container create    NewContainer: OCI spec + overlayfs snapshot
  ├─ 6. Task start          NewTask → task.Start → Dragonball VM boots
  │
  │    [inside VM: container init process = /yoloai/bin/entrypoint.sh]
  │      ├─ 7. entrypoint.sh     writes sandbox.jsonl entry; execs entrypoint.py as root
  │      ├─ 8. entrypoint.py     uid remap, write secrets to /run/secrets/
  │      └─ 9. sandbox-setup.py  reads secrets into env; starts tmux; launches agent
  │
  ├─ 10. Readiness poll     host reads sandbox.jsonl for "sandbox.tmux_start" event
  │                         OR falls back to rt.Exec("tmux has-session")
  └─ 11. Attach             InteractiveExec(["script","-q","-e","-c","exec tmux attach ...","/dev/null"])
```

---

## Step Status

### ✓ Step 1 — EnsureImage

- Fixed: `system build` calls `EnsureImage(force=true)`.
- Fixed: After `ctr images rm yoloai-base`, `system build` performs fresh import via
  `docker save | ctr import` slow path (when `linkFromDockerNamespace` fails) or fast-path
  link from Docker's moby namespace.
- **Verified:** containerd image digest matches Docker image digest after build.

### ✓ Step 2 — Destroy (--replace)

- `Remove()` → `Stop()` (SIGTERM → SIGKILL) → `retryDelete(WithSnapshotCleanup)` → `teardownCNI`.
- `retryDelete` retries 5× with 2s delay to handle Kata shim teardown lag.
- **Verified:** subsequent `Create()` finds no stale container or snapshot.

### ✓ Step 3 — Workdir setup

- Host dir copied into `~/.yoloai/sandboxes/x/work/` with git baseline.
- Mounted into container at same host path (mirrored mount).
- **Verified:** pane shows `yoloai@...:/home/karl/x$` — correct workdir.

### ✓ Step 4 — CNI setup

- `setupCNI`: create named netns `yoloai-yoloai-x` at `/var/run/netns/`.
- Run CNI ADD with bridge plugin → assigns 10.88.x.x address.
- Persist state to `sandboxDir/containerd/cni-state.json`.
- **Verified:** container gets network.

### ✓ Step 5 — Container create

- `kataConfigPath("io.containerd.kata.v2")` returns `""` → shim uses built-in Dragonball config.
  (Fixed: previously returned QEMU config path → VM crashed silently.)
- `img.Unpack(ctx, "overlayfs")` unpacks new image layers.
- Stale container/snapshot pre-cleared before `NewContainer`.
- **Verified:** `Create()` returns nil; journal shows Dragonball config loaded.

### ✓ Step 6 — Task start

- `NewTask(ctx, cio.NullIO)` + `task.Start()` boots Dragonball VM.
- Polls until `Running` status (up to 60s).
- **Verified:** task reaches Running; sandbox.jsonl entries appear.

### ✓ Step 7 — entrypoint.sh

- Writes `entrypoint.start` to sandbox.jsonl.
- Execs `entrypoint.py` as root.
- **Verified:** log entry present.

### ✓ Step 8 — entrypoint.py (root)

- Remaps uid/gid of mounted dirs to yoloai (1001).
- Writes secrets to `/run/secrets/` (0600 owned by root).
- Execs `gosu yoloai python3 sandbox-setup.py docker`.
- **Verified:** `uid.remap` and `secrets.write` events in sandbox.jsonl.

### ✓ Step 9 — sandbox-setup.py (uid 1001)

- Reads secrets from `/run/secrets/` into os.environ (try/except OSError for unreadable files).
  (Fixed: old image lacked try/except → PermissionError crash.)
- Starts tmux session `main`.
- Launches agent (`exec claude --dangerously-skip-permissions`).
- **Verified:** `sandbox.tmux_start` and `sandbox.post_launch` events in sandbox.jsonl.

### ✓ Step 10 — Readiness poll

- `waitForTmux` reads sandbox.jsonl for `sandbox.tmux_start` event (fast path, no exec needed).
- Falls back to `rt.Exec("tmux has-session")` only if jsonl not present.
- **Verified:** sandbox reaches "active"/"idle" status.

### ? Step 11 — Attach (in progress)

**Fixed so far:**
- `exec.go`: `containerEnv()` injects the container's full OCI spec env into exec processSpec
  so bare command names like `script` and `tmux` resolve via PATH.
- `commands.go`: attach command uses full path `/usr/bin/script`.

**Remaining failure:** `no sessions` — tmux attach finds no sessions in the server.

**Root cause:** `tmux_socket` is empty for the containerd backend (only set for Docker/Podman
in `create.go`). Without a fixed socket, tmux uses the uid-based default at
`/tmp/tmux-<uid>/default`. The exec'd process inside the Kata VM may resolve a different
socket than the init process, causing `tmux attach` to find a server with no sessions.

Docker/Podman use a fixed socket `/tmp/yoloai-tmux.sock` specifically to avoid this
problem (originally for gVisor ARM64; the same issue appears in Kata/containerd exec).

**Fix 11c:** Add `containerd` to the tmux socket condition in `sandbox/create.go`:
```go
if backend == "docker" || backend == "podman" || backend == "containerd" {
    tmuxSocket = "/tmp/yoloai-tmux.sock"
}
```
`readTmuxSocket` on the host then returns `/tmp/yoloai-tmux.sock`, and the attach
command becomes `exec tmux -S /tmp/yoloai-tmux.sock attach -t main`.

**Fixed:** After Fix 11c, tmux now connects to the server but fails with:
`open terminal failed: terminal does not support clear`

**Root cause:** The container OCI spec has no `TERM` env var (it's a runtime property, not
an image property). The exec process therefore has no TERM, so tmux/ncurses can't
initialize the terminal.

Docker's `InteractiveExec` shells out to `docker exec -it` which inherits the host shell's
environment (including TERM) automatically. Containerd exec requires explicit env.

**Fix 11d:** In `InteractiveExec` in `exec.go`, append `TERM=<host TERM>` to the env
(fallback: `xterm-256color`):
```go
term := os.Getenv("TERM")
if term == "" {
    term = "xterm-256color"
}
env = append(env, "TERM="+term)
```

### ✗ Steps 12-13 — Agent usability (in progress)

After attach succeeds, two additional issues:

**12. Terminal size:** Claude renders at ~52 cols instead of the actual 165. Root cause: the
kata-agent creates the PTY before the kata shim can apply the `ConsoleSize` or `Resize` RPC.
Fix applied: in `attachToSandbox` for the containerd case, run `/bin/sh -c "stty cols X
rows Y 2>/dev/null; exec tmux -S <sock> attach -t main"` — the `stty` command sets the
terminal dimensions on the PTY from inside the container before tmux queries them, bypassing
the kata ConsoleSize/Resize timing issue. Previously also removed the `script` wrapper (which
caused nested-PTY resize issues). **Pending verification.**

**13. Network timeouts:** `claude` reports "Request timed out" on every API call.
Two sub-issues discovered and fixed:

**13a. resolv.conf stub:** The container's `/etc/resolv.conf` pointed to systemd-resolved
stub (`127.0.0.53`) which is unreachable inside the VM. Fix: bind-mount
`/run/systemd/resolve/resolv.conf` (real upstream nameservers) in `lifecycle.go Create()`.

**13b. Route conflict with cni-podman0 (root cause of TCP timeouts):** Podman and yoloai
both use `10.88.0.0/16`. When both `cni-podman0` and `yoloai0` have routes for this subnet,
the kernel routes replies for 10.88.x.x addresses via `cni-podman0` (even when it's
`linkdown`), not via `yoloai0`. Outbound traffic from the VM reaches the internet (using
yoloai0 bridge + MASQUERADE), but replies never reach the VM because they're forwarded
to the wrong (down) bridge. Fix: change yoloai's subnet to `10.89.0.0/16` in
`cniConflistTemplate` (`runtime/containerd/cni.go`). Also update `ensureCNIConflist` to
overwrite the on-disk conflist when it doesn't match the template, so subnet changes
take effect automatically.

---

## Fix Plan

### Fix 11a — Supply PATH in exec processSpec

**File:** `runtime/containerd/exec.go`

Both `Exec` and `InteractiveExec` build a `specs.Process` with no `Env`. The Kata agent
(inside the VM) needs at least a standard PATH to resolve bare binary names.

The correct PATH to inject is the one from the container's OCI spec, which was set during
`Create` via `oci.WithImageConfig(img)`. The cleanest way to retrieve it:

```go
// getContainerEnv reads the Env from the container's stored OCI spec.
// Returns a minimal fallback PATH if the spec cannot be loaded.
func getContainerEnv(ctx context.Context, ctr client.Container) []string {
    spec, err := ctr.Spec(ctx)
    if err != nil || spec.Process == nil {
        return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
    }
    return spec.Process.Env
}
```

Then in both `Exec` and `InteractiveExec`, after loading the container:
```go
processSpec := &specs.Process{
    Args:  cmd,
    Cwd:   "/",
    Env:   getContainerEnv(ctx, ctr),
    // ...
}
```

**Verification:** `rt.Exec(ctx, name, []string{"which", "script"}, "")` returns
`/usr/bin/script` without error.

### Fix 11b — Use full path for attach command (belt-and-suspenders)

**File:** `internal/cli/commands.go`, `attachToSandbox`

Even with Fix 11a, using full paths makes the exec spec independent of PATH:

```go
cmd = []string{"/usr/bin/script", "-q", "-e", "-c", tmuxArgs, "/dev/null"}
```

This is the right long-term approach because `Exec` and `InteractiveExec` will be called
from other places in the future (e.g. `yoloai exec`) and relying on PATH in the
processSpec is fragile.

### Fix 11c — Verify PTY bridging works in Kata

After fixing the command resolution, verify the interactive PTY flow works:
- `process.Wait` + `process.Start` with `terminal: true`
- Dragonball's kata-agent must support PTY exec (it does, per Phase 0 spike notes)
- FIFO set created in temp dir; host stdin/stdout bridged via `cio.NewAttach`

If PTY bridging fails, fallback option: use `yoloai attach` (which calls `attachToSandbox`)
separately after `yoloai new` creates the sandbox, and diagnose from there with a real TTY.

---

## Test Protocol

Run each test individually to isolate failures. All commands run as `sudo -E`.

### T1: Verify image is current
```bash
sudo ctr -n yoloai images ls | grep yoloai-base
docker inspect yoloai-base --format '{{.RepoDigests}}'
# digests should match
```

### T2: Verify exec PATH resolution (non-interactive)
```bash
# Build + run
go build -o ./yoloai ./cmd/yoloai/
sudo -E ./yoloai new x /home/karl/x --replace --isolation vm  # no -a
sudo -E ./yoloai ls  # expect active/idle
# Then exec a command that needs PATH:
sudo -E ./yoloai exec x which script
sudo -E ./yoloai exec x which tmux
# Both should return /usr/bin/script, /usr/bin/tmux
```

### T3: Verify interactive attach (requires real TTY)
```bash
# Run from a real terminal (not via Claude Code exec):
sudo -E ./yoloai new x /home/karl/x -a --replace --isolation vm
# Should open tmux session with agent running
```

### T4: Verify --replace cycle
```bash
# Run T3 twice in a row
sudo -E ./yoloai new x /home/karl/x -a --replace --isolation vm
# detach from tmux (Ctrl-B D)
sudo -E ./yoloai new x /home/karl/x -a --replace --isolation vm
# Should destroy first VM and start a fresh one
```

---

## Implementation Order

1. **Fix 11a** — `getContainerEnv` in `runtime/containerd/exec.go` (both Exec and InteractiveExec)
2. **Fix 11b** — full path for `script` in `attachToSandbox`
3. **Run T1, T2** — verify via non-interactive test (works without real TTY)
4. **Run T3** — verify attach with real TTY (user runs manually)
5. **Run T4** — verify --replace cycle
6. **`make check`** — lint + tests must pass

---

## Deferred Issues

- **CNI teardown errors silently ignored:** `deleteNetNS` errors are swallowed in `teardownCNI`.
  Should at least log them. Low priority — netns gets recreated on next run anyway.
- **`waitForTmux` exec fallback:** If sandbox.jsonl fast-path misses (e.g. jsonl written but
  `sandbox.tmux_start` not present yet), the fallback `rt.Exec("tmux has-session")` also
  suffers from the PATH issue until Fix 11a is applied. Mitigated by: jsonl fast-path works,
  so the fallback is rarely hit.
- **`img.IsUnpacked` error ignored:** If `IsUnpacked` returns `(false, err)`, the `err != nil`
  branch skips unpacking silently. Should log or return the error. Low risk in practice.

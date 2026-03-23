# Backend Idiosyncrasies

Behaviors that differ from documentation, require non-obvious workarounds, or
have caused bugs. Use this as a first reference when a backend behaves
unexpectedly. Update it whenever you discover something new.

**How to use:** scan the symptom index below to find the relevant section, then
read the full entry for context and the fix. When you add an entry, also add a
row to the index.

---

## Symptom Index

| Symptom / error message | Section |
|---|---|
| VM loses network silently; traffic stops | [Kata: tcfilter networking model](#tcfilter-networking-model) |
| Container starts but has no network after `NewTask()` | [Kata: netns must be configured before NewTask](#kata-shim-startup-netns-must-be-fully-configured-before-newtask) |
| `EADDRINUSE` on shim start or `NewTask()` retry | [Kata: /run/kata persists on exit](#runkataname-persists-on-abnormal-exit), [EADDRINUSE on retry](#eaddrinuse-on-newtask-retry), [shim 500ms wait](#after-killing-orphaned-shim-processes-wait-500ms-before-proceeding) |
| `After 500 attempts` / kata-agent unreachable (Firecracker) | [Kata: Firecracker explicit config breaks boot](#firecracker-runtime-rs-explicit-config-path-breaks-vm-boot) |
| Bind mount target missing inside Kata VM | [Kata: no auto-create of mount targets](#kata-does-not-auto-create-bind-mount-target-directories) |
| `hotplug memory error: ENOENT` in kata-agent logs | [Kata: hotplug ENOENT is normal](#hotplug-memory-error-enoent-is-normal) |
| Task stays in `Created` after `Start()` returns | [Containerd: task.Start returns early](#taskstart-returns-before-the-vm-is-actually-running) |
| `parent snapshot sha256:... does not exist: not found` | [Containerd: WithNewSnapshot doesn't unpack](#withnewsnapshot-does-not-unpack-image-layers) |
| `docker save \| ctr import` hangs indefinitely | [Containerd: pipe hang on ctr failure](#docker-save--ctr-import-hangs-if-ctr-fails-early) |
| Containerd socket: no error from `os.Stat` despite permission denied | [Containerd: Stat can't detect EPERM](#osstat-on-the-containerd-socket-does-not-detect-permission-denied) |
| Containerd GC removes blobs; image becomes unrunnable | [Containerd: GC removes child blobs](#containerd-gc-removes-child-blobs-while-leaving-the-root-manifest-intact) |
| `already exists` on snapshot create after crash | [Containerd: orphaned snapshots](#kata-orphaned-snapshots-from-crashed-runs-must-be-pre-cleared) |
| `PermissionError` reading secrets in Kata VM | [Containerd: 0600 secrets after gosu](#kata-0600-secret-files-cause-permissionerror-after-gosu-uid-switch) |
| CNI bridge plugin: "netns and CNI_NETNS should not be the same" | [CNI: netns.NewNamed switches OS thread](#netnsnewnamed-switches-the-os-thread-via-unshare-and-never-restores-it) |
| `createNetNS` fails with "file exists" (EEXIST) | [CNI: stale netns file](#stale-named-netns-files-at-varrunnetnsname-persist-after-failed-runs) |
| CNI-FORWARD rules deleted for a running container | [CNI: pre-flight n.Remove deletes live rules](#the-pre-flight-nremove-can-delete-rules-for-running-containers) |
| IPAM allocates duplicate IP after replace | [CNI: stale IPAM lease](#cnI-results-cache-lives-at-varlibcniresults) |
| Two concurrent `yoloai new` with same name corrupts networking | [CNI: concurrent creation race](#two-yoloai-new-invocations-for-the-same-container-name-within-1s-will-corrupt-networking) |
| `overlayfs mount` fails with `EPERM` inside Docker | [Docker: AppArmor blocks mount](#apparmor-blocks-mount2-even-with-cap_sys_admin) |
| `git apply` silently fails on overlay patch | [Docker: Exec strips trailing newline](#docker-sdk-exec-strips-the-trailing-newline) |
| `tmux attach` exits with `EACCES` on `/dev/tty` (gVisor ARM64) | [Docker: gVisor ARM64 TIOCSCTTY](#gvisor-on-arm64-docker-exec--it-does-not-call-tiocsctty) |
| Container starts as root / wrong uid under rootless Podman | [Podman: rootless detection uses socket path](#rootless-detection-must-use-socket-path-not-osgetuid) |
| Wrong uid inside container on macOS Podman | [Podman: macOS keep-id maps VM uid](#macos---usernkeep-id-maps-the-podman-machine-uid-1000-not-the-macos-uid) |
| Podman rejects per-file bind mounts for secrets | [Podman: per-file bind mounts rejected](#per-file-bind-mounts-rejected-by-podmans-docker-compatible-api) |
| Secrets / files missing inside Tart VM | [Tart: VirtioFS directories only](#virtiofs-only-supports-directory-mounts-not-individual-files) |
| Shell command fails with "no such file" on VirtioFS path | [Tart: VirtioFS path has spaces](#virtiofs-mount-path-inside-the-vm-contains-spaces) |
| VM dies when `Start()` context is cancelled | [Tart: tart run needs exec.Command](#tart-run-process-must-use-execcommand-not-execcommandcontext) |
| DNS works but HTTPS to api.anthropic.com times out | [DNS: timeout = API unreachable, not DNS](#request-timed-out-in-claude-code--api-unreachable-not-dns-failure) |
| `iptables` warnings about legacy tables | [iptables-nft: legacy tables warning](#iptables--iptables-nft-both-iptables-legacy-and-iptables-nft-can-coexist) |

---

## Kata Containers (containerd, Dragonball VMM)

### tcfilter networking model

Kata reads the CNI-configured netns, then:
1. Creates `tap0_kata` inside the netns (a TUN/TAP device)
2. Sets up Linux TC mirred filters: `tap0_kata` ↔ `eth0` (the veth created by CNI)
3. **Does NOT delete `eth0`** — both `eth0` and `tap0_kata` coexist in the netns
4. The VMM (Dragonball) binds to `tap0_kata` in the netns for VM network I/O
5. Inside the VM, traffic arrives on a virtio NIC with the same MAC as `eth0`

If `eth0` is deleted from the netns (by any means), the TC mirred filter's target
device becomes `*` (null/missing), and the VM loses all network connectivity.
Kata itself does NOT detect or report this; it continues running.

### Kata shim startup: netns must be fully configured before `NewTask()`

Kata reads `eth0` from the netns at **shim startup time** (during `NewTask()`).
The Kata shim logs show `veth network interface found: eth0` with its IP and MAC.
After this point, Kata has committed to using that `eth0`; changes to the netns
veth are not reflected.

### `/run/kata/<name>/` persists on abnormal exit

The shim creates `/run/kata/<name>/shim-monitor.sock` at startup. If the shim
dies without cleanup, this directory persists. A subsequent shim start for the
same container name fails with `EADDRINUSE`. Must call `removeKataStateDir()`
before retrying. See `lifecycle.go::removeKataStateDir`.

### TTRPC shim socket: uppercase hex SHA256

Containerd's `SocketAddress()` formula for the TTRPC socket is:
`/run/containerd/s/<sha256(containerdSock + "/" + namespace + "/" + taskID)>`.
The **Kata Rust shim** formats the hash as **uppercase hex** (`%X`). The Go
shim would use lowercase. Remove both defensively.

### `EADDRINUSE` on `NewTask()` retry

If a shim fails after binding the TTRPC socket but before containerd registers it,
the orphaned socket file causes the next `NewTask()` to fail with `EADDRINUSE`.
The retry loop in `Start()` handles this. Kill the orphaned shim PID first, then
remove state directories, then retry.

### Firecracker runtime-rs: explicit config path breaks VM boot

The `io.containerd.kata-fc.v2` shim (Firecracker, runtime-rs ≥ 3.x) selects
the Firecracker VMM automatically based on the runtime type — no config path
needed. Passing `configuration-rs-fc.toml` explicitly causes the shim to
override its built-in vsock setup, resulting in "After 500 attempts" (the
kata-agent becomes unreachable and the task never reaches Running).

Fix: return `""` from `kataConfigPath()` for all runtimes, matching the
behavior of `ctr run` (which works). See `lifecycle.go::kataConfigPath`.

### Kata does NOT auto-create bind mount target directories

Standard Docker (runc) creates any missing bind mount target directories/files
automatically before applying mounts. Kata Containers' kata-agent does NOT — it
applies mounts against the virtiofs-shared rootfs which may be read-only at
mount time. If a target path doesn't exist in the image, the mount silently
fails (the directory does not appear inside the VM).

All bind mount targets must pre-exist in the container image:
- `/yoloai/logs`, `/yoloai/files`, `/yoloai/cache`, `/yoloai/overlay`
- `/yoloai/agent-status.json`, `/yoloai/runtime-config.json`, `/yoloai/prompt.txt`
- `/run/secrets`
- Agent state dirs: `/home/yoloai/.claude/`, `.gemini/`, `.codex/`, etc.
- Home seed file placeholders: `.claude.json`, `.opencode.json`, etc.

See commit fc3be64.

### Kata shim teardown lag: `Delete()` fails transiently after task exit

The Kata shim continues running briefly after the task exit event fires. An
immediate `container.Delete()` may fail with a transient error. Must retry with
a delay (5 attempts × 2s). See `lifecycle.go::retryDelete`.

### After killing orphaned shim processes, wait 500ms before proceeding

Sending `SIGKILL` to an orphaned `containerd-shim-kata` process does not
immediately release the TTRPC socket file. The OS needs approximately 500ms.
Retrying `NewTask()` too quickly still hits `EADDRINUSE`. See
`lifecycle.go::Create` and `Start`.

### `hotplug memory error: ENOENT` is normal

The Kata agent logs `{"msg":"hotplug memory error: ENOENT","level":"WARN",...}` on
every boot. This is benign — it means no memory hotplug device is present, which
is expected for non-balloon-memory configurations.

---

## CNI (Container Network Interface)

### Rule of thumb: plugin DEL in reverse ADD order

`libcni`'s `DelNetworkList` runs plugins in **reverse** order of the conflist:
for `[bridge, portmap, firewall]` ADD order, DEL order is `firewall → portmap → bridge`.

### The pre-flight `n.Remove()` can delete rules for RUNNING containers

`runCNIAdd` calls `n.Remove()` before `n.Setup()` to clean up stale state from a
**previous failed run**. If the **same container name** is reused (e.g. second
`yoloai new` with `--replace`, or the test using a predictable run_id), the
pre-flight DEL finds the old CNI cache and runs the firewall DEL — **deleting
CNI-FORWARD rules for whatever IP was in the cache, which may still be live**.

**Key observation**: The pre-flight DEL uses the container ID to look up the CNI
results cache at `/var/lib/cni/results/<netName>-<containerID>-<ifName>`. If a
container was previously created and its cache not cleaned up, the rules for the
OLD IP get deleted. If IPAM re-allocates the same IP to the new container, those
rules were needed for the new container too.

### CNI results cache lives at `/var/lib/cni/results/`

Cache key: `<networkName>-<containerID>-<ifName>` (e.g. `yoloai-yoloai-foo-eth0`).
Written by `cacheAdd` after successful `AddNetworkList`. Used by `DelNetworkList`
to recover the prevResult for DEL. `cacheDel` removes it at end of successful DEL.

If teardown fails mid-way, the cache file may be left behind. A subsequent ADD
pre-flight DEL will find it and use it.

### `AppendUnique` does not protect against interleaved ADD/DEL

If thread A calls `AppendUnique` to add rule R, and thread B calls `Delete` to
remove rule R, and then thread A calls `AppendUnique` again for a different rule,
rule R is gone from the chain permanently (no re-check). This is not a problem
in normal sequential operation but IS a problem if two `yoloai new` calls for the
same container name run concurrently.

### Firewall plugin: silent no-op when `result.IPs` is empty

`addRules()` does nothing if `result.IPs` is empty. This matters for the
pre-flight DEL (which passes an empty prevResult when there's no cache). BUT if
something causes `parseConf` to return an empty result during ADD (e.g., the
bridge plugin passes no IPs), the firewall plugin silently succeeds without adding
any CNI-FORWARD rules. No error is returned.

### `SetupIPMasq` creates a **chain jump**, not a bare MASQUERADE

The bridge plugin's `SetupIPMasq` creates a per-container chain `CNI-XXXXXXXX`
containing `ACCEPT` + `MASQUERADE` rules, then adds a POSTROUTING jump to it:
`-s <ip> -j CNI-XXXXXXXX`. A bare `MASQUERADE` rule in POSTROUTING (without a
comment or chain jump) is **not** from `SetupIPMasq`; it indicates broken state —
either a partial teardown that deleted the chain but not the POSTROUTING rule, or
a different tool wrote that rule.

### `TeardownIPMasq` deletes by exact match (comment included)

`TeardownIPMasq` calls `ipt.Delete("nat", "POSTROUTING", "-s", ip, "-j", chain, "-m", "comment", "--comment", comment)`.
If the comment or chain name doesn't match exactly, the rule is NOT deleted. This
can leave stale POSTROUTING rules after teardown.

### Two `yoloai new` invocations for the same container name within ~1s WILL corrupt networking

The sequence is:
1. Run A: creates netns, runs CNI ADD for name X (allocates IP 10.x.x.y, adds rules, writes cache)
2. Run B (before A has exited): `setupCNI` calls `deleteNetNS` → **destroys the netns
   from run A, which removes `eth0` from that netns, which was already handed to Kata**
3. Run B: creates fresh netns, pre-flight DEL finds A's cache → deletes A's iptables rules
4. Run B: CNI ADD creates new netns with same or different IP — but A's Kata shim has
   already committed to the old `eth0` (now deleted). A's container is broken.

This is the cause of the `firewall-test1` case where `eth0` disappeared: a second run
was created while the first was still running.

---

## iptables-nft on Ubuntu 24.04

### `iptables` = iptables-nft; both `iptables-legacy` and `iptables-nft` can coexist

Running `iptables` actually invokes `iptables-nft`. Rules created by either tool
are stored in different nftables tables/chains BUT both can be listed by the same
`iptables` command (nft sees both). Always run `iptables` (not `iptables-legacy`)
for CNI troubleshooting; legacy rules won't affect CNI traffic since CNI uses nft.

`iptables` warns `# Warning: iptables-legacy tables present, use iptables-legacy
to see them` when both are active. Ignore this for CNI work.

### `iptables-save` format shows exact rule ordering

`iptables -L` reorders rules for display (e.g., show all chains). Use
`iptables-save` to see the true append/insert order in the chain.

### CNI-FORWARD rule ordering reflects add order

`setupChains()` calls `ensureFirstChainRule()` to insert `CNI-ADMIN` at position 1.
`addRules()` then `AppendUnique`s per-IP rules to the END of CNI-FORWARD.
Normal result: CNI-ADMIN first, then per-IP rules in creation order.

If per-IP rules appear BEFORE CNI-ADMIN in the chain, something called
`AppendUnique` before `setupChains` could insert CNI-ADMIN (i.e., the chain was
empty when `addRules` ran, then a DIFFERENT call's `setupChains` re-inserted
CNI-ADMIN at position 1, pushing the already-appended IP rules down... actually
this is impossible; see the actual cause in the "two `yoloai new`" item above).

---

## DNS inside Kata VMs

### The VM uses the HOST's upstream DNS resolver, not a bridge DNS

The resolv.conf inside the container is bind-mounted from
`/run/systemd/resolve/resolv.conf` (or `/etc/resolv.conf`). On a systemd-resolved
host this points to the **upstream resolver** (e.g. `192.168.111.1`, the physical
router), NOT to `127.0.0.53` (systemd-resolved's stub, which is unreachable from
inside the VM).

This means DNS queries from inside the VM go to the external router IP. The VM
must be able to reach that IP via its default route through `yoloai0`. If
networking is broken (no CNI-FORWARD ACCEPT rules), DNS queries are silently
dropped at FORWARD.

### "Request timed out" in Claude Code = API unreachable, NOT DNS failure

When Claude Code prints `Request timed out. Retrying in 11 seconds…`, it means
the **HTTPS connection** to `api.anthropic.com` timed out. DNS might still work
(nslookup succeeds) but TCP/TLS to port 443 is dropped by the FORWARD chain.

To distinguish: run `curl --connect-timeout 5 https://api.anthropic.com/` inside
the VM. `000` = TCP timeout/refused; `4xx` = TCP connected, HTTP response received.

---

---

## Docker

### AppArmor blocks `mount(2)` even with `CAP_SYS_ADMIN`

Docker's default AppArmor profile blocks `mount()` syscalls even when
`CAP_SYS_ADMIN` is granted via `CapAdd`. Without explicitly disabling AppArmor,
the entrypoint cannot mount overlayfs inside the container and gets `EPERM`.

Workaround: add `security-opt apparmor=unconfined` whenever `SYS_ADMIN` appears
in `CapAdd`. See `docker.go::Create`. This is not advisory — the mount literally
fails otherwise.

### Docker SDK `Exec` strips the trailing newline

`ContainerExecAttach` + `stdcopy.StdCopy` output is fed through
`strings.TrimSpace`, which removes the trailing newline from `git diff` output.
`git apply` requires a trailing newline to parse patches; without it, the patch
is silently rejected or applies incorrectly.

Workaround: re-append `\n` to the patch bytes if the last byte is not `\n`
before calling `git apply`. See `Fix: restore trailing newline in overlay patch
output` (commit f9bf669).

### gVisor on ARM64: `docker exec -it` does not call `TIOCSCTTY`

When running in `container-enhanced` isolation on ARM64, `docker exec -it` does
not set a controlling terminal (`TIOCSCTTY`). The exec'd process has no CTY, and
`tmux attach` exits with `EACCES` when it tries to open `/dev/tty`.

Workaround: use `setsid tmux attach` instead of `script -q -e -c 'tmux attach'`.
`setsid` starts a new session with no controlling terminal; `/dev/tty` returns
`ENXIO`, which tmux handles by falling back to stdin (the PTY from `docker
exec -it`). See `docker.go::AttachCommand`.

Note: this is ARM64-specific. On AMD64, `script` creates a fresh PTY and CTY
cleanly without this issue.

---

## Podman

### Rootless detection must use socket path, not `os.Getuid()`

Checking `os.Getuid() != 0` to detect rootless Podman is wrong. When the user
runs `sudo -E yoloai`, `os.Getuid()` returns 0, but the socket is still the
user's rootless socket (e.g. `$XDG_RUNTIME_DIR/podman/podman.sock`). Passing
`--userns=keep-id` to a system Podman socket fails; not passing it to a rootless
socket causes the container to start as root and exit immediately.

Correct approach: check the socket path. `/run/podman/podman.sock` is the
system (non-rootless) socket. Everything else — `$XDG_RUNTIME_DIR`, WSL2 paths,
Podman Machine, `CONTAINER_HOST` — is treated as rootless. See
`podman.go::socketIsRootless`.

### macOS: `--userns=keep-id` maps the Podman Machine uid (1000), not the macOS uid

On macOS, Podman runs via Podman Machine (a Linux VM). `--userns=keep-id` maps
the VM user's uid (1000) into the container — not the macOS user's uid (e.g.
501). The container then runs as uid 1000, but `/home/yoloai` is owned by uid
1001 (the `yoloai` user), so agents cannot write their config.

Workaround: skip `keep-id` on macOS (`runtime.GOOS == "darwin"`). The
entrypoint uses `gosu` to remap `yoloai` to the correct uid, which is the same
path Docker takes. See `podman.go::Create`.

### Per-file bind mounts rejected by Podman's Docker-compatible API

Podman's Docker-compatible socket rejects per-file bind mounts where the source
is an existing file (e.g. `/run/secrets/ANTHROPIC_API_KEY`). Podman tries to
`mkdir` the source path, which fails with `EPERM`. Docker handles per-file bind
mounts correctly.

Workaround: bind-mount the entire secrets directory as one mount
(`/run/secrets → /run/secrets`) instead of individual per-secret file mounts.
See commit fefda87.

---

## Containerd

### `WithNewSnapshot` does NOT unpack image layers

`client.WithNewSnapshot(name, img)` only calls `Prepare(parent)` on the
top-level chain ID, expecting the snapshot chain to already exist. If the image
was imported via `ctr import` but not yet unpacked, container creation fails
with: `parent snapshot sha256:... does not exist: not found`.

Must explicitly call `img.IsUnpacked()` / `img.Unpack(ctx, snapshotter)` before
`NewContainer()`. See `lifecycle.go::Create`.

### `docker save | ctr import` hangs if `ctr` fails early

If `ctr images import` exits with an error (e.g. permission denied on the
containerd socket) while `docker save` is still writing to the pipe, `docker
save` blocks indefinitely on a write to a broken pipe. The parent process hangs.

Must wait on `importCmd.Wait()` first, and if it fails, immediately call
`saveCmd.Process.Kill()` before calling `saveCmd.Wait()`. See `image.go::Setup`.

### `os.Stat` on the containerd socket does not detect permission denied

`os.Stat("/run/containerd/containerd.sock")` succeeds even when the process has
no permission to open the socket (EPERM). The stat only checks directory entry
existence. Must use `os.Open()` to distinguish ENOENT from EPERM. See `Fix:
containerd backend: detect socket permission denied` (commit e24d201).

### Containerd GC removes child blobs while leaving the root manifest intact

When registering images in a new namespace via cross-namespace content sharing,
the garbage collector can remove platform manifest blobs, config blobs, and
layer blobs while leaving the root manifest list entry intact. Checking only
the root with `cs.Info(root)` is insufficient for verifying image readiness.

Must walk the full descriptor tree with `images.Children` to verify all blobs
are accessible. See `image.go::verifyDescriptorTree`.

### Cross-namespace content sharing requires `containerd.io/namespace.shareable=true`

To share content from Docker's containerd namespace (`moby` or `default`) into
yoloai's namespace without copying data, the source namespace must be labeled
`containerd.io/namespace.shareable=true`. Without this label, `cs.Writer` +
`w.Commit` triggers a full data write instead of the zero-copy metadata path.

Additionally, all parent blobs (manifest list, platform manifests) must have
`containerd.io/gc.ref.content.*` labels set manually. GC only follows the
direct image → root target link by default; without these labels it cannot
reach manifests, configs, and layers further down the tree and will collect them.
See `image.go::linkFromDockerNamespace`, `shareDescriptorTree`, `setGCRefLabels`.

### Kata: orphaned snapshots from crashed runs must be pre-cleared

When a Kata container run crashes after snapshot creation but before container
deletion, a snapshot without a corresponding container record is left behind.
The next `NewContainer()` with `WithNewSnapshot` fails because a snapshot of
the same name already exists.

Must call `r.client.SnapshotService(snapshotter).Remove(ctx, name)` before
`NewContainer()` in addition to the existing stale-container pre-clear. Errors
are silently ignored (snapshot may not exist). See `lifecycle.go::Create` and
commit bf23e95.

### `task.Start` returns before the VM is actually running

For Kata Containers (full Linux VM boot), `task.Start` returns as soon as the
shim acknowledges the `Start` RPC — the VM is still in `Created` state and
may take 10–60 seconds to reach `Running`. Callers that check running state
immediately after `Start()` returns will see `Created`.

Must poll `task.Status()` until the status is `Running` or `Stopped`. The
60-second timeout is chosen based on observed Kata boot times (Dragonball ~5s,
Firecracker ~10s on fast hardware; slow CI can be 30s+). See `lifecycle.go::Start`.

### Kata: 0600 secret files cause `PermissionError` after gosu uid switch

Secret files bind-mounted into Kata containers are owned by root with mode
0600. `entrypoint.py` reads them as root (before `gosu` switches to uid 1001).
`sandbox-setup.py` runs after `gosu` and cannot read 0600 files owned by uid 0.

Workaround in `sandbox-setup.py`: wrap secret file reads in `try/except OSError`
and skip unreadable files. `entrypoint.py` already handles the secrets correctly
before the uid switch. See commit bf23e95.

### `netns.NewNamed()` switches the OS thread via `unshare(CLONE_NEWNET)` and never restores it

`netns.NewNamed()` internally calls `unshare(CLONE_NEWNET)`, which moves the
**calling OS thread** into the new network namespace and does not restore it.
When CNI plugin executables are subsequently spawned, they inherit the switched
OS thread's namespace. The bridge plugin then sees `CNI_NETNS == current netns`
and rejects the call with "netns and CNI_NETNS should not be the same".

Fix: call `runtime.LockOSThread()` before `netns.NewNamed()`, then manually
save (`netns.Get()`) and restore (`netns.Set(origNS)`) the original namespace
around the call. See `cni.go::createNetNS`.

### Stale named netns files at `/var/run/netns/<name>` persist after failed runs

If a previous run failed after `createNetNS()` but before `teardownCNI()` had a
chance to call `deleteNetNS()`, the named netns file persists at
`/var/run/netns/yoloai-<name>`. The next run's `netns.NewNamed()` fails with
"file exists" (EEXIST).

Must call `deleteNetNS(nsName)` unconditionally before `createNetNS()`. This is
safe because `deleteNetNS` is idempotent (ignores ENOENT). See `cni.go::setupCNI`.

---

## Tart (macOS VMs)

### VirtioFS only supports directory mounts, not individual files

`tart run --dir name:path` only accepts directories. Any per-file bind mount
(e.g. a `/run/secrets/API_KEY` file) is silently skipped — no error is returned
by `tart run`, the file simply does not appear inside the VM.

Workaround: copy file contents into a sandbox directory and share the directory
via VirtioFS. For secrets, copy all secret files into `sandboxDir/secrets/` and
share `sandboxDir` as the `yoloai` VirtioFS share. See `tart.go::Create`.

### VirtioFS mount path inside the VM contains spaces

Tart mounts VirtioFS shares at `/Volumes/My Shared Files/<share-name>` inside
the macOS VM. The path contains a space. Any shell command constructing this
path must quote it. The setup script uses: `'%s/bin/sandbox-setup.py'` with
`%s = /Volumes/My Shared Files/yoloai`. See `tart.go::runSetupScript`.

### `ln -sfn` won't replace a directory; must use `rm -rf` first

Inside the Tart VM, when creating symlinks from expected mount target paths to
VirtioFS paths, `ln -sfn target link` silently creates the symlink *inside* the
target directory rather than replacing it, if a directory already exists at
`link`. Must explicitly `rm -rf link` before `ln -sfn`. See the symlink command
in `tart.go::runSetupScript`.

### `tart run` process must use `exec.Command`, not `exec.CommandContext`

`tart run <vmName>` is a long-lived process that keeps the VM alive. Using
`exec.CommandContext` with the parent's context would kill the VM when the
`Start()` function's context is cancelled (e.g. on HTTP request completion or
timeout). Must use bare `exec.Command`, then set `SysProcAttr{Setpgid: true}`
to detach it from the parent process group. See `tart.go::Start`.


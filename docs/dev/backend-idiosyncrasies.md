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
| `mkdir: /var/folders: Permission denied` during Tart setup | [Tart: mkdir system dirs fails](#tart-cannot-mkdir-system-directories-like-varfolders) |
| Tart base image rebuilt every time `yoloai new` runs | [Tart: empty sourceDir breaks marker](#empty-sourcedir-breaks-tart-provisioning-marker-file-check) |
| `tart exec` fails with "instance not found" right after boot | [Tart: exec needs stabilization delay](#tart-exec-needs-brief-stabilization-delay-after-boot) |
| `tart exec` with `--` separator fails silently or returns exit status 1 | [Tart: no support for -- separator](#tart-exec-does-not-support----argument-separator) |
| `yoloai attach` fails with "no sessions" on Tart VM | [Tart: exec -t changes environment](#tart-exec--t-changes-environment-preventing-tmux-from-finding-socket) |
| `xcrun simctl list runtimes` shows no runtimes when mounted via VirtioFS | [Tart: CoreSimulator requires sealed APFS](#coresimulator-cannot-discover-virtiofs-mounted-runtimes) |
| `Failed to start launchd_sim: could not bind to session` when booting simulator | [Tart: ditto'd runtime is incomplete](#dittod-ios-runtime-is-incomplete-use-xcodebuild--downloadplatform) |
| DNS works but HTTPS to api.anthropic.com times out | [DNS: timeout = API unreachable, not DNS](#request-timed-out-in-claude-code--api-unreachable-not-dns-failure) |
| `iptables` warnings about legacy tables | [iptables-nft: legacy tables warning](#iptables--iptables-nft-both-iptables-legacy-and-iptables-nft-can-coexist) |
| `--isolation vm` rejected on macOS / "containerd not available" | [Registry: containerd Linux-only](#containerd-backend-is-linux-only) |

---

## Runtime Backend Registry

### containerd backend is Linux-only

**Symptom:** Using `--os linux --isolation vm` on macOS fails with:
```
yoloai: --isolation vm requires containerd, which is not available on macOS.
Use a Linux host for VM isolation, or use --os mac for macOS-native sandboxing:
  container   macOS sandbox-exec (seatbelt)
  vm          Full macOS VM (Tart)
```

**Explanation:** The backend registry pattern introduced in commit 69c18f1 makes
backends register themselves at init() time only on supported platforms. Containerd
uses `//go:build linux` tags and only registers on Linux. On macOS, attempting to
use `--os linux --isolation vm` will fail at backend resolution time because containerd
is not in the registry.

**Fix:** On macOS, use `--os mac --isolation vm` to get Tart VMs instead. Smoke tests
and other cross-platform tooling should avoid specifying `os=linux` with `isolation=vm`
on macOS hosts.

**Code:** `runtime/registry.go`, `runtime/containerd/containerd.go`, `internal/cli/helpers.go:resolveBackend()`

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

### Tart cannot mkdir system directories like /var/folders

**Symptom:** VM setup fails with:
```
mkdir: /var/folders: Permission denied
```

**Explanation:** During mount setup, Tart creates symlinks from expected mount paths
to VirtioFS share paths. The setup tries to `mkdir -p` parent directories, but on macOS
paths like `/var/folders/h8/...` (system temp directories) cannot be created by regular
users. The parent directories already exist (created by macOS), so the mkdir failure can
be ignored.

**Fix:** Make mkdir non-fatal: `(mkdir -p '$parent' 2>/dev/null || true)`. If the parent
exists (which it should for system paths), the symlink creation proceeds successfully.

**Code:** `runtime/tart/tart.go::runSetupScript` line ~657

### Empty sourceDir breaks Tart provisioning marker file check

**Symptom:** Tart base image is rebuilt every time `yoloai new` is called, even though it was already provisioned. Output shows:
```
Removing old provisioned image...
Cloning base image for provisioning...
[... full provisioning steps ...]
Base VM image provisioned successfully.
```

**Explanation:** The Setup method checks for a `.tart-provisioned` marker file to skip unnecessary rebuilds. When `sourceDir` is an empty string, `filepath.Join("", ".tart-provisioned")` evaluates to `.tart-provisioned` in the current directory rather than the intended `~/.yoloai/profiles/base/.tart-provisioned`. The marker check fails, so Setup always rebuilds.

The marker is correctly written to the profile directory during provisioning, but the check looks in the wrong place when sourceDir is empty.

**Fix:** Pass the correct base profile directory to Setup:
```go
// Before (manager.go line 163):
if err := m.runtime.Setup(ctx, "", m.output, m.logger, false); err != nil {

// After:
baseProfileDir := config.ProfileDirPath("base")
if err := m.runtime.Setup(ctx, baseProfileDir, m.output, m.logger, false); err != nil {
```

**Impact:** Before the fix, every `yoloai new` command triggered a 2-3 minute base image rebuild, significantly slowing down sandbox creation and making tests much slower.

**Code:** `sandbox/manager.go::RunSetup` line ~163, `runtime/tart/build.go::Setup` and `isProvisioned`

### Tart exec needs brief stabilization delay after boot

**Symptom:** VM starts successfully and passes initial `tart exec <vmName> true` check during boot wait, but immediately after, running commands with `tart exec` fails with:
```
instance not found
```

**Explanation:** The `waitForBoot` polling loop succeeds as soon as `tart exec <vmName> true` returns successfully once. However, Tart's guest agent (which handles exec requests via gRPC) may need a brief moment to fully stabilize before it can reliably handle more complex commands. The first simple `true` command succeeds, but subsequent commands that require more setup (like bash with complex shell commands) fail with "instance not found".

This is likely a race condition where the guest agent is partially initialized - enough to handle a simple command, but not yet fully ready for complex operations.

**Fix:** Add a 500ms stabilization delay after `waitForBoot` succeeds and before calling `runSetupScript`:
```go
// Brief delay to let the VM fully stabilize after first successful exec.
// Tart's guest agent may need a moment to be fully ready for complex commands.
time.Sleep(500 * time.Millisecond)
```

**Impact:** Without the delay, VM creation fails intermittently with "instance not found" errors, especially noticeable in automated tests where VMs are created and used quickly.

**Code:** `runtime/tart/tart.go::Start` after `waitForBoot`

### Tart exec does not support `--` argument separator

**Symptom:** Commands constructed with `tart exec <vmName> -- <command>` fail silently or return `exit status 1` without clear error messages. For example:
```bash
tart exec vm-name -- sudo mkdir -p /some/path
# Returns exit status 1 with no stderr
```

**Explanation:** Unlike many CLI tools that use `--` to separate flags from arguments, `tart exec` does not support or recognize the `--` separator. The tart command interprets `--` as a literal argument to pass to the VM, which confuses the command execution.

The correct syntax is: `tart exec <vm-name> <command> [args...]`

All working tart exec invocations in the codebase use the `execArgs()` helper function which does NOT include `--`. Additionally, sudo commands in provisioning are wrapped in `bash -c "..."` for proper shell expansion and error handling.

**Fix:** Remove `--` separators from tart exec commands:
```go
// Wrong - includes --
cmd := exec.CommandContext(ctx, "tart", "exec", vmName, "--", "sudo", "mkdir", "-p", path)

// Correct - use execArgs helper
args := execArgs(vmName, "bash", "-c", fmt.Sprintf("sudo mkdir -p %s", path))
cmd := exec.CommandContext(ctx, r.tartBin, args...)
```

For commands that need shell features (variables, pipes, etc.) or sudo, wrap them in `bash -c "..."`:
```go
args := execArgs(vmName, "bash", "-c", "sudo mkdir -p /Library/Developer/...")
```

**Impact:** Commands fail with unclear exit status 1 errors. Runtime copying functionality completely broken during base image creation.

**Code:** `runtime/tart/tart.go::execArgs`, `runtime/tart/build.go` (provisioning commands), `runtime/tart/runtime_copy.go` (needs fix)

### Tart exec -t changes environment, preventing tmux from finding socket

**Symptom:** When running `yoloai attach` on a Tart VM, tmux fails with "no sessions" even though `tart exec yoloai-x tmux ls` shows the session exists. The attach command reaches "attaching to tmux session" in logs but then fails with exit status 1.

**Explanation:** When `tart exec` is invoked with the `-t` flag (PTY allocation), it changes the environment in a way that prevents tmux from locating its socket at the default UID-based location. Specifically:

- Without `-t`: tmux finds the socket at `/private/tmp/tmux-501/default`
- With `-t`: tmux looks for the socket in a different location (likely due to `$TMPDIR` changes)

The tmux server is created by the sandbox-setup script and uses the default socket location (no `-S` specified). Later, when attaching, `tart exec -i -t` creates an environment where tmux can't find this socket.

**Fix:** Explicitly specify the tmux socket path with `-S` in all tmux commands. The Tart runtime's `TmuxSocket()` method now returns `/private/tmp/tmux-501/default` (the admin user's default socket). This path is written to `runtime-config.json` during sandbox creation and read back during attach, ensuring the attach command uses `-S` to specify the socket explicitly.

Manual test that confirms the issue:
```bash
# Fails - tmux can't find socket
tart exec -i -t yoloai-x tmux attach -t main

# Works - explicit socket path
tart exec -i -t yoloai-x tmux -S /private/tmp/tmux-501/default attach -t main
```

**Impact:** `yoloai attach` completely broken for Tart VMs. Users cannot attach to running sandboxes.

**Code:** `runtime/tart/tart.go::TmuxSocket` (returns explicit socket path), `runtime/tart/tart.go::AttachCommand` (uses socket when provided)

### CoreSimulator cannot discover VirtioFS-mounted runtimes

**Symptom:** When iOS/tvOS/watchOS simulator runtimes are mounted via VirtioFS (even with proper symlinks), `xcrun simctl list runtimes` shows no runtimes or hangs indefinitely. The investigation document noted this empirically but didn't explain the technical mechanism.

**Root cause - Runtimes are Sealed APFS Disk Images:**

iOS Simulator runtimes are **not directories** - they are sealed APFS disk images:
```bash
$ mount | grep CoreSimulator/Volumes/iOS
/dev/disk13s1 on /Library/Developer/CoreSimulator/Volumes/iOS_23E244
  (apfs, sealed, local, nodev, nosuid, read-only, journaled, noatime, nobrowse)

$ diskutil apfs list | grep -A5 "iOS 26.4 Simulator"
|       Name:                      iOS 26.4 Simulator
|       Mount Point:               /Library/Developer/CoreSimulator/Volumes/iOS_23E244
|       Sealed:                    Yes  ← CRITICAL
```

**CoreSimulator's strict discovery requirements:**

1. **Sealed APFS volumes required** - Runtimes must be mounted as `sealed` APFS volumes for Apple's cryptographic code signing verification. VirtioFS is a network filesystem (9P/virtio) and cannot provide APFS volume semantics or the `sealed` property.

2. **Volume mount notifications** - CoreSimulator listens for macOS `DiskArbitration` volume mount events. From CoreSimulator.framework strings: `"Checking for mountable runtimes at '%@' due to volume mount notification"`. VirtioFS shares don't trigger system-level volume mount notifications.

3. **Disk image management** - CoreSimulator uses `SimDiskImageManager` to track runtime disk images. It expects `mountable` `.dmg` files managed by the MobileAsset system, located in `/System/Library/AssetsV2/com_apple_MobileAsset_*SimulatorRuntime/`. These are auto-mounted with specific APFS properties.

4. **Filesystem type checking** - Even symlinks to VirtioFS paths fail because CoreSimulator verifies the underlying filesystem type. Network filesystems are rejected.

**Why "symlink test" in investigation was misleading:**

The investigation's symlink test (docs/dev/ios-testing-investigation.md:656-662) moved a **local directory** to another location and symlinked it - this worked because both source and target were on the same local APFS volume. When the symlink points to a **VirtioFS mount**, the filesystem semantics are fundamentally different and CoreSimulator rejects it.

**This is a fundamental architectural limitation** - VirtioFS cannot emulate sealed APFS volumes. Runtimes **must** be copied to local VM storage or downloaded fresh inside the VM.

**Workaround:** Hybrid approach (validated in investigation):
- Mount Xcode.app from host via VirtioFS (saves ~11GB) - works fine
- Mount PrivateFrameworks from host via VirtioFS (saves ~2GB) - works fine
- **Copy or download runtimes locally** inside VM (~8-16GB per runtime) - required

**Code:** See `docs/dev/ios-testing-investigation.md` lines 844-966 for empirical testing; `runtime/tart/runtime_copy.go` for copy implementation.

### Ditto'd iOS runtime is incomplete; use `xcodebuild -downloadPlatform`

**Symptom:** After copying an iOS runtime from the VirtioFS mount using `ditto`, the runtime is recognized by `xcrun simctl list runtimes` but simulator devices fail to boot with:
```
Failed to prepare device for impending launch.
Unable to boot the Simulator.
Failed to start launchd_sim: could not bind to session, launchd_sim may have crashed or quit responding
```

Additionally, CoreSimulator logs show warnings about missing dyld cache.

**Root cause - Ditto cannot copy all protected files:**

Using `ditto` to copy a runtime from the VirtioFS mount at `/Library/Developer/CoreSimulator/Volumes/iOS_*/Library/.../iOS X.Y.simruntime` to local VM storage produces an **incomplete runtime**:

1. **Missing Info.plist** - Ditto may fail to copy `/Contents/Info.plist` due to permission errors, resulting in a malformed bundle that simctl cannot recognize without manual Info.plist creation.

2. **Incomplete system services** - The `modelmanagerd` directory (and potentially others) at `/Contents/Resources/RuntimeRoot/private/var/db/modelmanagerd/` has permissions that block ditto from reading (700 perms, owned by _modelmanagerd). Ditto continues after permission errors but skips these protected files.

3. **Missing or incomplete dyld cache** - Critical shared library cache components may be incomplete, causing simulator boot failures.

Even though ditto reports copying ~15GB/16GB and exits successfully (albeit with permission errors), the resulting runtime lacks components necessary for the simulator to boot. The `launchd_sim` error is a symptom of the incomplete installation, not a sandbox permission issue.

**Why the download approach works:**

Running `xcodebuild -downloadPlatform iOS` **inside the VM** downloads and installs a complete runtime:
- Downloads 8.46 GB runtime package from Apple
- Installs to `/Library/Developer/CoreSimulator/Volumes/iOS_*/...` with all components
- Runtime is fully functional - simulator devices boot successfully
- No Info.plist workarounds needed
- No launchd_sim errors

The download approach installs to the **same path** that ditto was copying to, proving the issue was incomplete file copying, not the installation location.

**Fix:** Replace runtime copying with download-inside-VM approach:
1. Mount Xcode.app and PrivateFrameworks from host via VirtioFS (saves ~13GB)
2. Configure Xcode inside VM (symlink, xcode-select, license, first-launch)
3. Run `xcodebuild -downloadPlatform iOS` (or tvOS, watchOS, visionOS) to download complete runtime
4. Verify runtime with `xcrun simctl list runtimes`

**Verification:** See `docs/dev/research/ios-runtime-download-verification.md` for complete manual verification that the download approach produces bootable simulators.

**Code:** `runtime/tart/runtime_copy.go` (currently implements ditto approach, needs replacement with download approach)

---


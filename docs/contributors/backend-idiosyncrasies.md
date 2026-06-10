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
| Agent idle 9s+, route=ok but dns/tcp probe times out (DF8) | [Kata: netns warm-up race](#kata-netns-warm-up-race-tap0_kata-tc-mirred-filter-not-installed-when-taskstart-returns) |
| Agent "Not logged in"/idle after `restart` on containerd-vm; guest log `secrets.skip` | [Kata: secrets dir removed before guest read](#kata-secrets-temp-dir-removed-before-the-guest-reads-it) |
| Tart: "secrets-consumed marker not observed before timeout" on every run (incl. passing) | [Kata: secrets dir removed before guest read](#kata-secrets-temp-dir-removed-before-the-guest-reads-it) (Tart variant) |
| `EADDRINUSE` on shim start or `NewTask()` retry | [Kata: /run/kata persists on exit](#runkataname-persists-on-abnormal-exit), [EADDRINUSE on retry](#eaddrinuse-on-newtask-retry), [shim 500ms wait](#after-killing-orphaned-shim-processes-wait-500ms-before-proceeding) |
| `After 500 attempts` / kata-agent unreachable (Firecracker) | [Kata: Firecracker explicit config breaks boot](#firecracker-runtime-rs-explicit-config-path-breaks-vm-boot) |
| Bind mount target missing inside Kata VM | [Kata: no auto-create of mount targets](#kata-does-not-auto-create-bind-mount-target-directories) |
| `hotplug memory error: ENOENT` in kata-agent logs | [Kata: hotplug ENOENT is normal](#hotplug-memory-error-enoent-is-normal) |
| `yoloai destroy` hangs; `ctr tasks ls` shows RUNNING but no qemu/firecracker; host CPU 60–80% | [Kata: shim wedge with dead VM](#kata-shim-wedge-with-dead-vm-sigkill-via-containerd-doesnt-release-the-task) |
| `yoloai destroy` hangs on a Tart sandbox; `tart list` shows VM running but guest unreachable | [Tart: VM process wedge](#tart-vm-process-wedge-tart-stop-and-sigterm-via-pgrep-dont-release-the-host-tart-run) |
| `yoloai diff <sha>` / commit listing on a Tart `:copy` sandbox errors with `unknown revision` / `not a git repository` for SHAs just listed | [Tart: commit-level git must dispatch through the runtime](#tart-commit-level-git-diff-sha-commit-listing-must-dispatch-through-the-runtime-not-host-git) |
| Task stays in `Created` after `Start()` returns | [Containerd: task.Start returns early](#taskstart-returns-before-the-vm-is-actually-running) |
| `parent snapshot sha256:... does not exist: not found` | [Containerd: WithNewSnapshot doesn't unpack](#withnewsnapshot-does-not-unpack-image-layers) |
| `docker save \| ctr import` hangs indefinitely | [Containerd: pipe hang on ctr failure](#docker-save--ctr-import-hangs-if-ctr-fails-early) |
| Containerd socket: no error from `os.Stat` despite permission denied | [Containerd: Stat can't detect EPERM](#osstat-on-the-containerd-socket-does-not-detect-permission-denied) |
| Containerd GC removes blobs; image becomes unrunnable | [Containerd: GC removes child blobs](#containerd-gc-removes-child-blobs-while-leaving-the-root-manifest-intact) |
| `yoloai apply` fails on containerd with `git diff --quiet: exec exited with code 1` | [Containerd: GitExec must return *runtime.ExecError](#gitexec-must-return-runtimeexecerror-not-a-plain-fmterrorf-on-non-zero-exit) |
| `yoloai exec <box> -- false` exits 0 on containerd-vm; isolation_check smoke flaps | [Containerd: InteractiveExec discarded the exit code](#interactiveexec-discarded-the-inner-exit-code-so-yoloai-exec-always-exited-0) |
| `already exists` on snapshot create after crash | [Containerd: orphaned snapshots](#kata-orphaned-snapshots-from-crashed-runs-must-be-pre-cleared) |
| CNI bridge plugin: "netns and CNI_NETNS should not be the same" | [CNI: netns.NewNamed switches OS thread](#netnsnewnamed-switches-the-os-thread-via-unshare-and-never-restores-it) |
| `createNetNS` fails with "file exists" (EEXIST) | [CNI: stale netns file](#stale-named-netns-files-at-varrunnetnsname-persist-after-failed-runs) |
| CNI-FORWARD rules deleted for a running container | [CNI: pre-flight n.Remove deletes live rules](#the-pre-flight-nremove-can-delete-rules-for-running-containers) |
| CNI ADD succeeds but container has no outbound connectivity (POSTROUTING and/or CNI-FORWARD ACCEPT for the IP missing in host iptables) | [Go: netns.NewNamed without LockOSThread (DF10)](#go-os-thread-netns-leak-from-netnsnewnamed--netnsset-without-runtimelockosthread); secondary: [CNI: firewall plugin silent no-op (DF9)](#firewall-plugin-silent-no-op-when-resultips-is-empty) |
| IPAM allocates duplicate IP after replace | [CNI: stale IPAM lease](#cnI-results-cache-lives-at-varlibcniresults) |
| Two concurrent `yoloai new` with same name corrupts networking | [CNI: concurrent creation race](#two-yoloai-new-invocations-for-the-same-container-name-within-1s-will-corrupt-networking) |
| `--network-isolated` silently unenforced under `--isolation container-enhanced` | [gVisor netstack ignores iptables](#gvisor-netstack-ignores-in-sandbox-iptables-rules) |
| `docker daemon is not responding` after `docker context use`; stale `/var/run/docker.sock` symlink to a stopped provider | [Docker: Go SDK ignores docker context](#the-docker-go-sdk-ignores-docker-context-clientfromenv-honors-only-docker_host) |
| dind `exec /hello: invalid argument` (any nested container) under container-privileged on macOS | [Docker: nested fuse-overlayfs can't exec on Docker Desktop / Podman Machine](#docker-in-docker-nested-fuse-overlayfs-cant-exec-on-docker-desktop--podman-machine-macos) |
| `overlayfs mount` fails with `EPERM` inside Docker | [Docker: AppArmor blocks mount](#apparmor-blocks-mount2-even-with-cap_sys_admin) |
| `sysctl: permission denied on key "net.ipv4.ip_forward"` starting inner Docker daemon | [Docker: /proc/sys and /sys/fs/cgroup read-only without systempaths=unconfined](#procsys-and-sysfsgroup-are-read-only-without-systempathsunconfined) |
| `mkdir /sys/fs/cgroup/docker: read-only file system` when inner Docker runs containers | [Docker: /proc/sys and /sys/fs/cgroup read-only without systempaths=unconfined](#procsys-and-sysfsgroup-are-read-only-without-systempathsunconfined) |
| `Seccomp_filters: 1` inside sandbox despite `container-privileged`; proc mount in userns fails | [Docker: Proxmox LXC seccomp survives seccomp=unconfined](#proxmox-lxc-seccomp-survives-secompunconfined-at-the-docker-layer) |
| `git apply` silently fails on overlay patch | [Docker: Exec strips trailing newline](#docker-sdk-exec-strips-the-trailing-newline) |
| `tmux attach` exits with `EACCES` on `/dev/tty` (gVisor ARM64) | [Docker: gVisor ARM64 TIOCSCTTY](#gvisor-on-arm64-docker-exec--it-does-not-call-tiocsctty) |
| gVisor `container-enhanced` fails on macOS/OrbStack: `cannot read client sync file: EOF` (boot log: `expected to open /tmp, but found /private/tmp`) | [OrbStack: gVisor /tmp virtiofs symlink](#orbstack-gvisor-runsc-fails-to-start-because-tmp-is-a-virtiofs-symlink-to-the-macos-privatetmp) |
| `failed to create an image ... after deleting the existing one: AlreadyExists` (intermittent) | [Docker: AlreadyExists race on rebuild of identical tag](#docker-daemon-races-on-alreadyexists-when-rebuilding-an-existing-tag-with-identical-content) |
| `yoloai system disk`/`doctor` reports absurd reclaimable cache (e.g. podman 129 GiB vs ~5 GiB from `system df`) | [Docker/Podman: Images[].Size includes shared layers](#diskusageimagessize-includes-shared-layers-summing-it-multiply-counts-them) |
| `doctor`/`disk` reports podman images as 0 B despite a multi-GB base | [Podman: /system/df reports LayersSize 0](#podman-systemdf-reports-layerssize-0) |
| `doctor` shows containerd image cache as `?`; `prune --images` leaves devmapper pool blocks used / df unchanged | [containerd: both snapshotters hold a copy](#containerd-both-overlayfs-and-devmapper-snapshotters-hold-a-copy-prune-and-sizing-must-cover-both) |
| Docker base image reads ~33 GiB on Linux vs ~5 GiB on macOS; `image rm` frees ~0; prune undercounts reclaim | [Docker: containerd store pins layers via build cache](#docker-containerd-image-store-image-rm-frees-no-disk-until-the-build-cache-is-pruned-sdk-spacereclaimed-undercounts) |
| `prune` dry-run promises to reclaim "volumes" but reports `reclaimed 0 B`; doctor counts the user's own (non-yoloai) volumes | [Docker/Podman: volume prune is anonymous-only; scope to yoloai volumes](#dockerpodman-volume-prune-default-filter-removes-only-anonymous-volumes-reclaim-accounting-must-be-scoped-to-yoloais-own-volumes) |
| `system prune` finds a different dangling image every run, reclaims 0 B, never converges, even with no builds | [Docker: legacy builder leaves a dangling image per step; build with BuildKit](#docker-legacy-builder-commits-one-dangling-intermediate-image-per-dockerfile-step-build-with-buildkit) |
| `podman: build cache prune failed: Error response from daemon: Not Found` | [Podman: no build-cache endpoint (404)](#podman-docker-compat-api-has-no-build-cache-endpoint--buildcacheprune-returns-404-not-found) |
| `prune --images` on Podman reports absurd reclaim (e.g. 142 GB freed for a ~5 GiB footprint) | [Podman: `ImagesPrune` `SpaceReclaimed` un-dedup sum](#podman-imagesprune-spacereclaimed-is-the-un-deduplicated-image-size-sum) |
| `prune --images` leaves a snapshot chain; `Remove` → `cannot remove snapshot with child` | [containerd: remove snapshots leaf-first](#containerd-snapshots-must-be-removed-leaf-first-children-before-parents-or-removal-silently-stalls) |
| `system disk` reports 0 containerd image bytes right after a successful `system build --backend containerd` | [containerd: import inconsistently materializes snapshots](#containerd-image-import-inconsistently-materializes-overlayfs-snapshots) |
| Base layer won't prune (`cannot remove snapshot with child`) but no snapshot claims it as parent in any namespace | [containerd: leftover lease GC-roots an orphaned child](#containerd-a-leftover-lease-gc-roots-an-orphaned-child-blocking-base-layer-removal) |
| Container starts as root / wrong uid under rootless Podman | [Podman: rootless detection uses socket path](#rootless-detection-must-use-socket-path-not-osgetuid) |
| `yoloai exec`/`attach` on Podman returns exit 125 `no such container` under concurrent load though `info`/`Inspect` shows active | [Docker/Podman: interactive exec must use the API socket](#dockerpodman-interactive-execattach-must-use-the-api-socket-not-the-bare-cli-dual-control-plane-divergence) |
| Wrong uid inside container on macOS Podman | [Podman: macOS keep-id maps VM uid](#macos---usernkeep-id-maps-the-podman-machine-uid-1000-not-the-macos-uid) |
| Rootless Podman privileged: `sudo dockerd` fails, or agent crashes on `prompt.txt` | [Podman: Linux rootless privileged needs keep-id:uid=1001](#linux-rootless-privileged-dind-plain-keep-id-fails-both-ways-use-keep-iduid1001) |
| `creating an ID-mapped copy of layer … no space left on device` on rootless Podman | [Podman: separate ID-mapped image copy per userns mapping](#rootless-podman-keeps-a-separate-id-mapped-image-copy-per-userns-mapping) |
| Podman rejects per-file bind mounts for secrets | [Podman: per-file bind mounts rejected](#per-file-bind-mounts-rejected-by-podmans-docker-compatible-api) |
| Secrets / files missing inside Tart VM | [Tart: VirtioFS directories only](#virtiofs-only-supports-directory-mounts-not-individual-files) |
| Shell command fails with "no such file" on VirtioFS path | [Tart: VirtioFS path has spaces](#virtiofs-mount-path-inside-the-vm-contains-spaces) |
| VM dies when `Start()` context is cancelled | [Tart: tart run needs exec.Command](#tart-run-process-must-use-execcommand-not-execcommandcontext) |
| `mkdir: /var/folders: Permission denied` or `ln: ... Permission denied` during Tart setup | [Tart: mkdir/symlink system dirs fails](#tart-cannot-mkdirsymlink-system-directories-like-varfolders) |
| `tart exec` fails with "instance not found" right after boot | [Tart: exec needs stabilization delay](#tart-exec-needs-brief-stabilization-delay-after-boot) |
| `tart exec` with `--` separator fails silently or returns exit status 1 | [Tart: no support for -- separator](#tart-exec-does-not-support----argument-separator) |
| `yoloai attach` fails with "no sessions" on Tart VM | [Tart: exec -t changes environment](#tart-exec--t-changes-environment-preventing-tmux-from-finding-socket) |
| `xcrun simctl list runtimes` shows no runtimes when mounted via VirtioFS | [Tart: CoreSimulator requires sealed APFS](#coresimulator-cannot-discover-virtiofs-mounted-runtimes) |
| `Failed to start launchd_sim: could not bind to session` when booting simulator | [Tart: ditto'd runtime is incomplete](#dittod-ios-runtime-is-incomplete-use-xcodebuild--downloadplatform) |
| In-VM iOS runtime download is slow; does the cirruslabs Xcode base already include a simulator? | [Tart: cirruslabs Xcode base bakes in the default runtime](#cirruslabsmacos--xcode-base-images-already-bake-in-the-default-ios-runtime--the-in-vm-download-is-redundant-for-it) |
| `iOS X.Y is not installed … install from Xcode > Settings > Components` sporadically on cirruslabs Xcode base | [Tart: cirruslabs Xcode base bakes in the default runtime](#cirruslabsmacos--xcode-base-images-already-bake-in-the-default-ios-runtime--the-in-vm-download-is-redundant-for-it) |
| `git diff` fails with "unable to read" object / git corruption on Tart VM | [Tart: VirtioFS corrupts git repositories](#virtiofs-corrupts-git-repositories) |
| Tart `info` shows `Changes: no` on a dirty sandbox; `destroy` skips the unapplied-work gate | [Tart: host change probe blind to in-VM workdir](#a-host-side-change-probe-is-blind-to-the-in-vm-workdir--info-showed-changes-no-on-a-dirty-tart-sandbox-and-destroy-skipped-its-gate) |
| `yoloai new` times out / "command timed out" on Tart; sandbox.jsonl stops after xcodebuild firstlaunch; agent never starts | [Tart: signal_secrets_consumed deadlock with get_working_dir](#tart-signal_secrets_consumed-must-run-before-get_working_dir) |
| Agent silently fails to start on Tart (claude/node not found) | [Tart: provisioned tool dirs live only on the login PATH](#provisioned-tool-dirs-live-only-on-the-login-path-cirrus-base-image) |
| Swift PM commands fail with sandbox-exec nesting errors on Seatbelt | [Seatbelt: macOS sandbox-exec doesn't nest](#macos-sandbox-exec-doesnt-nest--swift-pm-needs-the-swift-wrapper-sourced) |
| Agent dies silently/SIGTRAP (exit 133) on Seatbelt at launch; ICU/timezone deny in unified log | [Seatbelt: SBPL subpaths need vnode-resolved paths](#agent-dies-silently-sigtrap--sbpl-subpath-rules-must-use-vnode-resolved-paths) |
| Interactive error output "stair-steps" (each line shifts right) on Seatbelt/Tart | [Seatbelt/Tart: local-PTY backends must bridge, not inherit host stdio](#interactive-error-output-stair-steps--local-pty-backends-must-bridge-not-inherit-host-stdio-also-tart) |
| Migrated/upgraded Seatbelt sandbox still hits an already-fixed bug (SIGTRAP, `os.symlink FileExistsError`) on restart | [Seatbelt: regenerate derived artifacts on Start](#seatbelt-derived-artifacts-must-be-regenerated-on-start-not-frozen-at-create) |
| `attach` on a migrated Seatbelt sandbox fails: `error creating .../tmux/tmux.sock (No such file or directory)`, flat path missing `library/` | [Seatbelt: derive tmux socket live, not from frozen config](#host-side-tmux-socket-must-be-derived-live-not-read-from-frozen-runtime-configjson-seatbelt) |
| VS Code tunnel re-prompts for login on every container restart | [VS Code CLI: hostname-based keychain encryption](#vs-code-cli-file-keychain-uses-hostname-in-encryption-key) |
| Second sandbox tunnel loops `error access singleton` forever | [VS Code CLI: singleton lock blocks concurrent tunnels](#vs-code-cli-singleton-lock-blocks-concurrent-tunnels) |
| DNS works but HTTPS to api.anthropic.com times out | [DNS: timeout = API unreachable, not DNS](#request-timed-out-in-claude-code--api-unreachable-not-dns-failure) |
| `iptables` warnings about legacy tables | [iptables-nft: legacy tables warning](#iptables--iptables-nft-both-iptables-legacy-and-iptables-nft-can-coexist) |
| `Can't open socket to ipset` / network isolation fails on Podman macOS | [Podman macOS: iptables-nft lacks xt_set module](#podman-macos-iptables-nft-lacks-xt_set-module-ipset-unusable) |
| Smoke test: `stop_start/containerd-vm` fails with "agent idle for 9s+" | [QEMU: slow startup exceeds stall grace](#qemu-slow-startup-exceeds-smoke-test-stall-grace-period) |
| Smoke test: `stop_start/tart` fails; exchange dir empty | [Tart: xcodebuild -runFirstLaunch blocks agent startup](#tart-xcodebuild--runfirstlaunch-blocks-agent-startup) |
| `yoloai new --attach` hangs after "Sandbox created"; Python setup never completes | [Tart: mount_map uses Docker paths, triggering macOS automount](#tart-mount_map-uses-docker-style-paths-triggering-macos-automount-hang) |
| `FileNotFoundError` at `get_working_dir()` / agent starts in wrong directory | [Tart: workdir setup races Python startup](#tart-vm-workdir-setup-races-python-startup) |
| Tart `:copy`: `yoloai diff` after `restart` reports "No changes" despite agent edits; smoke `stop_start/tart` fails `expected 'output2.txt' … got: No changes` | [Tart: :copy diff after restart shows 'No changes'](#tart-copy-diff-after-restart-shows-no-changes) |
| `yoloai apply` fails: `git add: git [add -A]: exit status 128: … index.lock: File exists` while agent is running | [Docker/Podman: agent git and apply git race on index.lock](#dockerpodman-agent-git-and-apply-git-race-on-indexlock) |
| `FileNotFoundError: 'tmux'` in `sandbox-setup.py::setup_tmux_session` on Tart VM (intermittent) | [Tart: transient FS/PATH failure makes tmux unresolvable during the firstlaunch window](#tart-transient-fspath-failure-makes-tmux-unresolvable-during-the-firstlaunch-window) |
| Smoke test: `stop_start` fails "agent idle"; pane shows `Error: Exit code N` + a clarifying question; other backends pass | [Smoke harness: agent stalls when the sentinel command errors](#agent-stalls-when-the-sentinel-command-errors) |
| `create task: ... more than one sandbox exists with the provided prefix "..."` (containerd-vm, under concurrency) | [Kata: shim resolves sandboxes by name prefix](#kata-shim-resolves-a-sandbox-from-the-container-id-by-prefix-prefix-related-names-collide) |
| Is it safe to delete a `.lock` file while holding its flock? (prune / Destroy) | [Removing a .lock file while holding its flock is safe](#removing-a-lock-file-while-holding-its-flock-is-safe) |
| Tart base build / `tart run` fails with `The number of VMs exceeds the system limit` or VM self-stops at boot, but `tart list` shows nothing running | [Tart: orphaned Virtualization VM processes consume the macOS VM limit](#orphaned-virtualization-vm-processes-survive-a-crashed-tart-run-and-silently-consume-the-macos-vm-limit) |
| `tart delete <name>` fails with `instance not found` for a VM that exists (e.g. `delete old base: instance not found` during base promote) | [Tart: delete of a running VM reports "instance not found"](#tart-delete-of-a-running-vm-fails-with-a-misleading-instance-not-found-stop-first) |
| `system disk` shows tart `IMAGES: ?` / `CACHE: 0 B` despite GBs in `~/.tart`; `prune --images` reports 0 reclaimed | [Tart: list double-counts OCI tag+digest; sizing/prune must dedup](#tart-list-reports-a-pulled-oci-image-twice-tag--digest-over-one-on-disk-copy-sizing-and-prune-must-dedup-and-remove-both-rows) |
| macOS `docker` numbers don't match Docker Desktop assumptions (overlay2/btrfs, classic store) | [Docker on macOS may be OrbStack, not Docker Desktop](#docker-on-macos-may-be-orbstack-not-docker-desktop--docker-info-clientinfocontext-tells-you-which) |
| Podman macOS reports image bytes correctly even though the Linux `LayersSize: 0` workaround exists | [Podman: `/system/df` reports `LayersSize: 0`](#podman-systemdf-reports-layerssize-0) (macOS/version caveat) |
| `system disk` shows seatbelt `IMAGES: ?` / `CACHE: 0 B` — is it a gap? | [Seatbelt has no backend image/cache store](#seatbelt-has-no-backend-imagecache-store--cacheusageprunecache-are-correctly-absent) |

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

### Kata netns warm-up race: `tap0_kata` TC mirred filter not installed when `task.Start` returns

`task.Start()` returns when the VMM has been told to boot, **not** when the
in-netns TC mirred filter that bridges `eth0` ↔ `tap0_kata` is fully in
place. The filter is what carries packets between the host bridge (via
`eth0`/veth) and the VM (via `tap0_kata`). During the gap between
`task.Start()` returning and the filter being installed, the netns has a
default route but outbound packets silently drop — `dns=fail`,
`tcp=fail`, all timeouts with no RST.

**Symptom in the smoke test:** "agent idle 9s+ without sentinel" on
`containerd-vm` / `containerd-vmenhanced`, with the DF5 staged probe
showing `unreachable [dns failed | dns=fail route=ok tcp=fail
https=exit 28]`. Twelve data points (DF8) before the fix landed; retry
always succeeded because the filter caught up within a few seconds.

**Fix (v3):** after `waitForTaskRunning` reports the task Running,
run an in-task probe that verifies the **full outbound chain** —
default-route presence + DNS resolution + TCP connect to
`api.anthropic.com:443`. Retry every 500ms for up to 30s.
Best-effort: on persistent failure it logs a warning and proceeds
rather than blocking Start. See `lifecycle.go::waitForNetworkReady`.

**Why this probe shape — three iterations:**

- **V1 (insufficient): gateway:22 RST = success.** The TC mirred
  filter (eth0 ↔ tap0_kata) installs *before* host-side MASQUERADE
  is ready, so a gateway probe gets RST early and declares ready
  while external traffic still drops. Two distinct stages were
  collapsing into one.
- **V2 (insufficient): DNS + external TCP, but fast-exit on missing
  default route.** Right target, wrong policy: `ip route show
  default` can be empty during a transient setup window before CNI
  fully wires the netns. V2 treated that as "network=none → ready",
  so the probe returned in <100ms before the route was even
  installed. Failures looked identical to V1.
- **V3 (current): same DNS+TCP target, retry on missing-route too.**
  Since cni.go::setupCNI is unconditional for the containerd backend,
  missing-route is always transient. The probe retries until
  default-route + DNS + TCP all succeed, or the 30s budget exhausts.

If the containerd backend ever honors `NetworkMode == "none"`, the
probe will loop 30s and warn — acceptable for that edge case, but
worth revisiting at that point.

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

### Kata shim wedge with dead VM: SIGKILL via containerd doesn't release the task

**Symptom:** `yoloai destroy <name>` hangs indefinitely; or after a crashed
run, `sudo ctr --namespace yoloai tasks ls` reports `RUNNING` containerd
tasks while `ps aux | grep -E "qemu|firecracker"` returns 0 — the VM
underneath the shim is already dead. Host CPU sits at 60–80% (the wedged
shims spin on vsock recv calls that never return). The matching shim
processes are sleeping (`S` state) when inspected via `/proc/<pid>/status`.

**Why:** the Kata shim is stuck inside a vsock read to a kata-agent that
died with its VM. `task.Kill(SIGKILL)` sends the signal through
containerd's gRPC API, which the shim still answers — but the shim then
delivers the signal *into the VM* via vsock, and the VM is gone. The
shim's own process never receives the signal. `task.Wait()`'s exit
channel never fires.

**Fix in code:** `lifecycle.go::stopTaskWithEscalation` runs the
SIGTERM → SIGKILL ladder with bounded timeouts, then escalates to the
direct-PID escape hatch — `killStaleKataShims` walks `/proc` for the
matching `containerd-shim-kata-v2 -id <name>` and sends `SIGKILL`
directly to the shim's host PID. After that, `removeKataStateDir` clears
the `/run/kata/<name>/` and TTRPC socket residue. Logs a WARN event
(`event=containerd.stop.escalation`) so the user sees what was forced.

**Fix for the user:** never required for new sandboxes — the library
handles it automatically. Pre-existing leaks from a build before this
fix: `yoloai system prune` (which now uses the same escalation), or
`yoloai system doctor` to enumerate orphan state first.

Cross-references: `clearStaleContainerState` uses the same escalation
so a `yoloai new <name>` against a wedged orphan with the same name
auto-recovers.

### Tart VM process wedge: `tart stop` and SIGTERM via pgrep don't release the host `tart run`

**Symptom:** `yoloai destroy <name>` against a Tart sandbox hangs;
`tart list` shows the VM as running but the VM is unreachable (the
guest OS hung, or Virtualization.framework is wedged). Host `tart run
<name>` PID is still alive after a normal stop attempt.

**Why:** parallel to the Kata wedge above, with a different intermediary.
`tart stop` sends a shutdown request through Virtualization.framework
into the guest — if the guest kernel hangs or the framework call
blocks on hardware, the shutdown never confirms. The earlier
`stopVM` fallback also only sent SIGTERM via `pgrep -f "tart run.*<name>"`,
which a process stuck in a system call may queue but never act on.

**Fix in code:** `tart.go::stopVM` runs a three-step ladder:

  1. `tart stop <name>` bounded by a 10s context timeout
     (`tartGracefulStopTimeout`). A wedged VM can't hold the caller
     beyond that.
  2. SIGTERM to every host PID matching `tart run.*<name>` from pgrep,
     then `waitForExit` polls `syscall.Kill(pid, 0)` for up to 5s
     (`tartSigtermWait`).
  3. Survivors get SIGKILL via `proc.Kill()`. Logged at WARN
     (`event=tart.stop.escalation`) so the user sees that yoloai had
     to force-kill a stuck `tart run`.

This applies to both `Stop()` and the `Create()` pre-clear path
(line 198: `r.stopVM` before `tart delete`), so `yoloai new <name>`
against a wedged Tart orphan auto-recovers the same way the
containerd path does.

**Fix for the user:** as with Kata, none required — the library handles
it. The same `yoloai system prune` / `yoloai system doctor` surface
applies (Tart's `Prune()` enumerates `yoloai-*` VMs via `tart list`
and calls `stopVM + delete` per orphan).

### Orphaned Virtualization VM processes survive a crashed `tart run` and silently consume the macOS VM limit

**Symptom:** `yoloai new` / `yoloai system build` on Tart fails to boot —
the VM "self-stops" at boot (`tart run` exits cleanly, log shows
`Stopping VM...`), or you hit `The number of VMs exceeds the system
limit` — yet `tart list` shows **no VM running**. The host has been up a
while and/or had a crashed/SIGKILL'd build earlier.

**Why:** each running VM is backed by a `com.apple.Virtualization.VirtualMachine`
XPC process. When its launcher (`tart run`) is SIGKILL'd or the parent
process dies, that XPC process can **survive, reparented to launchd
(PPID 1)**. The VM keeps running at the framework level and keeps holding
a macOS VM slot — but `tart list` reads tart's own state, which no longer
knows about it, so the VM is invisible there. Apple's
Virtualization.framework caps **macOS guests at 2 concurrent VMs**; two
such orphans exhaust the limit and block all new VMs. The count only
resets when the orphan is killed or the host reboots (`tart stop` can't
touch a VM tart no longer tracks).

**Gotcha — the XPC process is shared across apps:** `com.apple.Virtualization.VirtualMachine`
is used by *every* app built on Virtualization.framework, not just tart —
e.g. **Claude.app** runs its own `claudevm.bundle` microVM, and **Podman's
`applehv` machine** (`podman-machine-default`) is itself a Virtualization.framework
VM. You cannot assume such a process is a tart VM, and you must **never**
kill one blindly — killing another app's VM breaks that app (a hand-killed
orphan in an early build of this feature took down the podman machine
mid-session, before the disk-image check below existed). The reliable
discriminator: a tart VM holds a `~/.tart/vms/<name>/disk.img` open
(visible via `lsof -p <pid>`); foreign VMs don't. Foreign VMs are also
typically Linux guests, which are **not** subject to the macOS 2-VM limit.

**Fix in code:** `tart/census.go` enumerates the XPC processes
(`detectVMProcesses`), keeps only those positively identified as tart
(open `.tart/vms/` disk), and classifies each as an owned sandbox (a live
`tart run <name>` launcher exists) or a killable orphan. Surfaced through
`yoloai doctor` as the "VM slots" section: it lists every tart slot,
exits non-zero when the limit is reached (a functional block on new
sandboxes), and prints the `kill <pid>` to reclaim an orphan. doctor
**reports only** — it never kills.

**Fix for the user:** `yoloai doctor` shows which PIDs to kill; `kill
<pid>` frees the slot, or reboot to reset the count. Killing a tart
orphan is safe (the VM is already untracked); doctor will not point you
at a non-tart VM.

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

**Production-confirmed signature, mechanism uncertain.** Smoke run
`yoloai-smoketest-20260526-175645.907` captured POSTROUTING masquerade
for `10.89.1.90` present (bridge ran), CNI-FORWARD ACCEPT rules absent.
At the time this was attributed to the upstream `addRules()` no-op. On
2026-05-26 a different bug — [DF10: Go OS thread netns leak](#go-os-thread-netns-leak-from-netnsnewnamed--netnsset-without-runtimelockosthread)
— was root-caused in our own code and **also produces exactly this
signature** (firewall plugin running on a netns-poisoned thread writes
CNI-FORWARD into the wrong netns). Every observed "DF9" smoke failure
was equally explainable by DF10, and the symptom disappeared after
fixing DF10. The upstream pathology described above is a real code path
but was **not independently confirmed** in our environment; treat it as
"defense-in-depth scenario" rather than "known active bug" until a
post-DF10 recurrence is captured.

**Mitigation in `cni.go` (revision 2, kept as defense-in-depth):**
`runCNIAdd` catches **two** variants and treats both as the same retry
signal `errFirewallRulesMissing`:
1. `result.Interfaces["eth0"].IPConfigs` is empty after `n.Setup`.
2. IPConfigs is populated but `iptables -S CNI-FORWARD` lacks an `ACCEPT`
   line for `<ip>/32` — `verifyCNIForwardRules` returns the sentinel.

On either, `runCNIAdd` runs `n.Remove` to undo any partial bridge state
before returning the sentinel. `setupCNI` then catches the sentinel via
`errors.Is`, recreates the netns, and retries CNI ADD **once**. The
retry emits `sandbox.network.firewall_retry` warn log; a failed
`n.Remove` emits `sandbox.network.firewall_rollback_failed`. Both are
defense-in-depth signals — if either fires in production after the DF10
fix, capture iptables + thread state before destroying the sandbox.

### Go OS thread netns leak from `netns.NewNamed` / `netns.Set` without `runtime.LockOSThread`

vishvananda/netns's `NewNamed`, `New`, and `Set` all operate via
`unshare(CLONE_NEWNET)` or `setns(2)` on the **current OS thread**.
After the call, only that one thread is in the new netns — the
goroutine on it inherits the netns, but the rest of the Go runtime's
threads are unaffected.

If you call any of these without `runtime.LockOSThread()` (and a
restore-to-origNS before `UnlockOSThread`), the goroutine can be
scheduled off the modified thread, and the thread goes back to Go's
pool **still in the wrong netns**. Any later goroutine that lands on
that thread inherits the wrong netns. This includes `exec.Command`
forks — the child inherits the netns of the parent thread at fork
time. libcni's plugin invocation path does exactly that, so the bridge
or firewall plugin can run in the wrong netns and write iptables rules
to a namespace the host can't see.

**Root cause of DF10 (fixed 2026-05-26):** `runtime/containerd/containerd.go::canCreateNetNS`
called `netns.NewNamed` + `DeleteNamed` with no `LockOSThread` and no
restore. The probe is invoked on every containerd-backend `new`,
leaking one runtime thread into an anonymous netns each time.
Reproduction: 20-iteration loop of `yoloai new --agent test
--isolation vm` failed ~20% pre-fix, 0/20 post-fix.

**Rule of thumb:** every callsite that uses `netns.New`,
`netns.NewNamed`, or `netns.Set` must look exactly like
`createNetNS` — Lock, save origNS, do the work, Set(origNS), defer
Unlock. Grepping `netns\.\(New\|Set\)` in any future containerd
backend code should turn up nothing except a function that follows
that pattern.

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

## Docker

### AppArmor blocks `mount(2)` even with `CAP_SYS_ADMIN`

Docker's default AppArmor profile blocks `mount()` syscalls even when
`CAP_SYS_ADMIN` is granted via `CapAdd`. Without explicitly disabling AppArmor,
the entrypoint cannot mount overlayfs inside the container and gets `EPERM`.

Workaround: add `security-opt apparmor=unconfined` whenever `SYS_ADMIN` appears
in `CapAdd`. See `docker.go::Create`. This is not advisory — the mount literally
fails otherwise.

### `/proc/sys` and `/sys/fs/cgroup` are read-only without `systempaths=unconfined`

**Symptom:** Starting an inner Docker daemon inside a `container-privileged` sandbox fails with:
```
sysctl: permission denied on key "net.ipv4.ip_forward"
```
or containers launched by the inner daemon fail with:
```
mkdir /sys/fs/cgroup/docker: read-only file system
```

**Explanation:** Docker bind-mounts `/proc/sys` and `/sys` (including `/sys/fs/cgroup`) as
read-only in all containers unless `--privileged` or `--security-opt systempaths=unconfined`
is used. An inner Docker daemon needs to write `net.ipv4.ip_forward` via `/proc/sys` and
create cgroup subtrees under `/sys/fs/cgroup` for every container it runs.

**Fix:** Use `container-privileged` (`--privileged`) for Docker-in-Docker workloads. Privileged
mode makes `/proc/sys` and `/sys/fs/cgroup` writable and works on all Docker versions.
`systempaths=unconfined` achieves the same with a narrower capability grant but requires
Docker ≥ 20.10 and is rejected by older daemons with `invalid --security-opt`.

**Code:** `sandbox/create_instance.go` case `container-privileged`.

---

### Docker SDK `Exec` strips the trailing newline

`ContainerExecAttach` + `stdcopy.StdCopy` output is fed through
`strings.TrimSpace`, which removes the trailing newline from `git diff` output.
`git apply` requires a trailing newline to parse patches; without it, the patch
is silently rejected or applies incorrectly.

Workaround: re-append `\n` to the patch bytes if the last byte is not `\n`
before calling `git apply`. See `Fix: restore trailing newline in overlay patch
output` (commit f9bf669).

### Docker daemon races on AlreadyExists when rebuilding an existing tag with identical content

**Symptom:** `make releasetest` / `make integration` intermittently fails with:
```
docker build: failed to create an image docker.io/library/yoloai-base:latest with target sha256:<id>
  after deleting the existing one: AlreadyExists: image "docker.io/library/yoloai-base:latest": already exists
```
Re-running the same command without code changes succeeds.

**Explanation:** When BuildKit finalizes an image whose computed SHA matches the existing tag (byte-identical build inputs), it deletes the old reference and re-tags. The daemon's image-store (especially with the containerd snapshotter enabled, which is the default on recent Docker versions) reports `delete` complete before the reference is fully released. The immediate `create` then sees the old entry and fails with `AlreadyExists`. The race window is small (typically a few ms) and depends on snapshotter, daemon version, and load.

**Triggers it reliably in tests:**
- `make integration` first runs `make base-image` (populates the daemon's image), then test code with a fresh `HOME=tmpdir` calls `EnsureSetup` → the new HOME has no `~/.yoloai/cache/.base-image-checksum`, so `NeedsBuild()` returns true → docker SDK rebuilds the exact same content under the exact same tag → race.

**Fix in test code:** pre-seed the checksum in the per-test HOME immediately after `HOME` is overridden:
```go
os.MkdirAll(layout.CacheDir(), 0750)
dockerrt.RecordBuildChecksum(layout, "")
```
`RecordBuildChecksum` writes `~/.yoloai/cache/.base-image-checksum` using the binary's current build-inputs hash; on the next `NeedsBuild()` call the existing image is judged fresh and no rebuild is attempted.

**Apply at EVERY fresh-HOME site, not just `TestMain`.** Each per-test `cliSetup` / `integrationSetup` / `e2eSetup` helper calls `t.TempDir()` for its own isolated HOME — those new HOMEs don't carry the `TestMain` seed, so the first test in the suite re-triggers the rebuild race even when `TestMain` already pre-seeded. In the e2e suite the failure mode is more severe: the binary runs as a subprocess and a wedged Docker SDK HTTP transport hangs the subprocess indefinitely (test has no per-call timeout, only the 15-minute suite timeout). The subprocess inherits `HOME` from the test process via `t.Setenv`, so writing the checksum in the test process is visible to the subprocess. Applied at:

- `internal/sandbox/integration_main_test.go:TestMain` (binary bootstrap)
- `internal/sandbox/integration_helpers_test.go::integrationSetup` (per-test)
- `internal/cli/integration_main_test.go:TestMain` (binary bootstrap)
- `internal/cli/integration_test.go::cliSetup` (per-test)
- `test/e2e/helpers_test.go::e2eSetup` (per-test, subprocess-visible)

**Workaround for users hitting it interactively:** re-run the command, or delete `~/.yoloai/cache/.base-image-checksum` and let yoloai rebuild from scratch (which produces a fresh SHA when source changed, or trips the race again if not).

**Code:** `runtime/docker/build.go::Setup`, `runtime/docker/build.go::buildBaseImage`.

---

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

### OrbStack: gVisor (`runsc`) fails to start because `/tmp` is a virtiofs symlink to the macOS `/private/tmp`

**Symptom:** `--isolation container-enhanced` on macOS under **OrbStack** fails at container start with the opaque:

```
OCI runtime create failed: creating container: cannot create sandbox:
cannot read client sync file: waiting for sandbox to start: EOF
```

The real cause is only in the runsc boot log (`/tmp/runsc/runsc.log.*.boot.txt`):

```
FATAL ERROR: error setting up chroot: error mounting tmpfs in chroot:
failed to safely mount: expected to open /tmp, but found /private/tmp
```

**Explanation:** gVisor's sentry sets up its sandbox chroot under a hard-coded `/tmp` and runs a mount-safety check that the resolved path matches. In the OrbStack Linux VM, `/tmp` is a **symlink to `/private/tmp`**, and `/private` is the **macOS host mounted into the VM over virtiofs** (`mac on /private type virtiofs`). The symlink makes the safety check see `/private/tmp`, so runsc aborts before the sandbox starts. This is **OrbStack-specific** — Docker Desktop's LinuxKit VM has a normal tmpfs `/tmp` and is unaffected. It is *not* an arm64 or macOS limitation: with the chroot bypassed, gVisor boots fine on Apple Silicon (`Linux version 4.19.0-gvisor`, `systrap` platform) and Claude Code runs without the old #35454 hang.

**Fix / workarounds (none wired into yoloai yet):**
- **Docker Desktop instead of OrbStack** — normal `/tmp`, works once `runsc` is installed in the VM.
- Make `/tmp` a real directory in the OrbStack VM (replacing the `/private/tmp` symlink) — but that breaks OrbStack's macOS `/tmp` sharing, so it's not a safe default.
- `--TESTONLY-unsafe-nonroot` in the runtime's `runtimeArgs` skips the chroot (disables a security boundary; debugging only).

**Code pointer:** the macOS prerequisite check now relies on daemon registration (`docker.go::RequiredCapabilities` returns `gvisorRegistered` only off-Linux); the chroot collision is purely a VM filesystem-layout issue, surfaced at `runtime.New`/`Start`, not something yoloai's checks can detect ahead of time.

### Docker-in-Docker: nested `fuse-overlayfs` can't exec on Docker Desktop / Podman Machine (macOS) — RESOLVED via overlay2 + real-fs volume

**Symptom (pre-fix):** under `--isolation container-privileged` on macOS Docker Desktop, a nested `dockerd` configured for `fuse-overlayfs` + `docker run hello-world` pulled the image fine then died with:

```
exec /hello: invalid argument
```

Every nested container hit it — `alpine echo`, `busybox uname`, `hello-world` all failed identically with `EINVAL` on `execve`. **Not** arch-related (arm64-on-arm64; failed even with `--platform linux/arm64`).

**Explanation:** native `overlay2` can't nest on the container's overlay rootfs (`driver not supported: overlay2`), so yoloai used to pin `fuse-overlayfs`. But whether a process can *exec* a binary on a `fuse-overlayfs` mount depends on the **host VM's kernel**: **OrbStack**, **Podman Machine** (Fedora), and native **Linux** can; **macOS Docker Desktop**'s **LinuxKit** kernel cannot — `execve` returns `EINVAL`, on both overlay and real-fs backings (so the backing fs isn't the issue; the FUSE-exec path is). Verified cross-platform in `docs/contributors/design/research/dind-storage-drivers.md`.

**Fix (current):** yoloai mounts a managed **real-filesystem named volume at `/var/lib/docker`** for every privileged sandbox (`docker.go` `ensureDindVolumeMount`). On a real-fs backing the nested daemon **auto-selects the native overlay driver** — no FUSE, so the LinuxKit exec limitation never applies — and the daemon.json pin is gone, so both `start_dockerd` and a manual `sudo dockerd &` get it. Verified working end-to-end on Docker Desktop (ext4 → overlay2), OrbStack (btrfs), Podman Machine (xfs → fuse-overlayfs, which execs fine there), and Linux. `start_dockerd` keeps a fuse-overlayfs fallback only when the backing is still `overlay` (i.e. the volume is somehow absent). `vfs` also works as a manual escape hatch but is slow/disk-heavy; not used.

The earlier stopgaps — a `system check` advisory and a smoke `dind` N/A-reclassification — were **removed** once the real fix landed (dind now works on every provider, so they'd be misleading).

**Code pointer:** `internal/runtime/docker/docker.go` — `ensureDindVolumeMount` / `dockerLibVolumeName` (Create mounts it, Remove reclaims it). `internal/runtime/docker/resources/Dockerfile` — daemon.json pin removed. `internal/runtime/monitor/setup_helpers.py` `dockerd_storage_args` + `sandbox-setup.py` `start_dockerd` (fstype probe). Reproduce the *old* failure with `docker run --rm --privileged --entrypoint bash yoloai-base -c 'echo {} | sudo tee /etc/docker/daemon.json; sudo dockerd --storage-driver=fuse-overlayfs & … sudo docker run --rm hello-world'` on Docker Desktop.

### gVisor netstack ignores in-sandbox iptables rules

**Symptom:** A sandbox created with `--isolation container-enhanced` (gVisor / runsc) and `--network-isolated` appears to apply the deny-by-default rules in its startup log (`network.isolate iptables default-deny applied`), but outbound traffic to non-allowlisted destinations is **not** blocked. Egress to any IP succeeds.

**Explanation:** gVisor implements its own userspace network stack (the "Sentry"). The `iptables` command inside a runsc sandbox writes rules into a guest-only table that gVisor's netstack does not consult. The host kernel never sees those rules — outbound packets traverse the host veth and exit normally. The Linux netfilter machinery that `entrypoint.py::isolate_network` relies on is bypassed entirely.

This applies to both backends that can load runsc:
- `docker` with `--isolation container-enhanced`
- `podman` with `--isolation container-enhanced`

Standard runc (`--isolation container`, `--isolation container-privileged`) is unaffected because the host kernel evaluates iptables in the container's netns. Kata-based isolation modes (`vm`, `vm-enhanced`) are unaffected because the guest Linux kernel inside the VM evaluates iptables exactly like bare metal.

The entrypoint loud-failure fix (`NetworkIsolationError`) catches *some* gVisor failures incidentally — gVisor's iptables emulation rejects `-m set --match-set`, so the ipset-backed allowlist rule fails at container start, taking the sandbox down. That's accidental and brittle: future gVisor versions may accept the rule without enforcing it, putting us back in silent-no-op territory.

**Fix:** Reject the combination at sandbox creation, before the container is built. `runtime.IsolationEnforcesInSandboxIptables(isolation)` returns false for `container-enhanced`; `sandbox/create_instance.go::buildInstanceConfig` checks this when `state.networkMode == "isolated"` and returns an explicit error pointing the user at the working isolation modes.

**Permanent fix:** The redesign in [`docs/contributors/design/network-isolation.md`](design/network-isolation.md) moves enforcement to the host netns, where gVisor's netstack is irrelevant — packets leaving the gVisor sandbox traverse the host veth and hit the host iptables rules like any other backend. Until that lands, the combination is rejected.

**Code:** `runtime/isolation.go::IsolationEnforcesInSandboxIptables`, `sandbox/create_instance.go::buildInstanceConfig`

### Proxmox LXC seccomp survives `seccomp=unconfined` at the Docker layer

**Symptom:** Inside a `container-privileged` sandbox on a Proxmox LXC host, `cat /proc/self/status` shows:
```
Seccomp: 2
Seccomp_filters: 1
```
despite yoloai correctly setting `seccomp=unconfined` on the Docker container. Rootless Docker and rootless Podman both fail when they try to mount proc inside a user namespace:
```
runc create failed: unable to start container process: error mounting "proc" to rootfs at "/proc": operation not permitted
```
or:
```
crun: open /proc/sys/net/ipv4/ping_group_range: Read-only file system
```
Confirmed by: `unshare --user --map-root-user --mount --fork sh -c 'mount -t proc proc /proc'` returning `permission denied`.

**Explanation:** When Docker/containerd itself runs inside an unprivileged Proxmox LXC container with `features: nesting=1`, Proxmox applies its own nesting seccomp profile to the LXC container. That filter sits below the Docker layer and cannot be removed by `seccomp=unconfined` at the Docker level — seccomp filters stack and can only be restricted, never relaxed, by child processes. The nesting profile allows most syscalls but blocks `mount(2)` with proc/sysfs types inside user namespaces, which is exactly what rootlesskit (Docker rootless) and crun (rootless Podman) require.

**Workaround (host):** On the Proxmox host, add to `/etc/pve/lxc/<ctid>.conf`:
```
lxc.seccomp.profile:
```
An empty value disables LXC seccomp for that container entirely. The container must be stopped and restarted. This is appropriate for a trusted dev workstation LXC container.

**Impact on yoloai:** Rootless Docker silently fails inside `container-privileged` sandboxes on Proxmox LXC hosts even though yoloai's configuration is correct. Rootful Docker works because it does not use a user namespace.

**Code:** `sandbox/create_instance.go` — the seccomp setting is correct; the failure is environmental.

---

### `DiskUsage().Images[].Size` includes shared layers; summing it multiply-counts them

**Symptom:** `yoloai system disk` / `yoloai doctor` reports an absurd reclaimable cache — e.g. podman at **129.74 GiB** when `podman system df` says only ~5 GiB. The inflation scales with the number of images: dozens of intermediate base-build stages each "weigh" ~5 GiB in the report.

**Explanation:** The Docker/Podman SDK's `client.DiskUsage()` returns each `image.Summary.Size` as that image's *total* size **including layers it shares with other images**. yoloai's base build leaves many `<none>` intermediate stages that all share one ~5 GiB base, so summing `img.Size` across them counts the shared layers once per image. `docker/podman system df` does not do this — its images SIZE column is the deduplicated layer-store total, which the SDK exposes separately as `types.DiskUsage.LayersSize`.

**Fix:** Use `du.LayersSize` for the image portion of the cache total; add container `SizeRw`, volume `UsageData.Size`, and build-cache `Size` on top (those live outside the image layer store and are not deduplicated against it). Never sum `du.Images[].Size`.

**Code:** `internal/runtime/docker/prune.go` `splitCacheBytes()` (shared by docker + podman; returns the no-rebuild `cached` total and the rebuild-forcing `images` total separately). Guard test: `internal/runtime/docker/prune_test.go::TestSplitCacheBytes_ImagesUseDeduplicatedLayersSize`.

**Related (Podman):** the `du.LayersSize` fix above silently fails on Podman, whose docker-compat `/system/df` returns `LayersSize: 0`. The Podman backend injects a per-image dedup instead — see [Podman: `/system/df` reports `LayersSize: 0`](#podman-systemdf-reports-layerssize-0).

**Related (display):** containerd now sizes its image cache via the snapshot `Usage` API (see [containerd: both snapshotters hold a copy](#containerd-both-overlayfs-and-devmapper-snapshotters-hold-a-copy-prune-and-sizing-must-cover-both)); `ImageBytes == -1` remains only as an error fallback when listing images fails, and the `<= 0` filter in `internal/cli/doctorcmd/doctor.go` `renderReclaimTier` still guards it (a `-1` would otherwise render as a literal `-1 B` and skew the total).

---

### Docker containerd image store: `image rm` frees no disk until the build cache is pruned; SDK `SpaceReclaimed` undercounts

**Symptom:** On Linux Docker with the containerd image store enabled (`features.containerd-snapshotter`), `yoloai system disk` reports the docker backend consuming far more than the image's apparent size — e.g. **33.66 GiB** for a base image that occupies ~5 GiB on macOS Docker Desktop (classic store). `docker image rm <id>` reports success but frees ~0 bytes on disk. After `docker builder prune -af`, the same `image rm`/`image prune` suddenly frees ~20 GiB even though the SDK's `ImagesPrune.SpaceReclaimed` reported only ~5.9 GiB.

**Explanation:** With the containerd snapshotter, BuildKit's build cache holds references to the image layers it produced. While those cache records exist, the layers are pinned: removing the image record drops the tag but containerd's GC can't reclaim the still-referenced content blobs/snapshots. Pruning the build cache releases the references, and only then does layer removal actually return disk. Separately, the SDK's `SpaceReclaimed` field counts only the content it directly deleted in that call, not the cascading snapshot/blob GC that follows — so it undercounts real reclaim by ~4x. The classic (non-containerd) store on macOS Docker Desktop doesn't exhibit either behavior, which is why the same base image reads ~5 GiB there.

**Fix:** Prune the build cache *before* (or in the same pass as) image removal so layers actually free. yoloai's plain `prune` does `BuildCachePrune(all=true)` + `VolumesPrune` + dangling `ImagesPrune` (no rebuild forced); `--images` adds full image removal. Because `SpaceReclaimed` is unreliable (it undercounts here, and *over*counts on Podman — see [Podman: `ImagesPrune` `SpaceReclaimed` is the un-deduplicated image-size sum](#podman-imagesprune-spacereclaimed-is-the-un-deduplicated-image-size-sum)), the reclaimed total is **not** taken from `SpaceReclaimed`. It is the drop in this backend's own `CacheUsage` across the prune (`before − after`), which reuses the already-accurate sizing and is self-attributed per backend (an earlier `statfs` free-space delta was abandoned because, on a shared `/`, one backend's delta absorbs bytes freed by another's prune — see working-notes D37).

**Note on logical vs physical:** because `CacheUsage` counts build cache and image layers separately but they *share* content on the containerd store, the reported reclaim is a *logical* figure that can exceed the physical bytes `df` shows freed. That gap is expected and documented (D37), not a bug.

**Code:** `internal/runtime/docker/prune.go` — `PruneCache` (prune order + before/after delta), `reclaimableBytes` (the `CacheUsage` sample), `splitCacheBytes` (build cache counted as no-rebuild `cached`, `LayersSize` as rebuild-forcing `images`).

---

### Docker on macOS may be OrbStack, not Docker Desktop — `docker info` `.ClientInfo.Context` tells you which

**Symptom:** macOS disk-reporting verification assumed Docker Desktop's LinuxKit VM (classic image store, data root hidden inside the VM). On a dev machine the `docker` CLI was actually talking to **OrbStack** (`docker info` → `Context: orbstack`), which is a different LinuxKit-style VM with `Storage Driver: overlay2` on a `btrfs` backing filesystem, `containerd-snapshotter` **off** (classic store), and `Default Runtime: runc`.

**Why it matters / what we verified (2026-05-29, Docker 29.4.0 via OrbStack):** the socket/API-only sizing path is store- and VM-agnostic, so it Just Works regardless of which macOS Docker you run. `yoloai system disk` reported docker `image_bytes = 5023481654` (4.68 GiB) — **byte-exact** against `docker system df` Images SIZE `5.023GB` — and `cached_bytes = 507954634` (484.4 MiB) matching Local Volumes `508MB`. Because OrbStack uses the **classic** store (not the containerd snapshotter), the [`image rm` frees no disk until build cache pruned](#docker-containerd-image-store-image-rm-frees-no-disk-until-the-build-cache-is-pruned-sdk-spacereclaimed-undercounts) pinning behavior does **not** apply, and the logical-vs-physical reclaim gap collapses (logical ≈ physical). No code change needed; the takeaway is to **check `docker info` for the active context/store before comparing numbers** — "macOS Docker" is not necessarily Docker Desktop.

**Code:** none (verification only). Sizing path: `internal/runtime/docker/prune.go` `CacheUsage`/`splitCacheBytes`.

---

### The Docker Go SDK ignores `docker context`; `client.FromEnv` honors only `DOCKER_HOST`

**Symptom:** After `docker context use desktop-linux` the `docker` CLI works, but yoloai fails with `docker daemon is not responding`. Root cause: `/var/run/docker.sock` is a symlink to a *stopped* provider's socket (e.g. `~/.orbstack/run/docker.sock` after switching OrbStack → Docker Desktop), and the Go SDK kept dialing it.

**Explanation:** `dockerclient.FromEnv` reads `DOCKER_HOST`/`DOCKER_CERT_PATH`/`DOCKER_API_VERSION` and otherwise falls back to the built-in default socket. Unlike the `docker` CLI, it does **not** consult `~/.docker/config.json` `currentContext` or the `~/.docker/contexts/meta/<sha256(name)>/meta.json` endpoint store. So `docker context use` retargets the CLI but not any SDK-based tool — they diverge whenever the default socket is stale.

**Fix:** `resolveDockerHost` mirrors the CLI's precedence sourced from the threaded env (§12): `DOCKER_HOST` → active context (`DOCKER_CONTEXT` env, else config.json `currentContext`) endpoint → "" (SDK default). Any parse/read failure degrades to "". As a self-heal for the stale-symlink case with no context switch, when the resolved socket fails `Ping` the auto path probes well-known local sockets (`/var/run`, Docker Desktop, OrbStack, Colima, Rancher Desktop) and adopts the first that answers, printing a one-line stderr notice. An explicitly pinned host (the podman backend) bypasses both. `probe` was widened to match (context endpoint or any existing well-known socket counts as available).

**Code:** `internal/runtime/docker/dockerhost.go` — `resolveDockerHost`, `wellKnownDockerSockets`, `sockExists`; `internal/runtime/docker/docker.go` — `NewWithSocket` (`dialDocker`/`dialFirstAlive` fallback), `probe`.

---

### Docker/Podman: `volume prune` (default filter) removes only *anonymous* volumes; reclaim accounting must be scoped to yoloai's own volumes

**Symptom:** `yoloai doctor`/`system disk` report a large reclaimable "cached" figure (e.g. 484.4 MiB) that is actually the user's **named** volumes — `docker volume ls` shows things like `foley_postgres-data` (a compose database) and `vscode`, which have nothing to do with yoloai. `yoloai system prune` dry-run promises to remove them ("would remove unused volumes …"), then the real prune reports `reclaimed 0 B` because nothing was freed.

**Explanation:** Two compounding problems. (1) Since Docker 23, `docker volume prune` / the SDK `VolumesPrune` with default filters removes only **anonymous** volumes — named volumes survive unless `all=true` is set. So the dry-run estimate (which summed *every* volume's size) over-promised relative to what the prune could remove. (2) More fundamentally, yoloai **creates no Docker volumes at all**, so counting *any* host volume as yoloai-reclaimable is wrong — and threatening to delete the user's database volume is dangerous. The only reason the DB survived was the anonymous-only quirk masking the over-promise. (See also the OrbStack verification note above, which observed `cached_bytes` == the 508MB Local Volumes and mistook it for legitimate yoloai cache.)

**Fix:** Scope both the estimate and the prune to volumes carrying the `com.yoloai.managed` label. `splitCacheBytes` counts only labeled volumes; `PruneCache` calls `VolumesPrune` with `label=com.yoloai.managed` + `all=true` (so named yoloai volumes are removed, not just anonymous ones). yoloai creates no volumes today, so this currently reclaims nothing and reports nothing for volumes — correct. Any future code that creates a volume MUST stamp it with `managedLabel`.

**Podman caveat:** Podman's docker-compat API does **not** accept the `all` volume filter — passing it fails the whole prune with `failed to parse filters for all=true&label=…: "all" is an invalid volume filter`, surfacing as `podman: volumes prune failed: …`. Podman has no anonymous-vs-named distinction (`podman volume prune` removes every unused volume by default), so the `all=true` arg is unnecessary there. `PruneCache` therefore omits it when `binaryName == "podman"` and sends only the `label` filter.

**Code:** `internal/runtime/docker/prune.go` — `managedLabel` const, `splitCacheBytes` (label-gated volume loop), `PruneCache` (label+all `VolumesPrune` filter, `all` omitted for Podman).

---

### Docker: legacy builder commits one dangling intermediate image per Dockerfile step — build with BuildKit

**Symptom:** `yoloai system prune` finds a *different* `<none>` (dangling) image on every run — `Orphaned resources: image faf2f314ca62`, then `8e3deeacf8ac`, etc. — even when no sandboxes are running and nothing has been built. Each removal reports `reclaimed 0 B` and the cycle never ends. `docker images -a --filter dangling=true` shows dozens of `<none>` images, all the same size (e.g. 6.63 GB), all created at the same time. Their IDs match the layers in `docker history yoloai-base`.

**Explanation:** The moby Go SDK's `client.ImageBuild` runs the **legacy builder**, not BuildKit (the `#(nop) COPY …` history format is the tell). The legacy builder commits a separate image per Dockerfile instruction. On the **containerd image store** (Docker Desktop 23+ default) those per-step images are untagged manifests that form the parent chain of the tagged `yoloai-base` and surface as dangling. Without `-a`, the daemon reports only the current *leaf* of that untagged chain — exactly one image — so `pruneDanglingImages` removes one per run; removing it frees nothing (blobs are shared with the live tag) and exposes the next intermediate as the new leaf. The result is an N-deep peel that regenerates on every base rebuild and quietly destroys the layer cache rebuilds would reuse. (BuildKit, by contrast, keeps step results in the *build cache* — pruned by `BuildCachePrune` — not as images, so no dangling intermediates exist.)

**Fix:** Build `yoloai-base` via BuildKit by shelling out to `<binary> build -` (context tar piped to stdin) with `DOCKER_BUILDKIT=1`, instead of `client.ImageBuild`. Podman's `build` (Buildah) likewise never commits per-step images, so the same code path is correct there. Profile builds with secrets already used this CLI path; the base build now matches. After switching, a one-time `docker image prune` clears the legacy intermediates left by prior builds (once `yoloai-base` is rebuilt with BuildKit they are no longer ancestors of any tag, so they prune cleanly and free real disk).

**Code:** `internal/runtime/docker/build.go` — `(*Runtime).buildBaseImage` (CLI/BuildKit via `<binary> build -`), `curatedBuildEnv` (forces `DOCKER_BUILDKIT=1`).

---

### Podman: docker-compat API has no build-cache endpoint — `BuildCachePrune` returns 404 (Not Found)

**Symptom:** `yoloai system prune` against the Podman backend prints `podman: build cache prune failed: Error response from daemon: Not Found`.

**Explanation:** Podman's Docker-compatible API has no BuildKit build-cache endpoint; `POST /build/prune` returns HTTP 404. The Podman backend embeds `*docker.Runtime` and inherits its `PruneCache`, which unconditionally calls `BuildCachePrune`. The 404 is expected and harmless, but surfacing it as "failed" is misleading.

**Fix:** In `PruneCache`, swallow the error when `cerrdefs.IsNotFound(err)` is true (it stays a real failure for any other error). Podman has no build cache to free, so skipping is correct.

**Code:** `internal/runtime/docker/prune.go` — `PruneCache` (`BuildCachePrune` error guarded by `!cerrdefs.IsNotFound`).

---

## Podman

### Podman: `/system/df` reports `LayersSize: 0`

**Symptom:** `yoloai doctor` / `yoloai system disk` reports the podman backend's images as **0 B** even though `podman system df` shows a multi-GB base image (e.g. ~5.5 GB). The cached tier (build cache, volumes) reports correctly; only the image tier reads zero.

**Explanation:** yoloai sizes images from the Docker SDK's `client.DiskUsage()`, taking the deduplicated `du.LayersSize` (see [`Images[].Size` includes shared layers](#diskusageimagessize-includes-shared-layers-summing-it-multiply-counts-them)). Docker populates `LayersSize` with the daemon's deduplicated layer-store total; **Podman's docker-compat `/system/df` always returns `LayersSize: 0`** and only fills the per-image `Size`/`SharedSize` fields. So the inherited docker code, correct for Docker, yields 0 for Podman.

**Fix:** The Podman backend injects a per-image dedup via `docker.Runtime.SetImageBytesFunc`. Summing `img.Size` would multiply-count the shared base (38 build stages sharing one ~5.5 GB base read as ~150 GB — the failure mode of the shared-layers entry above). The deduplicated total is `Σ(img.Size − img.SharedSize) + max(img.SharedSize)`: every image's unique bytes plus the shared layer set counted once. For yoloai's single-base build chain the largest `SharedSize` captures the full shared union exactly; multiple independent bases would slightly underestimate the shared tier.

**Code:** `internal/runtime/podman/podman.go` `podmanImageBytes()` (injected in `New` via `SetImageBytesFunc`); `internal/runtime/docker/prune.go` `splitCacheBytes()` (uses `imageBytesFn` when set, else `du.LayersSize`). Guard tests: `podman_test.go::TestPodmanImageBytes_*`, `docker/prune_test.go::TestSplitCacheBytes_ImageBytesFuncOverride`.

**macOS / version caveat (verified 2026-05-29, Podman 5.8.1 via Podman Machine `applehv`):** `LayersSize` is **NOT 0** on this version — the raw `/system/df` returns `LayersSize: 5018303449`, matching `podman system df` Images SIZE exactly. The `LayersSize: 0` bug above is therefore **Podman-version-specific**, not universal. The `podmanImageBytes` dedup still runs (it's unconditional) and, because every build-stage row shares the one base, it computes the *identical* value (`Σ(unique) + max(shared) == LayersSize` here), so it's harmless redundancy on 5.8.1 — the injected path agrees with the field it was working around. Keep the injection: older Podman (the version the bug was first seen on) still reports 0, and the dedup is correct on both.

### Podman: `ImagesPrune` `SpaceReclaimed` is the un-deduplicated image-size sum

**Symptom:** `yoloai system prune --images` on Podman reports a wildly inflated reclaim — e.g. **142.27 GB** freed when the actual footprint is ~5.18 GiB. The over-count scales with the number of images, exactly like the reporting-side bug.

**Explanation:** Podman's docker-compat `ImagesPrune` returns `SpaceReclaimed` as the **sum of every removed image's `Size`**, each of which *includes shared layers* — the same multiply-counting as [`DiskUsage().Images[].Size`](#diskusageimagessize-includes-shared-layers-summing-it-multiply-counts-them), but on the prune path instead of the sizing path. 38 build stages sharing one ~5 GiB base sum to ~140 GB. (Docker on the containerd store has the *opposite* problem — `SpaceReclaimed` undercounts.) So raw `SpaceReclaimed` is untrustworthy in both directions and must not be reported.

**Fix:** Don't use `SpaceReclaimed` at all. Report reclaim as the drop in the backend's own `CacheUsage` across the prune (`before − after`); `CacheUsage` already deduplicates correctly for Podman (via `podmanImageBytes`), so the delta is accurate (verified: 5.18 GB, matching the `/system/df` dedup) and self-attributed per backend. See working-notes D37.

**Code:** `internal/runtime/docker/prune.go` `PruneCache` + `reclaimableBytes` (shared by docker + podman). Note `BuildCachePrune` returns "Not Found" on Podman (no BuildKit cache) — warned and harmless; the before/after delta still captures the actual reclaim.

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

### Linux rootless privileged (dind): plain `keep-id` fails both ways; use `keep-id:uid=1001`

**Symptom:** On a Linux rootless Podman host, a `container-privileged` sandbox
either can't start a nested `dockerd` (`sudo: a password is required`) or, if you
strip `keep-id` to dodge that, the agent crashes reading its own prompt
(`PermissionError: /yoloai/prompt.txt`). Neither plain `keep-id` nor no-`keep-id`
works for privileged.

**Explanation:** The userns mode decides the container's starting uid, which
drives everything:

- **plain `keep-id`** maps the host user 1:1, so the container runs as that user
  (e.g. 1000). Host-written 0600 files map to a uid it owns (readable ✓), but that
  user is *not* `yoloai` — it has no passwordless sudo and no `docker` group, so
  `sudo dockerd` fails.
- **no `keep-id`** starts the container as root (mapped to a host subuid). The
  entrypoint can sudo, but host-written 0600 files (prompt, credentials) map to
  container-root while the agent is remapped `yoloai` — so they're unreadable and
  `deliver_prompt` crashes.
- **`keep-id:uid=1001,gid=1001`** maps the host user onto `yoloai` (1001). The
  agent runs as `yoloai`: it has sudo + docker (dind works) *and* host 0600 files
  map to a uid it owns (readable). This is the only mode that satisfies both.

**Fix:** For rootless privileged on Linux, `podman.go::Create` injects
`keep-id:uid=1001,gid=1001` (non-privileged stays plain `keep-id`; overlay and
macOS unchanged). Because the agent now runs non-root, the entrypoint's
`mount --make-shared /` (needed for dind mount propagation) runs via `sudo -n`
when not root — `yoloai` has passwordless sudo. See
`internal/runtime/docker/resources/entrypoint.py` `main()`. Docker-priv and macOS
podman-priv still run the entrypoint as root, so they take the direct (no-sudo)
path unchanged.

### Rootless Podman keeps a separate ID-mapped image copy per userns mapping

**Symptom:** `create container … creating an ID-mapped copy of layer … lchown …:
no space left on device`. Disk fills up faster than the `podman system df` image
total (~5.6 GB) suggests — running both the `podman` and `podman-priv` smoke
phases on one host roughly *doubles* the rootless-podman image footprint.

**Explanation:** When the `--userns` mapping doesn't line up with the layers'
on-disk ownership and the kernel/storage driver can't do a native idmapped mount,
rootless Podman falls back to `storage-chown-by-maps`: it materializes a full
**chowned copy** of the image layers for that mapping. Each *distinct* mapping
gets its own copy. Non-privileged podman maps host→1000 (plain `keep-id`);
privileged maps host→1001 (`keep-id:uid=1001`) — two different mappings, so two
~5.6 GB copies coexist. These copies live outside what `podman system df` counts,
so the disk shrinks without an obvious culprit.

**Fix / mitigation:** Not a yoloai bug — it's the price of the dual mapping that
makes privileged dind work (see the entry above). On a tight disk, ensure
headroom before a full smoke run (`df -h /`); the copies are reclaimed when the
underlying images are pruned. The Go build cache (`go clean -cache`, often
multi-GB) is usually the fastest unrelated reclaim if the host is already near
full.

### Podman macOS: iptables-nft lacks `xt_set` module; ipset unusable

On macOS, Podman Machine runs a Linux VM using `iptables-nft`. The `xt_set`
kernel module (which backs `iptables -m set --match-set`) is not loaded in
Podman Machine's kernel, so any `iptables` rule referencing an ipset set fails
with: `Can't open socket to ipset`.

Symptom: `isolate_network()` in `entrypoint.py` fails during `ipset create` or
the `--match-set` `iptables` rule, taking down the container with a
`NetworkIsolationError`.

Fix: probe for ipset availability with a try/except around the `ipset create`
call. On failure, fall back to per-IP `iptables -d <ip> -j ACCEPT` rules for
each allowlisted address. See `entrypoint.py::isolate_network`.

### Per-file bind mounts rejected by Podman's Docker-compatible API

Podman's Docker-compatible socket rejects per-file bind mounts where the source
is an existing file (e.g. `/run/secrets/ANTHROPIC_API_KEY`). Podman tries to
`mkdir` the source path, which fails with `EPERM`. Docker handles per-file bind
mounts correctly.

Workaround: bind-mount the entire secrets directory as one mount
(`/run/secrets → /run/secrets`) instead of individual per-secret file mounts.
See commit fefda87.

---

### Docker/Podman: agent git and apply git race on index.lock

**Symptom:** `yoloai apply` (or `yoloai diff`) fails with:
```
git add: git [add -A]: exit status 128: fatal: Unable to create '.git/index.lock': File exists.
```
when the agent is still running (stop/restart not yet complete, or `apply` called while agent is active).

**Cause:** For `:copy` mode sandboxes, the work directory is a bind-mounted host path shared between host and container. `yoloai apply --include-uncommitted` (and `HasUncommittedChanges`) calls `git add -A` on the host `.git`. The agent inside the container (e.g. Claude Code) independently runs `git add -A` for its status bar `(+2,-0)` display. Both processes race to acquire `index.lock`.

The lock is held for only milliseconds, making this a transient flake rather than a hard failure.

**Fix:** `HasUncommittedChanges` and the uncommitted-staging path in `patch/apply.go` retry `git add -A` up to 5 times with 100 ms delays on `index.lock` errors. `workspace.StageUntracked` (used by `diff.go`) applies the same retry. See `workspace.IsIndexLocked`, `workspace.StageUntracked`, and `patch.gitAddRetry`.

---

### Docker/Podman: interactive exec/attach must use the API socket, not the bare CLI (dual control-plane divergence)

**Symptom:** `yoloai exec`/`attach` on Podman intermittently fails with `Error: no such container <name>` (exit 125) for a container the daemon clearly has running — `yoloai info`/`DetectStatus` (which call `Inspect` over the socket) report it `active` the entire time. Surfaced in the `isolation_check` smoke test under the concurrent multi-backend load: the container was created+started at T+0, stayed alive ~42 s, yet a `yoloai exec` issued in that window could not resolve the name for ~30 s.

**Explanation:** The docker/podman backend had **two control planes for the same container**. Lifecycle and status (`Create`/`Inspect`/`Exec`/`Remove`) go through the Docker SDK over the discovered API socket; but `InteractiveExec` and `StdioExec` shelled out to the bare `docker`/`podman` CLI binary (`r.binaryName … exec -it`). The bare CLI re-opens the rootless-Podman container store independently of the long-lived socket connection, and under concurrent load it can fail to resolve a container name that the socket connection sees as `Running:true`. Same container, two resolvers, divergent answers — a classic split-brain. The podman event journal was the smoking gun: create/start logged at the start of the window, the container `died` 42 s later, and the *first* exec event only appeared 32 s in — i.e. the bare CLI never reached the live container during the failing window though the socket did.

**Fix:** Route interactive exec/attach through the same SDK socket as every other op. `InteractiveExec` and `StdioExec` now share one `execAttach` core: `ContainerExecCreate` → `ContainerExecAttach` (hijacked conn) → `bridgeExecStreams` (TTY: raw `io.Copy`; non-TTY: `stdcopy.StdCopy`) → `ContainerExecInspect` for the exit code, returning `&runtime.ExecError{ExitCode}` on non-zero. TTY sizing/resize go over `ContainerExecResize`. One connection, one control plane — no bare-CLI store race. (`r.binaryName` survives only for `build`/`prune`/log helpers.)

**Code:** `internal/runtime/docker/docker.go` `execAttach`/`createExec`/`bridgeExecStreams`/`resizeExec`/`forwardExecResizes`. Conformance guards (run for docker AND podman): `runtime/runtimetest/conformance.go` `StdioExec*`/`InteractiveExec*ExitCode` subtests.

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

### `GitExec` must return `*runtime.ExecError` (not a plain `fmt.Errorf`) on non-zero exit

`sandbox/patch/apply.go::HasUncommittedChanges` runs `git diff --quiet HEAD`
and treats exit 1 as "diffs present" via `errors.As(err, *runtime.ExecError)`.
After W8 of the architecture remediation (`e59704b`) replaced the previous
text-match (`strings.Contains(err.Error(), "exec exited with code 1")`) with
the typed-error check, containerd's `GitExec` silently broke `yoloai apply` on
every sandbox with uncommitted changes — including the smoke test's
`stop_start/containerd-vm`. Symptom: `apply: exit 1` with stderr
`git diff --quiet: exec exited with code 1`.

Docker / podman / seatbelt wrap the original `*exec.ExitError` via `%w`, so
`errors.As(err, *exec.ExitError)` (the fallback branch) recognises exit 1.
Tart goes through `runtime.RunCmdExecRaw`, which directly returns
`*runtime.ExecError`. Containerd unwrapped the `*exec.ExitError` into a plain
`fmt.Errorf("exec exited with code %d: %s", ...)` string, losing the error
type and breaking both branches.

Fix: construct `&runtime.ExecError{ExitCode, Stderr}` directly so callers can
match exit codes through `errors.As`. Regression test at
`runtime/containerd/containerd_test.go::TestGitExec_ExitOneReturnsExecError`.
See `runtime/containerd/containerd.go::GitExec`.

### `InteractiveExec` discarded the inner exit code, so `yoloai exec` always exited 0

Containerd's `InteractiveExec` (`runtime/containerd/exec.go`) drained the task's
exit channel but threw the status away — `<-exitCh; return nil`. Every
interactive exec therefore reported success regardless of the inner command's
exit code, so `yoloai exec <box> -- false` exited 0 on this backend (Docker
propagates the code for free: its `InteractiveExec` reads the exit code from
`ContainerExecInspect` over the socket). The non-interactive `Exec` on the same backend was
always correct — it reads `exitStatus.ExitCode()` — which made the gap easy to
miss.

This silently turned the smoke harness's `isolation_check` egress probe (which
keyed off the `yoloai exec` exit code) into a no-op: the inner `curl` was being
blocked correctly, but the swallowed exit code made the test alternately
"pass for the wrong reason" or trip its blocked-by-timeout deadline depending on
incidental exec-machinery errors — presenting as an intermittent isolation
fail-open that did not exist.

Fix: capture `exitStatus := <-exitCh` and return `&runtime.ExecError{ExitCode:
code}` on non-zero, mirroring the non-interactive `Exec`. The shared
`runtime.InteractiveExitError` helper normalizes the still-shelled-out backends
(tart/seatbelt) to the same `*runtime.ExecError` contract (docker/podman now read
the code from `ContainerExecInspect` over the socket), so every
backend's `InteractiveExec` surfaces a non-zero inner exit identically. The
public `Sandbox.Exec` boundary then translates that internal error into the
public `*yoloai.ExecExitError` (carrying the inner code) — the CLI can't import
`internal/runtime` (G7), so it matches the public type with one `errors.As` and
`os.Exit`s the code (`cli/sandboxcmd/exec.go`). Regression test:
`runtime/containerd/integration_test.go` (the `TestIntegration_ContainerLifecycle`
exec assertions).

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

### containerd: both overlayfs and devmapper snapshotters hold a copy; prune and sizing must cover both

**Symptom (sizing):** before the fix, `yoloai doctor` reported the containerd backend's image cache as `?` (the `ImageBytes == -1` "unknown" sentinel), hiding several GB of real disk. **Symptom (prune):** `yoloai system prune --images` left thin-pool allocation behind — `dmsetup status containerd-pool` still showed >50% data blocks used after a prune that claimed success — and the leaked snapshots eventually filled the pool (a likely contributor to the `smoke-containerd-disk-pressure` ENOSPC stalls).

**Explanation:** yoloai selects the snapshotter per isolation mode (`lifecycle.go`): **overlayfs** for `--isolation vm`/container, **devmapper** for `--isolation vm-enhanced` (Firecracker). A host that has run both modes therefore holds **two physical copies** of the base image's layers — one in `io.containerd.snapshotter.v1.overlayfs`, one in the devmapper thin-pool. The original `CacheUsage`/`pruneSnapshots` hardcoded `SnapshotService("overlayfs")`, so devmapper snapshots were never counted and never removed.

Sizing must go through the **containerd socket**, not the host filesystem: yoloai may run unprivileged via the `containerd` group, and `/var/lib/containerd` is root-only (so `du`/`dmsetup` are unavailable on the normal path). The snapshot `Usage(ctx, key).Size` API returns real allocated bytes for *both* snapshotters over the socket (devmapper reports per-thin-device allocation, summing to the pool's used-block total), so summing `Usage` across every snapshot in each snapshotter is the portable, root-free measurement.

**devmapper caveat:** removing a devmapper thin snapshot returns its blocks to the pool (the `dmsetup` used-block count drops), but the pool's backing loopback file (`/var/lib/containerd/devmapper/data`, host-configured at a fixed size, ~10 GB) **does not shrink** — discards are not punched back to the host file. So host `df` is unchanged by a prune even though the pool regains free blocks. yoloai's prune prints this explicitly so the reported reclaim isn't mistaken for freed host disk. (The pool itself is a host prerequisite, configured by the devmapper setup script + `/etc/containerd/config.toml`, not owned by yoloai — yoloai only prunes the snapshots it created inside it.)

**Code:** `internal/runtime/containerd/prune.go` — `snapshotterNames` (`{overlayfs, devmapper}`), `snapshotInfos` (Walk returning each snapshot's `Info` incl. `Parent`; `present=false` skips an unconfigured snapshotter), `orderLeafFirst` (Kahn topological pass; see below), `pruneSnapshots`/`pruneSnapshotter` (iterate both, remove leaf-first, sum each removed snapshot's `Usage`, print the devmapper caveat), `CacheUsage` (sums `Usage` across both into `ImageBytes`, per-snapshotter breakdown in `Detail`).

### containerd: snapshots must be removed leaf-first (children before parents) or removal silently stalls

**Symptom:** `prune --images` removes some snapshots but leaves a chain behind; `SnapshotService.Remove` returns `cannot remove snapshot with child: failed precondition` for layers that still have descendants. A single arbitrary-order `Walk`+`Remove` pass only deletes the chain's leaves, leaving the bulk to be reclaimed by a later GC (which doesn't always root them).

**Explanation:** Image layers form parent→child snapshot chains. containerd refuses to remove a committed snapshot that still has a child. To free a whole chain synchronously you must remove children before their parents.

**Fix:** Order removals leaf-first via a Kahn topological pass over the in-memory `Parent` links (`orderLeafFirst`): enqueue snapshots with no in-set child, emit each, decrement its parent's child-count, enqueue the parent when it reaches zero. Every `Remove` then succeeds in one pass and the returned reclaim total reflects bytes actually freed — no reliance on a later GC. Any snapshot left un-emitted (cycle, or a parent outside the set) is appended at the end so nothing is silently dropped.

**Code:** `internal/runtime/containerd/prune.go` `orderLeafFirst`, called by `pruneSnapshots`.

### containerd: image import inconsistently materializes overlayfs snapshots

**Symptom:** After `yoloai system build --backend containerd`, sometimes the import unpacks the image into overlayfs snapshots (e.g. 28 snapshots, so `system disk` immediately reports the footprint) and sometimes it only links the image (0 snapshots, `system disk` reports 0 image bytes for the namespace) — with no change in the command.

**Explanation:** The containerd import/link path doesn't deterministically unpack layers into the snapshotter; whether snapshots materialize at import time vs. lazily at first container `run` varies. `client.WithNewSnapshot` likewise does **not** unpack (see [`WithNewSnapshot` does NOT unpack image layers](#withnewsnapshot-does-not-unpack-image-layers)). So a freshly-built containerd image may carry content blobs but zero snapshots until a container is created from it.

**Consequence for testing:** to get a containerd snapshot footprint to size/prune, create a sandbox (the normal `run` path unpacks via `img.Unpack`) rather than relying on the build to materialize snapshots. Avoid `ctr images mount` for this — see the lease entry below.

### containerd: a leftover lease GC-roots an orphaned child, blocking base-layer removal

**Symptom:** `prune --images` removes every layer except the base, which refuses removal with `cannot remove snapshot with child: failed precondition` — yet `ctr -n yoloai snapshots ls` (and every other namespace) shows **no** snapshot claiming it as parent. Retrying `Remove` keeps failing; the snapshot only disappears after the responsible lease is deleted and GC runs.

**Explanation:** A lease with a `containerd.io/gc.expire` label (created automatically by `ctr images mount`, among others) GC-roots the snapshots it pinned, including an active/View child of the base layer. That child keeps the base un-removable, but it isn't a normal committed snapshot so it doesn't appear in `snapshots ls`. The synchronous `Remove` precondition check still sees it. Dropping the lease lets the next GC pass collect both.

**Consequence:** This is a **test-scaffolding artifact** (a leftover `ctr images mount` lease), not something yoloai's own create/destroy/prune flow produces — yoloai never creates such leases. If you manually `ctr images mount` to populate a testbed, `ctr -n yoloai leases rm <id>` afterward, or expect the base layer to linger until the 1-hour `gc.expire` elapses.

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

### Kata: secrets temp dir removed before the guest reads it

Symptom: after `yoloai restart` (and intermittently `new`) on `containerd-vm` /
`containerd-vmenhanced`, the agent launches but reports `Not logged in · Run
/login` and sits idle; the smoke harness reports "agent idle 9s+". The guest log
(`logs/sandbox.jsonl`) shows `secrets.skip "no secrets to inject"` and
`read_secrets.done loaded 0 secrets from /run/secrets` even though the
credentials are present on the host. Flaky — a retry usually passes. Distinct
from the DF8 netns warm-up race: the network probe is clean (dns/route/tcp/https
all ok) and the failure is an *auth* error, not a connection error.

Cause: credentials are written to an ephemeral host temp dir bind-mounted at
`/run/secrets`. The host removed that dir on a fixed 1-second timer after
`task.Start` returned — but `task.Start` returns while the Kata VM is still
booting (see the entry above), so on a slow boot the host deleted the dir before
the in-VM entrypoint read it. The guest then saw an empty `/run/secrets`, no
`CLAUDE_CODE_OAUTH_TOKEN` / API key reached the agent's environment, and Claude
Code came up unauthenticated. Restart is more susceptible than create because
tearing down the old VM contends with booting the new one, pushing guest boot
past the 1-second window. The same fixed sleep was harmless on Docker (near-
instant boot).

Fix: the in-sandbox entrypoint writes a host-visible marker
(`<sandboxdir>/logs/.secrets-consumed`, `store.SecretsConsumedMarker`) *after*
reading `/run/secrets`; the host polls for it (30s cap, then removes anyway so
the ephemeral dir never leaks) before removing the temp dir. The guest's
sequential code guarantees the read precedes the marker, and the host removal
happens only after it observes the marker, so the read strictly precedes the
removal — race eliminated. `entrypoint.py` (docker/containerd) and
`sandbox-setup.py` (tart/seatbelt) both write the marker;
`create_instance.go::buildAndStart` and `waitForSecretsConsumed` poll for it.

Gotcha that bit the first cut of this fix: the marker MUST live under a
bind-mounted `/yoloai` subdir (logs/), not at the `/yoloai` root. The container
gets individual bind mounts for `/yoloai/logs`, `/yoloai/files`, `/yoloai/cache`,
etc. (see `buildSystemMounts`), but the `/yoloai` root is **not** mounted — a
file written there lands on the container's own ephemeral fs and never reaches
the host, so the host would poll forever and fall back to the 30s timeout on
every launch (turning a flaky correctness bug into a deterministic 30s latency
penalty). `logs/` is the right home: it's bind-mounted and propagates guest→host
in real time (the smoke harness reads agent-created `/yoloai/files/done` from the
host side, proving sub-dir propagation is prompt).

**Tart variant — the 30s cap was too short, masking a live race (2026-05-28).**
"The read strictly precedes the removal — race eliminated" holds only when the
guest reaches its secrets read within the cap. On Tart it does not: a macOS VM
boots to the entrypoint's `read_secrets` in ~50s *warm*, and 120s+ on a cold
first boot that also runs `xcodebuild -runFirstLaunch` (see the Xcode entry
below). So the marker timed out on **every** Tart run — the smoke log shows the
"marker not observed before timeout" warning even on a *passing* run — and the
host removed the secrets dir at 30s while the guest read it ~20s *later*. The
removal-before-read invariant was violated; it only avoided an unauthenticated
agent because VirtioFS host→guest deletion propagation lags, so the guest still
saw the (host-deleted) dir. Correctness was riding on undefined timing.

Fix: the wait cap is now backend-declared. `BackendDescriptor.SecretsConsumedTimeout`
(0 = the 30s package default) lets a slow-booting backend raise it; Tart sets
180s so the host actually observes the marker before removing the dir, restoring
the invariant rather than relying on VirtioFS lag. Trade-off: on a cold
first-boot `new` blocks until the real read (the marker is the signal that the
guest is done) instead of bailing at 30s — correctness over latency for an
ephemeral credential. Code: `runtime.go` (`SecretsConsumedTimeout` field),
`runtime/tart/tart.go` (180s), `sandbox/create_instance.go::effectiveSecretsConsumedTimeout`.

Orphan cleanup: an abnormally-terminated `new` (killed / timed-out before
`launchContainer`'s `defer os.RemoveAll`) leaves the `yoloai-secrets-*` dir — a
plaintext credential — in the system temp dir; the 180s wait widens that window
on Tart. `yoloai system prune` sweeps stale `yoloai-*` temp dirs
(`PruneTempFiles`). That sweep previously scanned a hardcoded `/tmp` and so
**missed macOS entirely** (`os.MkdirTemp("", …)` writes to `os.TempDir()` =
`/var/folders/.../T`); fixed to scan `os.TempDir()`. The integration test that
asserted cleanup also now snapshots before/after rather than scanning the whole
shared temp dir, so a pre-existing orphan no longer fails it.

Related restart asymmetry (independent of the race): `recreateContainer`
previously omitted the sudo-recovered `credOverrides` that `Create` sets, so
`sudo yoloai restart` *without* `-E` lost credentials deterministically (the env
vars are stripped and there was no sudo-recovery fallback) even though `sudo
yoloai new` worked. Now mirrored in `lifecycle.go::recreateContainer`.

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

### `tart list` reports a pulled OCI image twice (tag + digest) over one on-disk copy; sizing and prune must dedup and remove both rows

**Symptom:** `yoloai system disk` reported tart as `IMAGES: ?` and `CACHE: 0 B` while `~/.tart` held **~56 GiB**, and `yoloai system prune --images` reported **0 reclaimed** even though it removed the base image. Tart implemented `PruneCache` but **no `DiskUsageReporter`**, so `CacheUsageFor` returned `ImageBytes=-1` ("unknown", rendered `?`) and the reclaim came back hardcoded `0`.

**Explanation (verified 2026-05-29, Tart 2.31.0, Apple Silicon):** a single pulled OCI base (`ghcr.io/cirruslabs/macos-sequoia-base:latest`) appears as **two** `tart list` rows — one by tag (`:latest`) and one by digest (`@sha256:…`) — both reporting the same `Size` (e.g. 31 GB) but backed by **one** on-disk directory under `~/.tart/cache/OCIs/<repo>/sha256:<digest>/`. Naively summing `tart list` Size double-counts the OCI base; and `tart delete <tag>` removes only the tag row, leaving the digest row pinning the on-disk copy, so a tag-only prune frees ~0. The provisioned local VM (`yoloai-base`) is a separate clone under `~/.tart/vms/` with its own footprint (additive, no sharing). `tart list --format json` Size is **whole-GB** (decimal, rounded), so the figure is coarse (±~0.5 GB/image) but reconciles with `du`.

**Fix:** Tart now implements `DiskUsageReporter`. `CacheUsage` sums the provisioned VM + the base-repo OCI rows **deduped to one** (max Size per repo, mirroring the podman "count shared once" approach), reporting it as `ImageBytes` (tart has no no-rebuild cache → `CachedBytes` always 0). `PruneCache` deletes the provisioned VM **and every base-repo OCI row** (tag *and* digest), then reports reclaim as the `CacheUsage` before−after delta (D37), same as docker/podman. Scope is deliberately yoloai's base images only — not every VM tart tracks, nor live sandbox clones — so the IMAGES column reconciles with what `prune --images` actually frees (unlike docker/podman, tart is the user's general VM tool and must not imply it'll delete unrelated personal VMs). Result: tart now reports **55.88 GiB** (matching `du`'s ~56 GiB) and the dry-run estimate includes it.

**Code:** `internal/runtime/tart/diskusage.go` (`CacheUsage`, `ownedImageBytes`, `ownedImageRefs`, `baseImageRepo`); `internal/runtime/tart/prune.go::PruneCache` (before/after delta, deletes all owned refs). Tests: `diskusage_test.go::{TestBaseImageRepo,TestCacheUsageCountsOwnedImagesDedupingOCI,TestPruneCacheReportsReclaimDelta,TestPruneCacheDryRunReturnsEstimate}`.

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

### `tart delete` of a running VM fails with a misleading "instance not found"; stop first

**Symptom:** `tart delete <name>` returns `instance not found` for a VM that
demonstrably exists in `tart list` — because the VM is **running**. The error
names the wrong cause (it reads as "no such VM" rather than "VM is busy/running").
Observed as `delete old base: instance not found` at the final *promote* step of
`yoloai system build --backend tart`, abandoning an hour-long provision: the swap
in `Setup` deletes the old `yoloai-base` before cloning the freshly provisioned
temp VM into place, and if the old `yoloai-base` happened to be running, the
delete failed and the whole build was lost.

**How a base ends up running unexpectedly:** anything that left a `tart run
<name>` process alive — a crashed/abandoned boot, a stray manual `tart run
yoloai-base` (e.g. a diagnostic that backgrounded it and never stopped it), or an
unclean shutdown (`Error: unavailable (14): Transport became inactive` is tart's
normal exec-transport drop as a VM powers off, but the host process can linger).
`tart list` will show the VM as `running` and `ps aux | grep "tart run"` reveals
the host launcher PID.

**Fix:** stop the VM before deleting it. `Setup`'s promote now calls
`r.stopVM(ctx, provisionedImageName)` (the bounded `tart stop` → SIGTERM →
SIGKILL ladder in `tart.go::stopVM`, best-effort and a no-op when already
stopped) before `tart delete`. See `build.go::Setup` promote block. Any future
code path that deletes a VM that *could* be running must stop it first rather
than trusting `tart delete`'s error text.

**Manual recovery:** `tart stop <name>` (or kill the `tart run <name>` host PID),
then retry. The provisioned base's identity isn't recorded until after a
successful promote, so a failed promote leaves the old base in place and
`needsBuild` still true — re-running `system build` rebuilds cleanly.

### `tart run` process must use `exec.Command`, not `exec.CommandContext`

`tart run <vmName>` is a long-lived process that keeps the VM alive. Using
`exec.CommandContext` with the parent's context would kill the VM when the
`Start()` function's context is cancelled (e.g. on HTTP request completion or
timeout). Must use bare `exec.Command`, then set `SysProcAttr{Setpgid: true}`
to detach it from the parent process group. See `tart.go::Start`.

### Tart cannot mkdir/symlink system directories like /var/folders

**Symptom:** VM setup fails with:
```
mkdir: /var/folders: Permission denied
```
or:
```
ln: /var/folders/.../project-name: Permission denied
```

**Explanation:** During mount setup, Tart creates symlinks from expected mount paths
to VirtioFS share paths. Both `mkdir -p` and `ln -sfn` can fail on system paths like
`/var/folders/...` (macOS temp directories) and `/private/var/...` because these are
root-owned inside the Tart VM. The parent directories already exist (created by macOS),
and the symlink needs `sudo` to write into them.

**Fix:** Make mkdir non-fatal: `(mkdir -p ... || sudo mkdir -p ... || true)`. For the
symlink, try without sudo first, fall back to sudo:
`(rm -rf '$target' && ln -sfn ...) || (sudo rm -rf '$target' && sudo ln -sfn ...)`.
This avoids hardcoding which paths need sudo.

**Code:** `runtime/tart/tart.go::runSetupScript` line ~900

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

The investigation's symlink test (docs/contributors/archive/investigations/ios-testing-investigation.md:656-662) moved a **local directory** to another location and symlinked it - this worked because both source and target were on the same local APFS volume. When the symlink points to a **VirtioFS mount**, the filesystem semantics are fundamentally different and CoreSimulator rejects it.

**This is a fundamental architectural limitation** - VirtioFS cannot emulate sealed APFS volumes. Runtimes **must** be copied to local VM storage or downloaded fresh inside the VM.

**Workaround:** Hybrid approach (validated in investigation):
- Mount Xcode.app from host via VirtioFS (saves ~11GB) - works fine
- Mount PrivateFrameworks from host via VirtioFS (saves ~2GB) - works fine
- **Copy or download runtimes locally** inside VM (~8-16GB per runtime) - required

**Code:** See `docs/contributors/archive/investigations/ios-testing-investigation.md` lines 844-966 for empirical testing; `runtime/tart/runtime_copy.go` for copy implementation.

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

**Verification:** See `docs/contributors/design/research/ios-runtime-download-verification.md` for complete manual verification that the download approach produces bootable simulators.

**Code:** `runtime/tart/runtime_copy.go` (currently implements ditto approach, needs replacement with download approach)

### `cirruslabs/macos-*-xcode` base images already bake in the default iOS runtime — the in-VM download is redundant for it

**Symptom / question:** the in-VM `xcodebuild -downloadPlatform iOS` step (~8.5 GB download, expands to ~16 GB) is slow on every Tart `new`. Does the `ghcr.io/cirruslabs/macos-tahoe-xcode:latest` base (and its `macos-*-xcode` siblings) already include a simulator runtime so we can skip it?

**Answer — yes, for the *default* runtime only.** The Cirrus Labs Xcode images are built by `templates/xcode.pkr.hcl` in `cirruslabs/macos-image-templates`, which runs exactly the commands our own verification settled on, but at *image-build* time:
```
"xcodebuild -downloadPlatform iOS",
"xcodebuild -runFirstLaunch",
```
So `:latest` ships with the default iOS runtime pre-installed and validated. Extra versions are bakeable via the template's `additional_ios_builds` variable, but the stock published image carries only the default. If a project needs a *specific* (non-default) iOS version, that one still has to be downloaded in-VM via `xcodebuild -downloadPlatform iOS -buildVersion <build>` — same path the template uses for extras.

**Known caveat — sporadic non-recognition on Xcode 26.x.** The runtime is installed but occasionally stops being recognized by `simctl` ("iOS X.Y is not installed. Please download and install the platform from Xcode > Settings > Components."), ~1 in 20 runs, correlated with Apple runtime / Xcode point releases. Cirrus has no confirmed fix from Apple. This means the in-VM download must stay available as a fallback even after we adopt a baked-in base — don't delete `runtime_copy.go`'s download path, gate it.

**Tradeoff:** baking in trades per-run download time for a larger base-image pull/storage footprint (Xcode + runtime). Relevant when picking/pinning the Tart base in `runtime/tart`.

**References:**
- [macos-image-templates `xcode.pkr.hcl`](https://github.com/cirruslabs/macos-image-templates/blob/master/templates/xcode.pkr.hcl)
- [Issue #303 — iOS simulator not recognized on `macos-tahoe-xcode:26.1`](https://github.com/cirruslabs/macos-image-templates/issues/303)
- [actions/runner-images #12948 — Xcode 26 runtime recognition flake](https://github.com/actions/runner-images/issues/12948)

**Code:** `runtime/tart/runtime_copy.go` (download path — keep as fallback), Tart base-image selection in `runtime/tart`.

### VirtioFS corrupts git repositories

**Symptom:** Git commands inside Tart VMs fail with corruption errors:
```
fatal: unable to read 5e01dacada080659f675a6213ba8f7a02447996f
```

Additionally:
- Same file appears both staged and unstaged
- `git status` shows changes but `git diff` fails
- Corruption appears after `yoloai reset` operations

**Root cause - VirtioFS/9P protocol limitations:**

Git's object database has strict filesystem requirements that VirtioFS (9P protocol) cannot satisfy:

1. **No hard link support** - Git uses hard links extensively for object deduplication and packing. The 9P protocol fundamentally does not support hard links (Plan 9 architecture has no "unix leftovers like hard/soft links"). When git tries to create hard links on VirtioFS, they're silently converted to copies or fail, corrupting the object database structure.

2. **Data corruption during concurrent operations** - The Linux kernel mailing list documents 9p data corruption issues with writeback caching during concurrent file operations (LKML 2026/2/18/794). Git's object database relies on concurrent reads/writes to pack files and loose objects.

3. **Atomic operation failures** - Git expects atomic rename operations for safe object creation. Network filesystems like VirtioFS may not provide proper atomicity guarantees, leading to partially-written objects or lost updates.

4. **Cache coherency issues** - VirtioFS uses aggressive client-side caching for performance. Git's fsync expectations may not be honored, resulting in stale reads or lost writes.

**Current yoloAI architecture (problematic):**

For Tart VMs with `:copy` mode directories:
1. Work directory copied to `~/.yoloai/sandboxes/<name>/work/` on host
2. Shared back to VM via VirtioFS at `/Volumes/My Shared Files/yoloai/work/...`
3. **Agent and git run inside VM on the VirtioFS mount** ← corruption happens here

The corruption is especially triggered by `yoloai reset`, which:
- Deletes and re-copies the work directory on the host
- Restarts the container/VM
- Git then operates on the fresh VirtioFS mount and corrupts its object database

**Fix:** Work directories must be on **local VM storage**, not VirtioFS mounts:

1. During sandbox creation: Copy work directory to local VM filesystem (e.g., `/Users/admin/yoloai-work/<escaped-path>`)
2. Update runtime-config.json to use the local VM path as `working_dir`
3. Git and agent operations run on local storage (fast, no corruption)
4. During diff/apply: Copy changes from local VM → VirtioFS → host for final transfer

VirtioFS should only be used for:
- Transferring initial state (host → VM during creation)
- Transferring final state (VM → host during diff/apply)
- Never for active git operations

**References:**
- Linux kernel mailing list: [9p data corruption with writeback caching during concurrent operations](https://lkml.org/lkml/2026/2/18/794)
- ddev project: [Git "dubious ownership" error triggered when using VirtioFS](https://github.com/ddev/ddev/issues/4829)
- Hacker News discussion: [virtfs uses 9p - hard link limitations](https://news.ycombinator.com/item?id=33009752)

**Impact:** All Tart VMs with `:copy` mode directories are affected. Git corruption can lead to data loss and broken repositories.

**Code:** `runtime/tart/tart.go::ResolveCopyMount`, `runtime/tart/tart.go::Create`, `sandbox/lifecycle.go::Reset` (needs implementation)

### A host-side change probe is blind to the in-VM workdir — `info` showed `Changes: no` on a dirty Tart sandbox, and `destroy` skipped its gate

**Symptom:** A Tart sandbox with real, unapplied work (`yoloai diff x` lists a new file) reported `Changes: no` in `yoloai sandbox x info`, and `yoloai destroy x` tore it down **without** demanding `--abandon-unapplied`. Silent data loss.

**Root cause:** Because [VirtioFS corrupts git repositories](#virtiofs-corrupts-git-repositories), the working copy of a Tart `:copy`/`:overlay` sandbox lives on **VM-local storage**, not on the host. The host-side `work/` tree is only the *seed* copied in at creation — it never receives the agent's edits. The old change probe ran `git status --porcelain` against that host seed, so it always saw the pristine baseline and answered "no changes." The destroy/replace gate trusted that answer and let the teardown through. (Host-bind-mount backends — Docker/Podman/Containerd/Seatbelt — are unaffected: their workdir *is* the host path, so a host probe is correct.)

**Fix:** Route change detection through the runtime via `runtime.GitExecFor` (`patch.HasUnappliedWorkVia`), so the probe runs *inside* the VM where the real working copy lives, exactly like `diff`/`apply`. The probe is tri-state (`WorkClean`/`WorkDirty`/`WorkUnknown`): when the VM-local backend is **stopped**, its `GitExec` returns `runtime.ErrNotRunning` and the probe reports `WorkUnknown` — the change state genuinely can't be read from the host. Callers **fail safe** on `WorkUnknown`: `info`/`list` surface `Changes: unknown` (public `yoloai.ChangesUnknown`), and the destroy/replace and reset gates block with a "sandbox is stopped, so unapplied changes can't be verified (start it to check, or use --abandon-unapplied)" message rather than reading a stale host seed and silently proceeding.

**Why not just read the host seed:** there is no coherent host-side view to read — that's the whole reason git runs in-VM (see the VirtioFS section above). A host probe isn't merely stale, it's structurally incapable of seeing in-VM edits.

**Code:** `internal/sandbox/patch/changes.go::HasUnappliedWorkVia` (+ `WorkProbe` tri-state), `internal/runtime/runtime.go::GitExecFor`/`ErrNotRunning`, gates in `internal/sandbox/create/create.go`, `internal/sandbox/lifecycle/reset.go::NeedsConfirmation`, and the read-model in `internal/sandbox/status/status.go::detectWorkdirChanges`. The engine opens the backend best-effort (`Engine.TryEnsure`) before the gate so a running VM can be probed.

### Provisioned tool dirs live only on the *login* PATH (Cirrus base image)

**Environmental fact:** The Cirrus-based Tart image composes its tool PATH in `~/.zprofile` — Homebrew, keg-only `node@22`, and `~/.local/bin` where the native Claude Code binary lives. That file is sourced only by *login* shells. The agent, however, is launched via `tart exec bash -c` (non-login) and, on restart, from Go via `respawn-pane`; neither sources `~/.zprofile`, so without intervention `claude` is not on PATH and the agent silently fails to start (a shell prompt, no agent process).

**How yoloAI handles it:** The backend's launch wrap (`PATH="$HOME/.local/bin:/opt/homebrew/opt/node@22/bin:/opt/homebrew/bin:$PATH"`) is a compile-time constant declared on the backend descriptor (`BackendDescriptor.AgentLaunchPrefix`). It is computed once at sandbox creation and stored as `agent_launch_prefix` in `runtime-config.json` — the single source of truth. Every launch path prepends that stored value: Go restart in `lifecycle/restart.go` and Python first-launch in `sandbox-setup.py` both read the field directly (older sandboxes are backfilled by the v1→v2 schema migration), so the two paths can never drift. (Historical note: an earlier base installed Claude Code via npm with a `#!/usr/bin/env node` shebang that the Cirrus image's `node@24` shadowed; switching to the native standalone binary removed that whole class of node-version shadowing, but the agent still needs `~/.local/bin` on the non-login PATH.)

**Code:** `runtime/tart/tart.go` (descriptor `AgentLaunchPrefix` + `PrepareAgentCommand`), `runtime/tart/build.go` (provisionCommands compose the login PATH), `sandbox/create/create.go` (stores the prefix), `sandbox/lifecycle/restart.go` (relaunch prepends it), `config/schema.go` (v1→v2 backfill)

---

## Seatbelt (macOS sandboxing)

### Seatbelt has no backend image/cache store — `CacheUsage`/`PruneCache` are correctly absent

**Symptom / question:** `yoloai system disk` shows seatbelt as `IMAGES: ?` and `CACHE: 0 B`. Is that a reporting gap like the Tart one was?

**Explanation (verified 2026-05-29):** No. Seatbelt runs agents **directly on the host** via `sandbox-exec` using the host's own tools — its `Setup` only *checks* that required binaries are on `PATH` (`runtime/seatbelt/build.go`); it pulls/builds/caches **nothing**. There is no VM, no image, no layer store. The only on-disk state a seatbelt sandbox accumulates is the per-sandbox directory under `~/.yoloai/sandboxes/<name>/` (work dirs, agent-state, logs) — and that's already reported by the `sandboxes` row of `system disk`, the same for every backend. So seatbelt implements neither `DiskUsageReporter` nor `CachePruner`, and its core `Prune` is a no-op (no central registry of instances). The `?` in the IMAGES column is `CacheUsageFor`'s "unknown" fallback (`ImageBytes=-1`); it's cosmetically imperfect (a true "—"/0 would read better) but functionally correct — there is genuinely nothing for `prune`/`prune --images` to reclaim. **Leave it a no-op; do not invent a cache to measure.**

**Code:** `internal/runtime/seatbelt/build.go::Setup` (PATH check only), `internal/runtime/seatbelt/prune.go` (no-op `Prune`, no `PruneCache`/`CacheUsage`); fallback in `internal/runtime/runtime.go::CacheUsageFor`.

### macOS `sandbox-exec` doesn't nest — Swift PM needs the swift-wrapper sourced

**Environmental fact:** macOS sandboxes don't support nesting, so a project's own Swift PM commands — which internally invoke `sandbox-exec` — fail inside a Seatbelt sandbox with nesting errors. The workaround is `~/.swift-wrapper.sh`, which intercepts swift commands and adds `--disable-sandbox`; it must be sourced into the agent's shell before launch, or Swift build/test breaks.

**How yoloAI handles it:** The backend's launch wrap (`source ~/.swift-wrapper.sh && `) is a compile-time constant declared on the backend descriptor (`BackendDescriptor.AgentLaunchPrefix`), computed once at sandbox creation and stored as `agent_launch_prefix` in `runtime-config.json` — the single source of truth. Both launch paths prepend that stored value: Go restart in `lifecycle/restart.go` and Python first-launch in `sandbox-setup.py` read the field directly (older sandboxes are backfilled by the v1→v2 schema migration), so the wrapper is sourced identically whether the agent starts via the Python path or a later Go-driven restart.

**Code:** `runtime/seatbelt/seatbelt.go` (descriptor `AgentLaunchPrefix` + `PrepareAgentCommand`), `sandbox/create/create.go` (stores the prefix), `sandbox/lifecycle/restart.go` (relaunch prepends it), `config/schema.go` (v1→v2 backfill)

---

### Agent dies silently (SIGTRAP) — SBPL subpath rules must use vnode-resolved paths

**Symptom:** Under Seatbelt the agent (claude/Node) dies 0.5–3.5s after launch with no output; the tmux pane is already dead at the post-launch check. `sandbox-exec -f profile.sb claude --version` exits 133 (128+5 = SIGTRAP). A `.ips` crash report in `~/Library/Logs/DiagnosticReports/` shows `EXC_BREAKPOINT`/`SIGTRAP` ("pointer authentication trap IB") on the main thread inside ICU `std::__call_once` / `uenum_count`. The macOS unified log shows `deny file-read-data /private/var/db/timezone/...`.

**Explanation:** macOS firmlinks `/var` → `/private/var` (also `/etc`, `/tmp`), and the sandbox enforces access at the **vnode level — after symlink resolution**. An SBPL rule for `(subpath "/var/db")` does **not** match a read of the resolved `/private/var/db`. ICU loads timezone data from `/private/var/db/timezone/tz/<ver>/zoneinfo/...` at startup; when that read is denied, ICU aborts the process via SIGTRAP before any agent output. `writeProfileSystemPaths` was the only profile section that emitted raw `systemReadPaths()` entries without running them through `resolvePathVariants`, so `/var/db` and `/var/run` rules never covered their `/private/var/...` targets.

**Fix:** Wrap every `systemReadPaths()` entry in `resolvePathVariants()` so the resolved `/private/var/...` variant is emitted alongside the original — matching what every other profile section already does.

**Code:** `runtime/seatbelt/profile.go::writeProfileSystemPaths` (+ `resolvePathVariants`); regression test `seatbelt_test.go::TestGenerateProfile_SystemPathsSymlinkResolved`

---

### Interactive error output "stair-steps" — local-PTY backends must bridge, not inherit host stdio (also Tart)

**Symptom:** On seatbelt (and tart), when an interactive command fails early — e.g. `tmux` can't open its socket — the error message cascades down-and-to-the-right, each line starting one column further right than the last, instead of printing as clean left-aligned lines.

**Explanation:** The CLI boundary (`cliutil.WithTerminal`) puts the host tty in raw mode (`term.MakeRaw`) for *every* interactive command, which clears `OPOST`/`ONLCR` — so a bare `\n` no longer gets an implicit carriage return. The bridged backends (docker/podman/containerd) are unaffected because their child runs under a *remote* PTY whose slave still has `OPOST` on, emitting proper `\r\n` that the library copies verbatim. Seatbelt and tart used to hand the child the host's `os.Stdin/Stdout/Stderr` directly (`cmd.Stdout = streams.Out`); the child then wrote bare `\n` into the raw host tty → stair-step. Inheriting host `*os.File`s also violated the `IOStreams` abstraction — it only worked when the streams happened to be real terminals, breaking any non-CLI embedder.

**Fix:** Run the child under a *locally* allocated PTY (`runtime.PTYBridgeExec`, via `creack/pty.StartWithSize`) and `io.Copy` the master to the caller's streams — the same model docker uses, but with a host-local PTY. The PTY slave keeps `OPOST` on, so the child emits `\r\n` and the raw host tty renders it correctly. This also makes `IOStreams.Resize` work uniformly (forwarded via `pty.Setsize`) and keeps both backends embedder-safe. **Tart caveat:** `tart exec -t` already allocates a PTY inside the VM, so wrapping it locally is a double-PTY (local + remote, like `script ssh -t`) — correct, but only exercisable on a macOS host with Tart.

**Code:** `runtime/interactive_pty.go::PTYBridgeExec`; `runtime/seatbelt/seatbelt.go::InteractiveExec`; `runtime/tart/tart.go::InteractiveExec`. The CLI raw-mode owner is `cli/cliutil/streams.go::WithTerminal` (unchanged — now uniform across backends).

---

### Seatbelt derived artifacts must be regenerated on Start, not frozen at Create

**Symptom:** A Seatbelt sandbox created by an older yoloai binary still hits an already-fixed bug after upgrading — e.g. the SIGTRAP above, or `os.symlink ... FileExistsError` at `sandbox-setup.py` — even after `yoloai system migrate` relocated its data dir. Destroy + recreate works; restart/attach does not.

**Explanation:** Unlike container backends (where `runtime-config.json` holds container-internal paths and the host dir is only a bind-mount source resolved fresh each Start), Seatbelt runs on the host and bakes host-absolute artifacts into the sandbox dir at **Create** time: the SBPL `profile.sb` and the monitor scripts (`sandbox-setup.py`, etc.). `Start` originally just re-read them off disk, so any bug baked in at Create — a profile missing the `/private/var` rules, a non-idempotent symlink step — persisted across upgrades. Data-dir migration relocates the directory but does not rewrite these frozen files, so a migrated old sandbox can never come up clean.

**Fix:** `Start` regenerates the derived artifacts from the persisted `InstanceConfig` before launch — `GenerateProfile(cfg, sandboxPath, r.homeDir)` + `writeSandboxScripts(sandboxPath)`. They are pure functions of config and host environment, not user state, so a restart on a newer binary self-heals sandboxes created by an older one. Independently, the `sandbox-setup.py` symlink guards use `os.path.lexists` (not `os.path.exists`, which follows symlinks and misses a dangling link) so re-running setup is idempotent.

**Code:** `runtime/seatbelt/seatbelt.go::Start` (regen block after config load); `runtime/monitor/sandbox-setup.py` (`os.path.lexists` guards in `SeatbeltBackend.setup`)

---

### Host-side tmux socket must be derived live, not read from frozen `runtime-config.json` (Seatbelt)

**Symptom:** `yoloai attach <box>` on a migrated Seatbelt sandbox fails with `error creating /Users/<you>/.yoloai/sandboxes/<box>/tmux/tmux.sock (No such file or directory)` — note the **flat** path, missing the `library/` principal-partition segment that migration introduced. Restart can also talk to the wrong socket on the prompt-delivery path.

**Explanation:** `runtime-config.json` froze a `tmux_socket` field at Create. For container backends that value is a *container-internal* path (e.g. under `/yoloai`), which is migration-invariant — fine. But Seatbelt's socket is a **host** path under the sandbox dir (`<sandboxDir>/tmux/tmux.sock`), and `yoloai system migrate` relocates the sandbox dir (e.g. into the principal partition `…/sandboxes/library/<box>/`) **without rewriting the frozen field**. The in-sandbox tmux server is created at the *live* path (Python's `SeatbeltBackend.get_tmux_socket` derives it from live argv), so the Go host side — reading the frozen flat path — pointed at a socket that doesn't exist. Docker was unaffected only because its frozen value is a container path that never moves.

**Fix:** Every Go host-side consumer derives the socket live via `runtime.TmuxSocket(layout.SandboxDir(name))` (what `terminal.go`'s capture-pane already did), instead of reading the frozen field. For docker this returns the same container path as before (no behavior change); for seatbelt/tart it tracks the current host dir. The frozen field stays in `runtime-config.json` because Python's docker backend still reads it (a migration-invariant container path), but the Go side no longer trusts it. General rule: **a frozen host-absolute path is a migration hazard — recompute host paths from the live layout, freeze only target-internal paths.**

**Code:** `sandbox/attach.go::Attach` + `WaitForAttachReady` (was `ReadTmuxSocket`, now deleted); `sandbox/lifecycle/restart.go` (relaunch + `deliverPromptViaTmux`); the live source is each backend's `TmuxSocket(sandboxDir)`.

---

### QEMU: slow startup exceeds smoke test stall grace period

**Symptom:** `stop_start/containerd-vm` fails with `"agent idle for 9s+ without sentinel 'done'"` even though the sandbox and agent are healthy.

**Explanation:** The smoke test's `wait_for_sentinel` has a stall detection mechanism: after a 30s grace period, if the sandbox status is "idle" for 3 consecutive 3-second polls (9s), the test fails early. For QEMU-backed Kata VMs, QEMU boots slower than Firecracker. By the time the QEMU VM starts, Claude loads, and Haiku model inference runs for the prompt command, the 30s grace period has already expired. The status becomes "idle" (Claude ready at `❯` or model inference in progress without a tool hook firing) and the stall detection triggers before the `done` file is created.

Firecracker (`containerd-vmenhanced`) starts faster and completes the task well within the grace period, so it is not affected.

**Fix:** `BackendSpec` now has a `stall_grace_secs` field. `containerd-vm` sets it to 120s, giving QEMU enough time to boot and process the prompt before stall detection activates. The stall detection still fires at 120+9=129s for genuinely stuck QEMU agents (vs. the full 300s QEMU_TIMEOUT).

**Code:** `scripts/smoke_test.py::BackendSpec.stall_grace_secs`, `Test.wait_for_sentinel`

---

### Tart: `xcodebuild -runFirstLaunch` blocks agent startup

**Symptom:** Smoke test `stop_start/tart` fails consistently on first attempt, with the exchange dir empty (agent never ran any commands). Stall detection fires before the `done` sentinel appears. On retries, the tests pass — typically after 3+ failed attempts, subsequent attempts succeed quickly.

**Explanation:** When an Xcode.app is mounted via VirtioFS (`/Volumes/My Shared Files/m-Xcode*.app`), `TartBackend.setup()` runs `xcodebuild -runFirstLaunch` to initialize Xcode components (device types, SDKs, etc.). On first run, this takes 60-120+ seconds. Because `setup()` runs synchronously before the tmux session and agent are started, the agent cannot start until xcodebuild finishes. The smoke test's stall detection fires at ~45s of polling (30s grace + ~15s), before the agent has a chance to run the bash prompt.

The pattern of "fails then passes on retry" comes from VirtioFS persistence: `xcodebuild -runFirstLaunch` writes initialization state into the Xcode.app bundle itself (which lives on the host via VirtioFS). Even after the failing VM is destroyed, the initialized state remains in the host-side Xcode.app bundle. Subsequent VMs find xcodebuild already initialized and skip the slow initialization, completing setup in seconds.

**Fix:** `xcodebuild -runFirstLaunch` now runs in the background via `subprocess.Popen(..., start_new_session=True)` with a log file at `{yoloai_dir}/xcodebuild-firstlaunch.log`. The agent starts immediately; xcodebuild completes in the background. Additionally, `stall_grace_secs=120` is set on all tart `BackendSpec` entries in the smoke test as a defensive measure.

**Residual (observed 2026-05-28, run `yoloai-smoketest-20260528-085108.627`):** the fix does not fully eliminate the cold-first-boot transient. `full_workflow/tart` failed with `command timed out` — the harness's **outer per-command wall-clock**, a *different* path than the stall detection that `stall_grace_secs=120` covers — then passed on retry. Even backgrounded, first-launch xcodebuild contends for VM CPU/IO and slows Claude/Haiku enough to blow the per-command timeout; the preserved attempt showed `agent-status.json {}` and Claude parked at the welcome screen (prompt never processed) with `xcodebuild-firstlaunch.log` mid-install. It's one-time per host/Xcode version (state persists in the host Xcode.app bundle), so retry is the practical mitigation. A complete fix would pre-warm `xcodebuild -runFirstLaunch` during base-image build / a one-time host preflight so no test VM pays it. Note this also interacts with the secrets-consumed wait (now 180s on Tart, see the secrets entry above): a cold boot legitimately blocks `new` longer while the guest finishes setup before reading secrets.

**Code:** `runtime/monitor/sandbox-setup.py::TartBackend.setup`, `scripts/smoke_test.py::BASE_MACOS_BACKENDS`

---

### Tart: mount_map uses Docker-style paths, triggering macOS automount hang

**Symptom:** `yoloai new --attach` with a Tart VM hangs indefinitely after printing "Sandbox created". Python's sandbox-setup.py stops producing log entries after `tart.symlinks` and never creates the tmux session. The `done` sentinel never appears in smoke tests even after 180s.

**Explanation:** `addMountMapToConfig` writes mount targets into `runtime-config.json`'s `mount_map` using the original Docker-style paths (e.g. `/home/yoloai/.config/git`). Python's `TartBackend.setup()` reads this map and calls `sudo mkdir -p /home/yoloai/.config` to create the symlink parent. On macOS, `/home` is managed by `automountd` — attempting to mkdir inside it triggers a network automount lookup for the `yoloai` home directory, which hangs until the lookup times out (60-120+ seconds). The Go-side `createVMMountSymlinks` correctly applies `remapTargetPath` (mapping `/home/yoloai/...` to `/Users/admin/...`), but the Python-side `mount_map` was missing this translation.

**Fix:** Apply `remapTargetPath` to mount targets in `addMountMapToConfig` before writing to `mount_map`. Python now receives `/Users/admin/.config/git` instead of `/home/yoloai/.config/git` and creates the parent dir at a valid macOS path with no automount involvement.

**Code:** `runtime/tart/tart.go::addMountMapToConfig` (apply `remapTargetPath`), `runtime/monitor/sandbox-setup.py::TartBackend.setup` (uses mount_map targets)

---

### Tart: VM workdir setup races Python startup

**Symptom:** `FileNotFoundError: No such file or directory: '/Users/admin/yoloai-work/...'` in setup.log. The agent never starts. Appears after fixing the automount hang (below), because that hang was accidentally delaying Python long enough for the Go-side rsync to finish.

**Explanation:** Python's `sandbox-setup.py` is launched via `nohup ... &` inside `launchContainer`. Go's `executeVMWorkDirSetup` (which runs rsync + git baseline to populate the workdir) is called *after* `launchContainer` returns. Python therefore reaches `backend.get_working_dir()` → `os.chdir(working_dir)` before the directory exists, crashing immediately.

Previously, Python was delayed 60-120s by the automount hang on `/home/yoloai/.config`, which gave rsync enough time to finish. Fixing the automount bug removed that accidental delay.

**Fix:** `TartBackend.get_working_dir()` now polls for the directory with a 120s timeout instead of calling `os.chdir` unconditionally. Python waits for Go to finish rsync before proceeding.

**Code:** `runtime/monitor/sandbox-setup.py::TartBackend.get_working_dir`

---

### Tart: `:copy` diff after restart shows 'No changes'

**Symptom:** On the Tart backend with a `:copy` workdir, `yoloai diff` after a `restart --prompt "…writes a file…"` reports "No changes" even though the agent demonstrably created the file. Reproduces in the smoke test as `stop_start/tart` failing with `diff after restart: expected 'output2.txt' in output / got: No changes`. Racy — frequent but not every run; the no-restart `full_workflow/tart` path passes because a cold first boot is slow enough to hide it.

**Explanation:** A baseline/agent ordering race. The diff baseline is the git commit created by `ExecuteVMWorkDirSetup` (host side): `mkdir` → `rsync` (original files only, no `--delete`) → `git init && git add -A && git commit`, run *after* `LaunchContainer` returns. But `LaunchContainer` only boots the VM — it does **not** launch the agent. The VM's own entrypoint (`sandbox-setup.py`) launches the agent and delivers the prompt asynchronously, gated only on `get_working_dir()` returning. The previous gate ([above](#tart-vm-workdir-setup-races-python-startup)) waited for the *directory* to exist, which happens after the host's `mkdir`/`rsync` but **before** the `git commit`. So on a fast clone-boot restart the agent launches, receives the prompt, and writes `output2.txt` before the baseline commit runs; `git add -A` then bakes `output2.txt` into the baseline, and `git diff <baseline>` shows nothing.

Autopsy timeline signature: `hook.idle` (agent finished writing) lands a few seconds *before* `sandbox.restart.complete` (the `ExecuteVMWorkDirSetup` baseline commit), confirming the commit raced behind the agent.

**Fix:** `TartBackend.get_working_dir()` now, for `:copy` workdirs, keeps polling after the directory exists until a committed `HEAD` resolves (`git -C <workdir> rev-parse HEAD` succeeds) — the exact "baseline ready" signal, since the commit is `ExecuteVMWorkDirSetup`'s last step. Gated on copy mode via the `copy_dirs` config key (non-empty iff the workdir is `:copy`); non-copy workdirs have no git repo and must not wait. The secrets-consumed gate ([deadlock entry](#tart-signal_secrets_consumed-must-run-before-get_working_dir)) is unaffected — `signal_secrets_consumed()` still runs before this wait, so the host always reaches and completes the baseline commit regardless of the VM.

**Code:** `runtime/monitor/sandbox-setup.py::TartBackend.get_working_dir` (and `_baseline_committed`)

---

### Tart: commit-level git (`diff <sha>`, commit listing) must dispatch through the runtime, not host git

**Symptom:** On a Tart `:copy` sandbox, `yoloai diff <sha>` / a commit-range diff and the per-commit stat listing fail with `unknown revision or path not in the working tree` / `not a git repository` — for the *same* commit SHAs that plain `yoloai diff` and commit listing just reported as present.

**Explanation:** Tart runs the sandbox work copy on **VM-local storage** and creates the baseline + agent commits **inside the VM** (`tart.Runtime.GitExec` translates the host workdir to a VM path like `/Users/admin/yoloai-work/<enc>` and execs git in the guest). Reads routed through the runtime (`git.NewSandbox` → `GitExecer`) see those commits; reads that run **host** git (`git.NewHost`) on the host workdir do not — that host path is the stale staging copy, or not a repo carrying those SHAs at all. `ListCommitsBeyondBaseline` and `GenerateDiff`'s `:copy` branch correctly used the sandbox scope, but `GenerateCommitDiff` and `ListCommitsWithStats` used a *host* runner to stat/diff the very commits the sandbox scope had just found, so they errored on VM-side SHAs. Bind-mount backends (Docker/Podman/Containerd) work because their work copy *is* on the host. Same root cause as the [host-side change-probe](#a-host-side-change-probe-is-blind-to-the-in-vm-workdir--info-showed-changes-no-on-a-dirty-tart-sandbox-and-destroy-skipped-its-gate) and [`:copy` diff after restart](#tart-copy-diff-after-restart-shows-no-changes) entries.

**Fix:** Any git operation on the sandbox *work copy* must use `git.NewSandbox(layout, rt, name)` — it dispatches in-VM for Tart and falls back to host exec for non-`GitExecer` backends, so it's a no-op for Docker/Podman/Containerd — never `git.NewHost`. Reserve `git.NewHost` for genuinely host-resident targets (the user's original directory in apply, `:rw` live mounts, dirty-checks of source dirs). `GenerateCommitDiff` gained a `Runtime` field on `CommitDiffOptions` to carry the dispatch.

**Non-obvious gotcha (and second half of the fix):** threading `e.runtime` is not enough — the Engine opens its backend *lazily*, so the backend-bound Engine method must call `e.TryEnsure(ctx)` first or `e.runtime` is still `nil` and `NewSandbox(nil, …)` silently falls back to host git (failing on the VM-local path). Every sibling Engine method (`ListCommitsWithStats`, `ListCommits`, `WorkdirTags`, …) does this; `GenerateCommitDiff` originally did not because it predated needing a runtime. The host-side `ListCommitsWithStats` worked while `GenerateCommitDiff` failed precisely because only the former called `TryEnsure`.

**Validated:** confirmed on a real macOS Tart host — `yoloai diff <sha>` and `diff --log --stat` on a Tart `:copy` sandbox both return correct output after the fix (they errored before).

**Code:** `internal/sandbox/patch/diff.go::GenerateCommitDiff` / `ListCommitsWithStats`; `internal/sandbox/engine_workdir.go::Engine.GenerateCommitDiff` (the `TryEnsure` call); the host-vs-sandbox scope distinction lives in `internal/git` (`NewHost` vs `NewSandbox`).

---

### VS Code CLI: file keychain uses hostname in encryption key

**Symptom:** VS Code tunnel re-prompts for GitHub/Microsoft login on every `yoloai restart`, even though `~/.yoloai/vscode-cli/token.json` exists and the machine-id is stable. `code tunnel user show --verbose` prints "Using file keychain storage" but then "not logged in".

**Explanation:** VS Code CLI encrypts the stored credential using AES with a key derived from the container hostname. Docker assigns the container ID as the hostname, so every new container gets a different hostname — making the token from the previous container undecryptable. `DBUS_SESSION_BUS_ADDRESS=disabled:` (the previous workaround) correctly triggers file-based storage, but does not prevent the hostname-based key rotation; the token is written in one container and silently rejected in the next.

The VS Code CLI binary exposes two undocumented env vars that fix this:
- `VSCODE_CLI_USE_FILE_KEYCHAIN=1` — forces file-based storage explicitly (bypasses D-Bus check entirely, cleaner than relying on D-Bus failure as a side-effect).
- `VSCODE_CLI_DISABLE_KEYCHAIN_ENCRYPT=1` — disables AES encryption of the stored token, making the file portable across hostname changes.

**Fix:** `sandbox-setup.py::launch_vscode_tunnel` sets both env vars in the tunnel launch command instead of `DBUS_SESSION_BUS_ADDRESS=disabled:`.

**Note:** After upgrading, delete `~/.yoloai/vscode-cli/token.json` and re-authenticate once. The old token was encrypted; it cannot be read by the new code. Subsequent restarts will use the unencrypted token and skip the login prompt.

**Code:** `runtime/monitor/sandbox-setup.py::launch_vscode_tunnel`

---

### VS Code CLI: singleton lock blocks concurrent tunnels

**Symptom:** Starting a second sandbox with `--vscode-tunnel` while another is already running loops forever with:
```
warn error access singleton, retrying: the process holding the singleton lock file (pid=120) exited
```

**Explanation:** VS Code CLI uses a file lock (`tunnel-stable.lock`) to enforce a single tunnel instance per data directory. When all sandboxes share the same `~/.yoloai/vscode-cli/` directory, the first sandbox acquires the lock via `flock(2)`. The second sandbox detects that the recorded PID no longer exists in *its* PID namespace, but cannot acquire the `flock` because the first sandbox's process still holds it from the host filesystem. VS Code CLI retries indefinitely.

**Fix:** Each sandbox now gets its own per-sandbox vscode-cli data directory (`~/.yoloai/sandboxes/<name>/vscode-cli/`). The lock, tunnel config, and server binary are all sandbox-local. To avoid requiring re-authentication for every new sandbox, `token.json` is seeded from the global credential store (`~/.yoloai/vscode-cli/token.json`) when the per-sandbox directory is first created.

**Code:** `sandbox/create.go::buildMounts` (vscodeTunnel section)

---

### Tart: `signal_secrets_consumed` must run before `get_working_dir`

**Symptom:** `yoloai new` times out ("command timed out") on the Tart backend.
`sandbox.jsonl` shows setup events up to `tart.xcode.firstlaunch.started` then
stops; `monitor.jsonl` is empty (agent never launched). The host log shows
"secrets-consumed marker not observed before timeout".

**Explanation:** A deadlock between the host and the in-VM setup script:

1. `buildAndStart()` (host) calls `waitForSecretsConsumed(timeout)`, blocking
   `launchContainer()` until the in-VM script writes `logs/.secrets-consumed`.
2. `executeVMWorkDirSetup()` (rsync that creates the VM-local working dir) runs
   only *after* `launchContainer()` returns — so the working dir never exists
   while the host is waiting.
3. `get_working_dir()` (in-VM) polls for the working dir for up to 120 s.
4. `signal_secrets_consumed()` (in-VM) was called *after* `get_working_dir()`.

Neither side could proceed: host waiting for the VM marker, VM waiting for the
host rsync, host waiting for the VM marker …

With a short `SecretsConsumedTimeout` (30 s) the host accidentally broke the
deadlock by giving up and letting `launchContainer` return. With 180 s the
smoke test's 120 s command timeout fires first.

**Fix:** `signal_secrets_consumed()` now runs *before* `get_working_dir()` in
`sandbox-setup.py::main()`. Secrets are available immediately (copied during
`Create()` via `copySecretsToSandbox()`). The tmux session does not exist yet,
so `tmux set-environment` is skipped; secrets reach the agent via the explicit
`env_exports=` prefix in `launch_agent()::send-keys` instead.

**Code:** `internal/runtime/monitor/sandbox-setup.py::main` (ordering of
`read_secrets` / `signal_secrets_consumed` vs `get_working_dir`);
`internal/sandbox/create.go` (ordering of `launchContainer` vs
`executeVMWorkDirSetup`).

---

### Tart: transient FS/PATH failure makes tmux unresolvable during the firstlaunch window

**Symptom:** `sandbox-setup.py` crashes with `FileNotFoundError: [Errno 2] No such
file or directory: 'tmux'` inside `setup_tmux_session`. The Tart VM is booted and
running; tmux **is** installed (`/opt/homebrew/bin/tmux` exists in the base image);
the crash is intermittent. In `sandbox.jsonl` the `tmux.start` event lands within a
couple of seconds of `tart.xcode.firstlaunch.started` — i.e. tmux is started while
`xcodebuild -runFirstLaunch` is still running, not after it completes.

**Explanation:** the security-scan storm that `xcodebuild -runFirstLaunch` triggers
transiently hides tmux from **both** `shutil.which("tmux")` (PATH lookup) **and**
`os.path.isfile("/opt/homebrew/bin/tmux")` (a direct stat — so this is not merely a
PATH-search problem). Any single resolution sampled inside this window misses a tmux
that is genuinely on disk. The window is timing-dependent, which is why the same
binary both passes and fails across runs (confirmed: commit `a10ab70` passed run
171245 and failed runs 202401/204935).

**Why the earlier "resolve at import time" fix (802ab22) did not hold:** moving
resolution to module-import time made it *worse* — import is the earliest possible
moment, landing it squarely in the firstlaunch window and then **freezing** the bad
`"tmux"` sample for the whole process. The pre-802ab22 call-time resolution
"accidentally" worked when the call happened to fall after the window cleared.
Neither *where* you resolve matters; the deciding factor is whether tmux is reachable
*at the sampled instant*.

**Fix:** `tmux_io.tmux_bin()` resolves lazily at call time **with bounded retry** —
re-probing `shutil.which` + the Homebrew/system fallback paths every 1s, caching the
first success and **never** caching the literal `"tmux"` fallback (so one transient
miss can't poison later calls). The happy path resolves on the first probe and never
sleeps. Both tmux call sites in `sandbox-setup.py` (`setup_tmux_session` and the
post-launch `wait-for` block) go through `tmux_io.tmux_bin()`.

**Why a fixed 30×1s budget was not enough (the recurrence):** the scan storm lasts
**as long as firstlaunch runs** (60-120s+), not a fixed number of seconds. A 30s
budget sampled at the start of the window expires *mid-storm* and falls back to the
literal `"tmux"`, crashing exactly as before (observed: run `20260529-031518` on a
build that already had the bounded-retry fix — `tmux.start` landed ~32s after
`firstlaunch.started`, inside the still-open window). The budget was guessing the
window's duration instead of observing it.

**Why gating on firstlaunch *completion* also failed (second recurrence):** an
intermediate fix bracketed the window with `.started`/`.done` marker files —
`tmux_bin()` re-probed while `.started` existed and `.done` did not, then dropped to
the bounded 30×1s retry as a "tail" once `.done` appeared. This still crashed
(observed: run `20260529-050323` on commit `18117bc`, which already had the marker
fix — `tmux.start` landed **62.6s** after `firstlaunch.started`, with
`xcodebuild-firstlaunch.log` showing `Install Succeeded`). The flaw: the `.done`
marker fires when the **xcodebuild process exits**, but the security scan that hides
tmux **tails off well after** that. So `.done` closed the window early, the 30s
"tail" grace ran out while tmux was still hidden, and we fell to the literal `"tmux"`.
Gating on xcodebuild completion underestimates the storm just like the fixed budget
did — it was simply a less-wrong guess.

**Fix that holds — probe to a long ceiling, ignore completion:** the Tart setup path
writes a single `xcodebuild-firstlaunch.started` marker and registers it with
`tmux_io.set_firstlaunch_marker()`. While that marker exists, `tmux_bin()` probes
once per second until tmux resolves **or** the 240s hard ceiling is hit — it does
**not** stop when xcodebuild finishes, because completion is not a reliable "tmux is
back" signal. On every non-Tart backend (no marker registered) resolution uses the
bounded 30×1s retry. The early-exit-on-completion was pure downside: when tmux is
present the very first probe returns it, so dropping the optimisation costs nothing on
the happy path and removes the premature give-up. The `.done` marker and its
`sh -c '… ; : > "$1"'` wrapper are gone.

**Code:** `internal/runtime/monitor/tmux_io.py` (`tmux_bin`, `_probe_tmux_bin`,
`_in_firstlaunch_context`, `set_firstlaunch_marker`, `_RESOLVE_ATTEMPTS`,
`_FIRSTLAUNCH_MAX_WAIT_SECONDS`); `internal/runtime/monitor/sandbox-setup.py`
(firstlaunch launch in `TartBackend.setup()`, plus `setup_tmux_session` and the
`main()` `wait-for` block).

---

## Smoke harness (agent task execution)

### Agent stalls when the sentinel command errors

**Symptom:** a `stop_start` smoke test fails with "agent idle
for Ns+ … no progress past sentinel 'done'", and `fingerprint: null` (before this
entry existed). The same test passes on other backends in the same run. The
preserved `terminal-snapshot.txt` shows the agent *did* receive the prompt and *did*
attempt the command, but Claude Code rendered a tool error — e.g.

```
⏺ Bash(touch …/files/in-progress && echo smoke > output.txt && mv …)
  ⎿  Error: Exit code 64
     usage: mv [-f | -i | -n] [-hv] source target
```

— after which the agent printed a clarifying question ("Did you mean… / Could you
verify the complete command?") and went idle waiting for a human. The exchange dir
holds `in-progress` but never `done`.

**Explanation:** this is an **agent-side** failure, not an infra/backend fault. The
product worked: the prompt was delivered intact (visible verbatim in the pane), the
filesystem was writable (`in-progress` was created, `output.txt` written, a git
commit made). What failed is the agent's own tool call — the model dropped the `mv`
*target* path when constructing the `Bash` tool input, so `mv` got one argument and
exited 64. Observed with the small `haiku-4-5` model on a long single-line command
carrying two ~90-char absolute paths; larger models on the same prompt completed it.
It is intermittent and model-dependent, which is why only some backends in a run trip
it even though the prompt is identical everywhere.

**Fix / triage:** there is nothing to fix in the backend. The smoke harness now
fingerprints this signature (it scans `terminal-snapshot.txt` for Claude Code's
`Error: Exit code N` tool-error block) so triage immediately reads "agent garbled the
sentinel command" instead of hunting for an infra bug. If it recurs often enough to
be noise, make the smoke prompt easier for a small model to execute exactly — e.g.
`cd` into the exchange dir and use relative `in-progress`/`done` names instead of two
long absolute paths on one line.

**Code:** `scripts/smoke_test.py` (`FINGERPRINTS` entry "agent's sentinel command
failed; agent stalled", and `_autopsy_artifact_files` now includes
`terminal-snapshot.txt`).

---

### Kata: shim resolves a sandbox from the container ID by *prefix*; prefix-related names collide

**Symptom:** under the parallel smoke matrix, `stop_start/containerd-vm` (or any
containerd-vm op) fails at task creation with:

```
start instance: create task: failed to create shim task: Others("failed to handle
message try init runtime instance ... Failed to create shim management server ...
more than one sandbox exists with the provided prefix
"yoloai-smoke-…-stop-start-containerd-vm", please provide a unique prefix")
```

The same op passes when run serially. The trigger is that the failing sandbox's
name is a **string prefix of another sandbox that is alive at the same time** —
here `…-containerd-vm` is a prefix of `…-containerd-vmenhanced`, and the parallel
matrix runs both Kata backends concurrently.

**Explanation:** yoloAI passes the instance name (`InstanceName`,
`internal/sandbox/store/paths.go`) verbatim as the containerd container ID and does
**no** prefix matching of its own. The prefix lookup is **entirely inside the
external Kata shim** (`containerd-shim-kata-v2` / runtime-rs): given a full
container ID it scans its sandbox store for entries that *start with* that ID and
refuses to proceed if more than one matches. Two coexisting sandboxes where one
name is a prefix of the other are therefore indistinguishable to the shim. Docker
and containerd's own container lookup are exact-match, so `docker` /
`docker-cenhanced` (also prefix-related) do **not** trip this — only the Kata VM
backends do.

**Fix (smoke harness):** sandbox names now carry a monotonic per-run sequence
suffix (`…-containerd-vm-007`), so no name is a prefix of another. The suffix
breaks the relationship because the plain name continues with `-` exactly where
the enhanced name continues with `e`. See `Test.sandbox()` in
`scripts/smoke_test.py`.

**Implication for real users:** this is a genuine containerd-vm limitation, not
just a test artifact — two *running* containerd-vm sandboxes whose names are
prefix-related (e.g. `app` and `app-v2`) will collide the same way. yoloAI does
not currently guard against this at create time; if it becomes a real-world papercut,
the fix would be a create-time check that rejects or warns on a prefix-related
live sandbox name for the Kata backend.

**Code:** `scripts/smoke_test.py` (`RunContext.name_seq`, `Test.sandbox()`);
ID construction in `internal/sandbox/store/paths.go` (`InstanceName`).

---

## yoloai host-side (locks, prune)

### Removing a `.lock` file while holding its flock is safe

**Symptom / concern:** `store.RemoveLockFile` and `SweepStaleLocks` `os.Remove`
a `<name>.lock` file *while the process still holds the flock on it*. This looks
like it should break mutual exclusion or error out.

**Explanation:** `flock(2)` is advisory and binds to the **open file
description (the fd), not the path**. Unlinking the path doesn't release the
lock — the holder keeps it until the fd closes. A concurrent acquirer that
re-creates `<name>.lock` gets a **fresh inode** and its own independent lock, so
removal-while-held can never hand two processes the same lock. The stale-lock
sweep relies on the inverse: it try-acquires (`locking.AcquireNonBlocking`) and
**skips on `ErrWouldBlock`**, so a file with a live holder is never removed; only
genuinely orphaned lock files (no holder) are swept.

**Consequence for design:** lock files can be removed eagerly on the happy path
(`Destroy`, failed `Create` rollback) without a PID check, and the prune sweep is
safe to run concurrently with live sandboxes. Lock files therefore don't
accumulate, and `system prune` only ever removes the truly-orphaned ones.

**Code:** `internal/sandbox/store/lock_unix.go` (`RemoveLockFile`,
`SweepStaleLocks`); `internal/sandbox/lifecycle.go` (Destroy);
`internal/sandbox/create.go` (Create rollback).

---

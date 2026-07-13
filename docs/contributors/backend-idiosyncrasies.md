# Backend Idiosyncrasies

Durable facts about **external tooling and platforms we depend on but cannot
change** — containerd, Kata, runc/gVisor, CNI plugins, iptables, the Docker and
Podman engines and their Go SDKs, Tart, QEMU/Firecracker, macOS (`sandbox-exec`,
Virtualization.framework, VirtioFS, CoreSimulator), the Go stdlib, POSIX, and
the like — where the tool contradicts its own docs, behaves surprisingly, or
forced us into a non-obvious workaround. Use this as the first reference when a
backend misbehaves.

**The inclusion test — does an entry belong here?**

> Is the root-cause behavior in an external tool/OS we cannot fix? → **document it here.**
> Is it our own code being wrong, which we have the power to just fix? → **it does not belong.**

The entry earns its place only when the *surprise lives outside our codebase*.
Writing our own workaround does not change that — the entry documents the
external constraint that forced the workaround, not the workaround itself. A
useful tell: **if you deleted every line of yoloAI's code, would the surprising
behavior still exist out in the tool?** If yes, it belongs here. If the behavior
exists only because of how *we* wired things, it does not.

What does **not** belong: a bug we wrote and fixed, an ordering or race in our
own startup sequence, a wrong return type or discarded value in our own code, an
invariant of our own architecture. We control that code, so those lessons live
in code comments, the decision log (`decisions/`), or git history — not here. If
the only takeaway is "don't write our own code wrong," it is not an idiosyncrasy.

**How to use:** scan the symptom index below to find the relevant section, then
read the full entry for context and the fix. When you add an entry, apply the
inclusion test first, then add a row to the index.

---

## Symptom Index

| Symptom / error message | Section |
|---|---|
| Apple sandbox: TUI loses left gutter / leading chars orphan onto row above, only on tmux scroll; `^b r` heals it; Docker clean; both emulators affected | [Apple: exec -t forces ONLCR, corrupting column tracking](#apple-container-exec--t-forces-onlcr-on-the-host-local-bridge-pty-corrupting-the-apps-column-tracking-on-scroll) |
| Brokered agent on podman-macOS hangs on first API call; one-shot curl to the injector works | [Podman Machine: gvproxy stalls streaming](#podman-machine-macos-gvproxy-host-forward-passes-a-one-shot-curl-but-stalls-the-agents-streaming-connection) |
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
| Task stays in `Created` after `Start()` returns | [Containerd: task.Start returns early](#taskstart-returns-before-the-vm-is-actually-running) |
| `OCI runtime exec failed: ... procReady not received` on exec/Launch | [Docker: procReady usually means the container is dying, not broken runc](#docker-procready-not-received-usually-means-the-container-is-exiting-not-a-broken-runtime) |
| `parent snapshot sha256:... does not exist: not found` | [Containerd: WithNewSnapshot doesn't unpack](#withnewsnapshot-does-not-unpack-image-layers) |
| `docker save \| ctr import` hangs indefinitely | [Containerd: pipe hang on ctr failure](#docker-save--ctr-import-hangs-if-ctr-fails-early) |
| Containerd socket: no error from `os.Stat` despite permission denied | [Containerd: Stat can't detect EPERM](#osstat-on-the-containerd-socket-does-not-detect-permission-denied) |
| Containerd GC removes blobs; image becomes unrunnable | [Containerd: GC removes child blobs](#containerd-gc-removes-child-blobs-while-leaving-the-root-manifest-intact) |
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
| Apple: `container system df` reports `containers.reclaimable: 0` despite a multi-GB build cache; `prune` seems to free nothing | [Apple: build cache lives inside the running builder container](#apple-build-cache-lives-inside-the-running-builder-container-invisible-to-system-dfs-reclaimable-field) |
| `system prune` finds a different dangling image every run, reclaims 0 B, never converges, even with no builds | [Docker: legacy builder leaves a dangling image per step; build with BuildKit](#docker-legacy-builder-commits-one-dangling-intermediate-image-per-dockerfile-step-build-with-buildkit) |
| Smoke/`system build` rebuilds `yoloai-base` from scratch every run on Docker Desktop (not OrbStack), though the image is present | [Docker Desktop: ImageInspect transiently NotFounds a present image on the idle containerd store](#docker-desktop-imageinspect-transiently-notfounds-a-present-image-on-the-idle-containerd-store) |
| Every `new`/`start`/`clone` fails on Docker Desktop (passes on OrbStack/Linux) with `substrate not ready within 30s`; container log skips `entrypoint.keepalive_only`, no `.substrate-ready` | [Docker Desktop: single-file bind mount serves stale content after atomic rename](#docker-desktop-a-single-file-bind-mount-serves-stale-content-after-the-host-atomic-renames-it-keepalive_only-never-reaches-the-entrypoint) |
| `podman: build cache prune failed: Error response from daemon: Not Found` | [Podman: no build-cache endpoint (404)](#podman-docker-compat-api-has-no-build-cache-endpoint--buildcacheprune-returns-404-not-found) |
| Long-lived `docker exec` (attached) process dies when the launching CLI exits; status-monitor / marker missing | [Docker: attached exec doesn't outlive its client](#docker-exec-an-attached-exec-does-not-outlive-the-client-that-started-it) |
| `prune --images` on Podman reports absurd reclaim (e.g. 142 GB freed for a ~5 GiB footprint) | [Podman: `ImagesPrune` `SpaceReclaimed` un-dedup sum](#podman-imagesprune-spacereclaimed-is-the-un-deduplicated-image-size-sum) |
| `prune --images` dry-run promises multi-GB reclaim but `reclaimed 0 B`, while a `yoloai` sandbox is still running | [Docker/Podman: running containers pin image layers; warn at dry-run](#dockerpodman-imagesprune-cant-remove-images-held-by-non-stopped-containers-the-dry-run-must-name-the-blockers) |
| `prune --images` leaves a snapshot chain; `Remove` → `cannot remove snapshot with child` | [containerd: remove snapshots leaf-first](#containerd-snapshots-must-be-removed-leaf-first-children-before-parents-or-removal-silently-stalls) |
| `system disk` reports 0 containerd image bytes right after a successful `system build --backend containerd` | [containerd: import inconsistently materializes snapshots](#containerd-image-import-inconsistently-materializes-overlayfs-snapshots) |
| Base layer won't prune (`cannot remove snapshot with child`) but no snapshot claims it as parent in any namespace | [containerd: leftover lease GC-roots an orphaned child](#containerd-a-leftover-lease-gc-roots-an-orphaned-child-blocking-base-layer-removal) |
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
| Intermittent podman `start instance: instance not found` (esp. after an interrupted build) | [Podman: interrupted build leaves mounted buildah working-containers](#podman-an-interrupted-build-leaves-mounted-buildah-working-containers-that-wedge-container-createstart) |
| `tart exec` with `--` separator fails silently or returns exit status 1 | [Tart: no support for -- separator](#tart-exec-does-not-support----argument-separator) |
| `yoloai attach` fails with "no sessions" on Tart VM | [Tart: exec -t changes environment](#tart-exec--t-changes-environment-preventing-tmux-from-finding-socket) |
| Agent renders ASCII on Tart (logo `_______`, emoji as `_`) despite healthy TERM/locale | [Tart: attach renders ASCII (non-UTF-8 tmux client)](#tart-attach-renders-ascii-tmux-downgrades-a-non-utf-8-client) |
| `xcrun simctl list runtimes` shows no runtimes when mounted via VirtioFS | [Tart: CoreSimulator requires sealed APFS](#coresimulator-cannot-discover-virtiofs-mounted-runtimes) |
| `Failed to start launchd_sim: could not bind to session` when booting simulator | [Tart: ditto'd runtime is incomplete](#dittod-ios-runtime-is-incomplete-use-xcodebuild--downloadplatform) |
| In-VM iOS runtime download is slow; does the cirruslabs Xcode base already include a simulator? | [Tart: cirruslabs Xcode base bakes in the default runtime](#cirruslabsmacos--xcode-base-images-already-bake-in-the-default-ios-runtime--the-in-vm-download-is-redundant-for-it) |
| `iOS X.Y is not installed … install from Xcode > Settings > Components` sporadically on cirruslabs Xcode base | [Tart: cirruslabs Xcode base bakes in the default runtime](#cirruslabsmacos--xcode-base-images-already-bake-in-the-default-ios-runtime--the-in-vm-download-is-redundant-for-it) |
| `git diff` fails with "unable to read" object / git corruption on Tart VM | [Tart: VirtioFS corrupts git repositories](#virtiofs-corrupts-git-repositories) |
| Tart `info` shows `Changes: no` on a dirty sandbox; `destroy` skips the unapplied-work gate | [Tart: host change probe blind to in-VM workdir](#a-host-side-change-probe-is-blind-to-the-in-vm-workdir--info-showed-changes-no-on-a-dirty-tart-sandbox-and-destroy-skipped-its-gate) |
| Agent silently fails to start on Tart (claude/node not found) | [Tart: provisioned tool dirs live only on the login PATH](#provisioned-tool-dirs-live-only-on-the-login-path-cirrus-base-image) |
| Agent on a long-idle Tart sandbox: `ConnectionRefused`/`FailedToOpenSocket` on every API call; `tart ip` finds nothing; guest `en0` is `169.254.x.x` | [Tart: vmnet session wedges on a long-idle VM](#tart-vmnet-session-wedges-on-a-long-idle-vm-host-sleep--subnet-re-pick--guest-drops-to-a-169254-link-local-address-agent-gets-connectionrefused) |
| Swift PM commands fail with sandbox-exec nesting errors on Seatbelt | [Seatbelt: macOS sandbox-exec doesn't nest](#macos-sandbox-exec-doesnt-nest--swift-pm-needs-the-swift-wrapper-sourced) |
| Agent dies silently/SIGTRAP (exit 133) on Seatbelt at launch; ICU/timezone deny in unified log | [Seatbelt: SBPL subpaths need vnode-resolved paths](#agent-dies-silently-sigtrap--sbpl-subpath-rules-must-use-vnode-resolved-paths) |
| Confined git under `sandbox-exec` dies (`xcrun_db` / `libxcrun` denied); or a malicious filter still writes to `/tmp`; or git works with `mach-lookup` denied | [Seatbelt: sandbox-exec-wrapping git — escape surfaces + the /usr/bin/git shim](#seatbelt-sandbox-exec-wrapping-git-for-confinement-has-two-escape-surfaces-mach-lookup-process-exec--the-usrbingit-shim-cant-run-confined) |
| Interactive error output "stair-steps" (each line shifts right) on Seatbelt/Tart | [Seatbelt/Tart: local-PTY backends must bridge, not inherit host stdio](#interactive-error-output-stair-steps--local-pty-backends-must-bridge-not-inherit-host-stdio-also-tart) |
| VS Code tunnel re-prompts for login on every container restart | [VS Code CLI: hostname-based keychain encryption](#vs-code-cli-file-keychain-uses-hostname-in-encryption-key) |
| Second sandbox tunnel loops `error access singleton` forever | [VS Code CLI: singleton lock blocks concurrent tunnels](#vs-code-cli-singleton-lock-blocks-concurrent-tunnels) |
| DNS works but HTTPS to api.anthropic.com times out | [DNS: timeout = API unreachable, not DNS](#request-timed-out-in-claude-code--api-unreachable-not-dns-failure) |
| `iptables` warnings about legacy tables | [iptables-nft: legacy tables warning](#iptables--iptables-nft-both-iptables-legacy-and-iptables-nft-can-coexist) |
| `Can't open socket to ipset` / network isolation fails on Podman macOS | [Podman macOS: iptables-nft lacks xt_set module](#podman-macos-iptables-nft-lacks-xt_set-module-ipset-unusable) |
| `no podman socket found` on macOS though `podman machine` is running (any command: `system build`, `new`, …) | [Podman macOS: socket discovery needs TMPDIR](#macos-podman-machine-socket-discovery-needs-tmpdir-without-it-inspect-reports-a-stale-tmp-path) |
| Smoke test: `stop_start/containerd-vm` fails with "agent idle for 9s+" | [QEMU: slow startup exceeds stall grace](#qemu-slow-startup-exceeds-smoke-test-stall-grace-period) |
| `diff`/`apply`/`status` fails only on containerd-vm with `git: detected dubious ownership in repository` | [Kata: virtiofs remaps work-copy uid; in-confinement git trips dubious-ownership](#kata-virtiofs-remaps-the-work-copy-owner-uid-so-in-confinement-git-trips-dubious-ownership) |
| Smoke test: `stop_start/tart` fails; exchange dir empty | [Tart: xcodebuild -runFirstLaunch blocks agent startup](#tart-xcodebuild--runfirstlaunch-blocks-agent-startup) |
| Smoke `done` never fires; claude stuck on a Bash permission prompt despite `--dangerously-skip-permissions`; "fullscreen renderer" modal seen | [Claude: fullscreen upsell re-execs and drops the flag](#claude-the-fullscreen-renderer-upsell-re-execs-claude-and-drops---dangerously-skip-permissions) |
| `container-enhanced` (gVisor): `new` exits 0 / `ls` active but agent never runs; box stuck on `sleep infinity`, only `entrypoint.keepalive_only` logged | [gVisor: docker exec --user resolves stale image passwd](#gvisor-container-enhanced-docker-exec---user-name-resolves-against-the-stale-image-passwd-not-the-live-one) |
| `yoloai new --attach` hangs after "Sandbox created"; Python setup never completes | [Tart: mount_map uses Docker paths, triggering macOS automount](#tart-mount_map-uses-docker-style-paths-triggering-macos-automount-hang) |
| `yoloai apply` fails: `git add: git [add -A]: exit status 128: … index.lock: File exists` while agent is running | [Docker/Podman: agent git and apply git race on index.lock](#dockerpodman-agent-git-and-apply-git-race-on-indexlock) |
| `FileNotFoundError: 'tmux'` in `sandbox-setup.py::setup_tmux_session` on Tart VM (intermittent) | [Tart: transient FS/PATH failure makes tmux unresolvable during the firstlaunch window](#tart-transient-fspath-failure-makes-tmux-unresolvable-during-the-firstlaunch-window) |
| `VM work dir setup: get baseline SHA: exec exited with code 69: You have not agreed to the Xcode license agreements` on Tart `new` (intermittent, passes on retry) | [Tart: transient FS/PATH failure makes tmux unresolvable during the firstlaunch window](#tart-transient-fspath-failure-makes-tmux-unresolvable-during-the-firstlaunch-window) |
| Smoke test: `stop_start` fails "agent idle"; pane shows `Error: Exit code N` + a clarifying question; other backends pass | [Smoke harness: agent stalls when the sentinel command errors](#agent-stalls-when-the-sentinel-command-errors) |
| `create task: ... more than one sandbox exists with the provided prefix "..."` (containerd-vm, under concurrency) | [Kata: shim resolves sandboxes by name prefix](#kata-shim-resolves-a-sandbox-from-the-container-id-by-prefix-prefix-related-names-collide) |
| `create task: failed to create shim task: ttrpc: closed` on **restart** (Stop then Start) of a containerd/Kata sandbox | [containerd: restart must re-create the netns Stop tore down](#containerd-restart-stopstart-must-re-establish-the-netns-that-stop-tore-down) |
| Is it safe to delete a `.lock` file while holding its flock? (prune / Destroy) | [Removing a .lock file while holding its flock is safe](#removing-a-lock-file-while-holding-its-flock-is-safe) |
| Tart base build / `tart run` fails with `The number of VMs exceeds the system limit` or VM self-stops at boot, but `tart list` shows nothing running | [Tart: orphaned Virtualization VM processes consume the macOS VM limit](#orphaned-virtualization-vm-processes-survive-a-crashed-tart-run-and-silently-consume-the-macos-vm-limit) |
| `tart delete <name>` fails with `instance not found` for a VM that exists (e.g. `delete old base: instance not found` during base promote) | [Tart: delete of a running VM reports "instance not found"](#tart-delete-of-a-running-vm-fails-with-a-misleading-instance-not-found-stop-first) |
| Smoke `stop_start`/`tag_transfer` fails **only on Tart**: `zsh: no such file or directory: /yoloai/bin/agent-run.sh`, pane dead (127); `setup.log` shows `NameError: log_error` | [Tart: fall-to-shell wrapper path must derive from yoloai_dir](#tart-the-fall-to-shell-wrapper-path-must-derive-from-yoloai_dir-not-the-container-yoloai) |
| `system disk` shows tart `IMAGES: ?` / `CACHE: 0 B` despite GBs in `~/.tart`; `prune --images` reports 0 reclaimed | [Tart: list double-counts OCI tag+digest; sizing/prune must dedup](#tart-list-reports-a-pulled-oci-image-twice-tag--digest-over-one-on-disk-copy-sizing-and-prune-must-dedup-and-remove-both-rows) |
| macOS `docker` numbers don't match Docker Desktop assumptions (overlay2/btrfs, classic store) | [Docker on macOS may be OrbStack, not Docker Desktop](#docker-on-macos-may-be-orbstack-not-docker-desktop--docker-info-clientinfocontext-tells-you-which) |
| Podman macOS reports image bytes correctly even though the Linux `LayersSize: 0` workaround exists | [Podman: `/system/df` reports `LayersSize: 0`](#podman-systemdf-reports-layerssize-0) (macOS/version caveat) |
| `system disk` shows seatbelt `IMAGES: ?` / `CACHE: 0 B` — is it a gap? | [Seatbelt has no backend image/cache store](#seatbelt-has-no-backend-imagecache-store--cacheusageprunecache-are-correctly-absent) |
| Apple `container create … --mount …` fails: `path '…' is not a directory` | [Apple: `--mount type=virtiofs` rejects file sources; use `-v`](#apple-mount-typevirtiofs-rejects-a-file-source-use--v-for-file-mounts) |
| Apple: `container build .` builds nothing / `COPY` fails (`"/x": not found`) | [Apple: `container build` drops a relative context](#apple-container-build-silently-drops-a-relative--context-pass-an-absolute-dir) |
| `podman build` → `Error: unknown flag: --provenance` / exit 125 | [Podman: build rejects docker BuildKit attestation flags](#podman-build-rejects-the-docker-buildkit-attestation-flags) |
| `idle` agent / keep-alive exits 1 with `usage: sleep number[unit]` on a macOS/Tart guest | [macOS guest BSD sleep rejects sleep infinity](#macos-guest-bsd-sleep-rejects-sleep-infinity-gnu-only) |
| `install network-isolation firewall: netns sidecar exited 2: … can't open file '/yoloai/bin/install-firewall.py'` (intermittent, under concurrent churn; file is present in image) | [Docker/OrbStack: ephemeral container transiently exposes an incomplete rootfs](#a-freshly-created-ephemeral-container-can-transiently-expose-an-incomplete-rootfs-under-heavy-concurrent-churn) |
| Same `install-firewall.py` (or any embedded-resource) error, but **deterministic** on one docker provider while another passes — file genuinely absent from that provider's image | [Docker: base-image staleness marker keyed per backend, not per provider/store](#docker-base-image-staleness-marker-was-keyed-per-backend-not-per-image-store-second-provider-runs-stale) |
| macOS `:overlay` sandbox loses the agent's uncommitted changes after `stop`/`restart`/`kill`; `yoloai diff` showed them while running | [macOS: overlayfs on a VirtioFS bind silently downgrades to a tmpfs upper (changes lost on restart)](#macos-overlayfs-on-a-virtiofs-bind-mount-silently-downgrades-to-a-container-local-tmpfs-upper-uncommitted-changes-lost-on-restart) |
| `:overlay` create on Podman-macOS crashes the entrypoint (`mount … cannot mount overlay read-only`, exit 32); container `Exited`, incomplete v3 sandbox | [macOS: overlayfs on a VirtioFS bind …](#macos-overlayfs-on-a-virtiofs-bind-mount-silently-downgrades-to-a-container-local-tmpfs-upper-uncommitted-changes-lost-on-restart) (Podman applehv variant) |
| `system migrate` of a running `:overlay` sandbox fails at dispose: `drop orig: openfdat …/ovlwork/work: permission denied` (rootful Docker) | [Linux: overlay flatten migration and host-side ownership of container-written state](#linux-overlay-flatten-migration-and-host-side-ownership-of-container-written-state) |
| `system migrate` refuses: sandbox runtime state `owned by uid 100999, not you` (podman-rootless) | [Linux: overlay flatten migration and host-side ownership of container-written state](#linux-overlay-flatten-migration-and-host-side-ownership-of-container-written-state) |
| Integration test fails only in CI's Docker job: `t.TempDir` cleanup `unlinkat …/.git/objects/…: permission denied` on podman-written files | [Linux: overlay flatten migration and host-side ownership of container-written state](#linux-overlay-flatten-migration-and-host-side-ownership-of-container-written-state) |
| arm64 base build: `link: running gcc failed` / `-fuse-ld=gold` / `cannot find 'ld'` | [Base image (trixie): gold linker split out of binutils](#base-image-trixie-gold-linker-is-a-separate-package-arm64-cgo-link-fails) |
| Base build: `pip install aider-chat` fails `setuptools.build_meta` / installs ancient 0.16.0 | [Base image (trixie): aider needs Python <3.13](#base-image-trixie-aider-chat-does-not-support-python-313-install-it-isolated-on-312-via-uv) |
| Smoke harness crashes mid-test (flaky): `UnicodeDecodeError: 'utf-8' codec can't decode byte 0xe2 … invalid continuation byte` | [tmux `capture-pane` can slice a multibyte char at the pane edge](#tmux-capture-pane-can-emit-invalid-utf-8-a-multibyte-char-sliced-at-the-pane-edge) |

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

---

### Kata shim startup: netns must be fully configured before `NewTask()`

Kata reads `eth0` from the netns at **shim startup time** (during `NewTask()`).
The Kata shim logs show `veth network interface found: eth0` with its IP and MAC.
After this point, Kata has committed to using that `eth0`; changes to the netns
veth are not reflected.

---

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

---

### `/run/kata/<name>/` persists on abnormal exit

The shim creates `/run/kata/<name>/shim-monitor.sock` at startup. If the shim
dies without cleanup, this directory persists. A subsequent shim start for the
same container name fails with `EADDRINUSE`. Must call `removeKataStateDir()`
before retrying. See `lifecycle.go::removeKataStateDir`.

---

### TTRPC shim socket: uppercase hex SHA256

Containerd's `SocketAddress()` formula for the TTRPC socket is:
`/run/containerd/s/<sha256(containerdSock + "/" + namespace + "/" + taskID)>`.
The **Kata Rust shim** formats the hash as **uppercase hex** (`%X`). The Go
shim would use lowercase. Remove both defensively.

---

### `EADDRINUSE` on `NewTask()` retry

If a shim fails after binding the TTRPC socket but before containerd registers it,
the orphaned socket file causes the next `NewTask()` to fail with `EADDRINUSE`.
The retry loop in `Start()` handles this. Kill the orphaned shim PID first, then
remove state directories, then retry.

---

### Firecracker runtime-rs: explicit config path breaks VM boot

The `io.containerd.kata-fc.v2` shim (Firecracker, runtime-rs ≥ 3.x) selects
the Firecracker VMM automatically based on the runtime type — no config path
needed. Passing `configuration-rs-fc.toml` explicitly causes the shim to
override its built-in vsock setup, resulting in "After 500 attempts" (the
kata-agent becomes unreachable and the task never reaches Running).

Fix: return `""` from `kataConfigPath()` for all runtimes, matching the
behavior of `ctr run` (which works). See `lifecycle.go::kataConfigPath`.

---

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

---

### Kata shim teardown lag: `Delete()` fails transiently after task exit

The Kata shim continues running briefly after the task exit event fires. An
immediate `container.Delete()` may fail with a transient error. Must retry with
a delay (5 attempts × 2s). See `lifecycle.go::retryDelete`.

---

### After killing orphaned shim processes, wait 500ms before proceeding

Sending `SIGKILL` to an orphaned `containerd-shim-kata` process does not
immediately release the TTRPC socket file. The OS needs approximately 500ms.
Retrying `NewTask()` too quickly still hits `EADDRINUSE`. See
`lifecycle.go::Create` and `Start`.

---

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

---

### Kata: virtiofs remaps the work-copy owner uid, so in-confinement git trips "dubious ownership"

**Symptom:** `yoloai diff`/`apply`/`status` on a `:copy` sandbox fails **only**
on the Kata VM backends (`containerd-vm`, `containerd-vmenhanced`) with
`fatal: detected dubious ownership in repository at '<work path>'` (git exit
128). The identical flow passes on docker/podman. Surfaced by the smoke test's
`stop_start/containerd-vm` `diff after restart` step.

**Why:** the copy-mode work-copy git runs *inside* the sandbox (audit C1/DF66 —
the agent-controlled `.git/config` must not run filter/diff/fsmonitor drivers on
the host). The work copy is shared into the Kata guest over virtiofs, which
presents the files under a **different uid** than the agent user git runs as, so
git's ownership guard refuses every operation. runc backends (docker/podman)
bind-mount the copy with the host uid intact and the agent user matches, so they
never trip it. This only regressed once git moved into confinement — the older
host-side git ran as the file owner, so the guard was never exercised.

**Fix in code:** `internal/git/git.go::sandboxExec.run` passes
`-c safe.directory=<in-sandbox path>` on every confined git invocation. git
honors `safe.directory` from a trusted command-line `-c` but **ignores** it when
set in the repo's own `.git/config`, so trusting the exact work path cannot be
self-authorized by the agent. The entry is a no-op on backends whose ownership
already matches (docker/podman).

**Fix for the user:** none — handled automatically. Do **not** advise
`git config --global --add safe.directory` (git's own hint): the failing git
runs inside the ephemeral sandbox, so a host-side global config would not reach
it.

---

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

---

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

---

### `hotplug memory error: ENOENT` is normal

The Kata agent logs `{"msg":"hotplug memory error: ENOENT","level":"WARN",...}` on
every boot. This is benign — it means no memory hotplug device is present, which
is expected for non-balloon-memory configurations.

## CNI (Container Network Interface)

### Rule of thumb: plugin DEL in reverse ADD order

`libcni`'s `DelNetworkList` runs plugins in **reverse** order of the conflist:
for `[bridge, portmap, firewall]` ADD order, DEL order is `firewall → portmap → bridge`.

---

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

---

### CNI results cache lives at `/var/lib/cni/results/`

Cache key: `<networkName>-<containerID>-<ifName>` (e.g. `yoloai-yoloai-foo-eth0`).
Written by `cacheAdd` after successful `AddNetworkList`. Used by `DelNetworkList`
to recover the prevResult for DEL. `cacheDel` removes it at end of successful DEL.

If teardown fails mid-way, the cache file may be left behind. A subsequent ADD
pre-flight DEL will find it and use it.

---

### `AppendUnique` does not protect against interleaved ADD/DEL

If thread A calls `AppendUnique` to add rule R, and thread B calls `Delete` to
remove rule R, and then thread A calls `AppendUnique` again for a different rule,
rule R is gone from the chain permanently (no re-check). This is not a problem
in normal sequential operation but IS a problem if two `yoloai new` calls for the
same container name run concurrently.

---

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

**Belongs here:** the silent no-op lives in the upstream CNI firewall plugin's `addRules()`; kept as a note on that external code path, though every observed firing traced back to the DF10 thread-netns bug (ours) and none has been independently reconfirmed since.

---

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

---

### `SetupIPMasq` creates a **chain jump**, not a bare MASQUERADE

The bridge plugin's `SetupIPMasq` creates a per-container chain `CNI-XXXXXXXX`
containing `ACCEPT` + `MASQUERADE` rules, then adds a POSTROUTING jump to it:
`-s <ip> -j CNI-XXXXXXXX`. A bare `MASQUERADE` rule in POSTROUTING (without a
comment or chain jump) is **not** from `SetupIPMasq`; it indicates broken state —
either a partial teardown that deleted the chain but not the POSTROUTING rule, or
a different tool wrote that rule.

---

### `TeardownIPMasq` deletes by exact match (comment included)

`TeardownIPMasq` calls `ipt.Delete("nat", "POSTROUTING", "-s", ip, "-j", chain, "-m", "comment", "--comment", comment)`.
If the comment or chain name doesn't match exactly, the rule is NOT deleted. This
can leave stale POSTROUTING rules after teardown.

---

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

## iptables-nft on Ubuntu 24.04

### `iptables` = iptables-nft; both `iptables-legacy` and `iptables-nft` can coexist

Running `iptables` actually invokes `iptables-nft`. Rules created by either tool
are stored in different nftables tables/chains BUT both can be listed by the same
`iptables` command (nft sees both). Always run `iptables` (not `iptables-legacy`)
for CNI troubleshooting; legacy rules won't affect CNI traffic since CNI uses nft.

`iptables` warns `# Warning: iptables-legacy tables present, use iptables-legacy
to see them` when both are active. Ignore this for CNI work.

---

### `iptables-save` format shows exact rule ordering

`iptables -L` reorders rules for display (e.g., show all chains). Use
`iptables-save` to see the true append/insert order in the chain.

---

### CNI-FORWARD rule ordering reflects add order

`setupChains()` calls `ensureFirstChainRule()` to insert `CNI-ADMIN` at position 1.
`addRules()` then `AppendUnique`s per-IP rules to the END of CNI-FORWARD.
Normal result: CNI-ADMIN first, then per-IP rules in creation order.

If per-IP rules appear BEFORE CNI-ADMIN in the chain, something called
`AppendUnique` before `setupChains` could insert CNI-ADMIN (i.e., the chain was
empty when `addRules` ran, then a DIFFERENT call's `setupChains` re-inserted
CNI-ADMIN at position 1, pushing the already-appended IP rules down... actually
this is impossible; see the actual cause in the "two `yoloai new`" item above).

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

---

### "Request timed out" in Claude Code = API unreachable, NOT DNS failure

When Claude Code prints `Request timed out. Retrying in 11 seconds…`, it means
the **HTTPS connection** to `api.anthropic.com` timed out. DNS might still work
(nslookup succeeds) but TCP/TLS to port 443 is dropped by the FORWARD chain.

To distinguish: run `curl --connect-timeout 5 https://api.anthropic.com/` inside
the VM. `000` = TCP timeout/refused; `4xx` = TCP connected, HTTP response received.

## Docker

### AppArmor blocks `mount(2)` even with `CAP_SYS_ADMIN`

Docker's default AppArmor profile blocks `mount()` syscalls even when
`CAP_SYS_ADMIN` is granted via `CapAdd`. Without explicitly disabling AppArmor,
the entrypoint cannot mount overlayfs inside the container and gets `EPERM`.

Workaround: add `security-opt apparmor=unconfined` whenever `SYS_ADMIN` appears
in `CapAdd`. See `docker.go::Create`. This is not advisory — the mount literally
fails otherwise.

---

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

**Code:** `orchestrator/create_instance.go` case `container-privileged`.

---

### Docker SDK `Exec` strips the trailing newline

`ContainerExecAttach` + `stdcopy.StdCopy` output is fed through
`strings.TrimSpace`, which removes the trailing newline from `git diff` output.
`git apply` requires a trailing newline to parse patches; without it, the patch
is silently rejected or applies incorrectly.

Workaround: re-append `\n` to the patch bytes if the last byte is not `\n`
before calling `git apply`. See `Fix: restore trailing newline in overlay patch
output` (commit f9bf669).

---

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

- `internal/orchestrator/integration_main_test.go:TestMain` (binary bootstrap)
- `internal/orchestrator/integration_helpers_test.go::integrationSetup` (per-test)
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

---

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

---

### Docker-in-Docker: nested `fuse-overlayfs` can't exec on Docker Desktop / Podman Machine (macOS) — RESOLVED via overlay2 + real-fs volume

**Symptom (pre-fix):** under `--isolation container-privileged` on macOS Docker Desktop, a nested `dockerd` configured for `fuse-overlayfs` + `docker run hello-world` pulled the image fine then died with:

```
exec /hello: invalid argument
```

Every nested container hit it — `alpine echo`, `busybox uname`, `hello-world` all failed identically with `EINVAL` on `execve`. **Not** arch-related (arm64-on-arm64; failed even with `--platform linux/arm64`).

**Explanation:** native `overlay2` can't nest on the container's overlay rootfs (`driver not supported: overlay2`), so yoloai used to pin `fuse-overlayfs`. But whether a process can *exec* a binary on a `fuse-overlayfs` mount depends on the **host VM's kernel**: **OrbStack**, **Podman Machine** (Fedora), and native **Linux** can; **macOS Docker Desktop**'s **LinuxKit** kernel cannot — `execve` returns `EINVAL`, on both overlay and real-fs backings (so the backing fs isn't the issue; the FUSE-exec path is). Verified cross-platform in `docs/contributors/design/research/dind-storage-drivers.md`.

**Fix (current):** yoloai mounts a managed **real-filesystem named volume at `/var/lib/docker`** for every privileged sandbox (`docker.go` `ensureDindVolumeMount`). On a real-fs backing the nested daemon **auto-selects the native overlay driver** — no FUSE, so the LinuxKit exec limitation never applies — and the daemon.json pin is gone, so both `start_dockerd` and a manual `sudo dockerd &` get it. Verified working end-to-end on Docker Desktop (ext4 → overlay2), OrbStack (btrfs), Podman Machine (xfs → fuse-overlayfs, which execs fine there), and Linux. `start_dockerd` keeps a fuse-overlayfs fallback only when the backing is still `overlay` (i.e. the volume is somehow absent). `vfs` also works as a manual escape hatch but is slow/disk-heavy; not used.

The earlier stopgaps — a `system check` advisory and a smoke `dind` N/A-reclassification — were **removed** once the real fix landed (dind now works on every provider, so they'd be misleading).

**Code pointer:** `runtime/docker/docker.go` — `ensureDindVolumeMount` / `dockerLibVolumeName` (Create mounts it, Remove reclaims it). `runtime/docker/resources/Dockerfile` — daemon.json pin removed. `runtime/monitor/setup_helpers.py` `dockerd_storage_args` + `sandbox-setup.py` `start_dockerd` (fstype probe). Reproduce the *old* failure with `docker run --rm --privileged --entrypoint bash yoloai-base -c 'echo {} | sudo tee /etc/docker/daemon.json; sudo dockerd --storage-driver=fuse-overlayfs & … sudo docker run --rm hello-world'` on Docker Desktop.

---

### gVisor netstack ignores in-sandbox iptables rules

**Symptom:** A sandbox created with `--isolation container-enhanced` (gVisor / runsc) and `--network-isolated` appears to apply the deny-by-default rules in its startup log (`network.isolate iptables default-deny applied`), but outbound traffic to non-allowlisted destinations is **not** blocked. Egress to any IP succeeds.

**Explanation:** gVisor implements its own userspace network stack (the "Sentry"). The `iptables` command inside a runsc sandbox writes rules into a guest-only table that gVisor's netstack does not consult. The host kernel never sees those rules — outbound packets traverse the host veth and exit normally. The Linux netfilter machinery that `entrypoint.py::isolate_network` relies on is bypassed entirely.

This applies to both backends that can load runsc:
- `docker` with `--isolation container-enhanced`
- `podman` with `--isolation container-enhanced`

Standard runc (`--isolation container`, `--isolation container-privileged`) is unaffected because the host kernel evaluates iptables in the container's netns. Kata-based isolation modes (`vm`, `vm-enhanced`) are unaffected because the guest Linux kernel inside the VM evaluates iptables exactly like bare metal.

The entrypoint loud-failure fix (`NetworkIsolationError`) catches *some* gVisor failures incidentally — gVisor's iptables emulation rejects `-m set --match-set`, so the ipset-backed allowlist rule fails at container start, taking the sandbox down. That's accidental and brittle: future gVisor versions may accept the rule without enforcing it, putting us back in silent-no-op territory.

**Fix:** Reject the combination at sandbox creation, before the container is built. `runtime.IsolationEnforcesInSandboxIptables(isolation)` returns false for `container-enhanced`; `orchestrator/create_instance.go::buildInstanceConfig` checks this when `state.networkMode == "isolated"` and returns an explicit error pointing the user at the working isolation modes.

**Permanent fix:** The redesign in [`docs/contributors/design/network-isolation.md`](design/network-isolation.md) moves enforcement to the host netns, where gVisor's netstack is irrelevant — packets leaving the gVisor sandbox traverse the host veth and hit the host iptables rules like any other backend. Until that lands, the combination is rejected.

**Code:** `runtime/isolation.go::IsolationEnforcesInSandboxIptables`, `orchestrator/create_instance.go::buildInstanceConfig`

---

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

**Code:** `orchestrator/create_instance.go` — the seccomp setting is correct; the failure is environmental.

---

### `DiskUsage().Images[].Size` includes shared layers; summing it multiply-counts them

**Symptom:** `yoloai system disk` / `yoloai doctor` reports an absurd reclaimable cache — e.g. podman at **129.74 GiB** when `podman system df` says only ~5 GiB. The inflation scales with the number of images: dozens of intermediate base-build stages each "weigh" ~5 GiB in the report.

**Explanation:** The Docker/Podman SDK's `client.DiskUsage()` returns each `image.Summary.Size` as that image's *total* size **including layers it shares with other images**. yoloai's base build leaves many `<none>` intermediate stages that all share one ~5 GiB base, so summing `img.Size` across them counts the shared layers once per image. `docker/podman system df` does not do this — its images SIZE column is the deduplicated layer-store total, which the SDK exposes separately as `types.DiskUsage.LayersSize`.

**Fix:** Use `du.LayersSize` for the image portion of the cache total; add container `SizeRw`, volume `UsageData.Size`, and build-cache `Size` on top (those live outside the image layer store and are not deduplicated against it). Never sum `du.Images[].Size`.

**Code:** `runtime/docker/prune.go` `splitCacheBytes()` (shared by docker + podman; returns the no-rebuild `cached` total and the rebuild-forcing `images` total separately). Guard test: `runtime/docker/prune_test.go::TestSplitCacheBytes_ImagesUseDeduplicatedLayersSize`.

**Related (Podman):** the `du.LayersSize` fix above silently fails on Podman, whose docker-compat `/system/df` returns `LayersSize: 0`. The Podman backend injects a per-image dedup instead — see [Podman: `/system/df` reports `LayersSize: 0`](#podman-systemdf-reports-layerssize-0).

**Related (display):** containerd now sizes its image cache via the snapshot `Usage` API (see [containerd: both snapshotters hold a copy](#containerd-both-overlayfs-and-devmapper-snapshotters-hold-a-copy-prune-and-sizing-must-cover-both)); `ImageBytes == -1` remains only as an error fallback when listing images fails, and the `<= 0` filter in `internal/cli/doctorcmd/doctor.go` `renderReclaimTier` still guards it (a `-1` would otherwise render as a literal `-1 B` and skew the total).

---

### Docker containerd image store: `image rm` frees no disk until the build cache is pruned; SDK `SpaceReclaimed` undercounts

**Symptom:** On Linux Docker with the containerd image store enabled (`features.containerd-snapshotter`), `yoloai system disk` reports the docker backend consuming far more than the image's apparent size — e.g. **33.66 GiB** for a base image that occupies ~5 GiB on macOS Docker Desktop (classic store). `docker image rm <id>` reports success but frees ~0 bytes on disk. After `docker builder prune -af`, the same `image rm`/`image prune` suddenly frees ~20 GiB even though the SDK's `ImagesPrune.SpaceReclaimed` reported only ~5.9 GiB.

**Explanation:** With the containerd snapshotter, BuildKit's build cache holds references to the image layers it produced. While those cache records exist, the layers are pinned: removing the image record drops the tag but containerd's GC can't reclaim the still-referenced content blobs/snapshots. Pruning the build cache releases the references, and only then does layer removal actually return disk. Separately, the SDK's `SpaceReclaimed` field counts only the content it directly deleted in that call, not the cascading snapshot/blob GC that follows — so it undercounts real reclaim by ~4x. The classic (non-containerd) store on macOS Docker Desktop doesn't exhibit either behavior, which is why the same base image reads ~5 GiB there.

**Fix:** Prune the build cache *before* (or in the same pass as) image removal so layers actually free. yoloai's plain `prune` does `BuildCachePrune(all=true)` + `VolumesPrune` + dangling `ImagesPrune` (no rebuild forced); `--images` adds full image removal. Because `SpaceReclaimed` is unreliable (it undercounts here, and *over*counts on Podman — see [Podman: `ImagesPrune` `SpaceReclaimed` is the un-deduplicated image-size sum](#podman-imagesprune-spacereclaimed-is-the-un-deduplicated-image-size-sum)), the reclaimed total is **not** taken from `SpaceReclaimed`. It is the drop in this backend's own `CacheUsage` across the prune (`before − after`), which reuses the already-accurate sizing and is self-attributed per backend (an earlier `statfs` free-space delta was abandoned because, on a shared `/`, one backend's delta absorbs bytes freed by another's prune — see working-notes D37).

**Note on logical vs physical:** because `CacheUsage` counts build cache and image layers separately but they *share* content on the containerd store, the reported reclaim is a *logical* figure that can exceed the physical bytes `df` shows freed. That gap is expected and documented (D37), not a bug.

**Code:** `runtime/docker/prune.go` — `PruneCache` (prune order + before/after delta), `reclaimableBytes` (the `CacheUsage` sample), `splitCacheBytes` (build cache counted as no-rebuild `cached`, `LayersSize` as rebuild-forcing `images`).

---

### Docker on macOS may be OrbStack, not Docker Desktop — `docker info` `.ClientInfo.Context` tells you which

**Symptom:** macOS disk-reporting verification assumed Docker Desktop's LinuxKit VM (classic image store, data root hidden inside the VM). On a dev machine the `docker` CLI was actually talking to **OrbStack** (`docker info` → `Context: orbstack`), which is a different LinuxKit-style VM with `Storage Driver: overlay2` on a `btrfs` backing filesystem, `containerd-snapshotter` **off** (classic store), and `Default Runtime: runc`.

**Why it matters / what we verified (2026-05-29, Docker 29.4.0 via OrbStack):** the socket/API-only sizing path is store- and VM-agnostic, so it Just Works regardless of which macOS Docker you run. `yoloai system disk` reported docker `image_bytes = 5023481654` (4.68 GiB) — **byte-exact** against `docker system df` Images SIZE `5.023GB` — and `cached_bytes = 507954634` (484.4 MiB) matching Local Volumes `508MB`. Because OrbStack uses the **classic** store (not the containerd snapshotter), the [`image rm` frees no disk until build cache pruned](#docker-containerd-image-store-image-rm-frees-no-disk-until-the-build-cache-is-pruned-sdk-spacereclaimed-undercounts) pinning behavior does **not** apply, and the logical-vs-physical reclaim gap collapses (logical ≈ physical). No code change needed; the takeaway is to **check `docker info` for the active context/store before comparing numbers** — "macOS Docker" is not necessarily Docker Desktop.

**Code:** none (verification only). Sizing path: `runtime/docker/prune.go` `CacheUsage`/`splitCacheBytes`.

---

### The Docker Go SDK ignores `docker context`; `client.FromEnv` honors only `DOCKER_HOST`

**Symptom:** After `docker context use desktop-linux` the `docker` CLI works, but yoloai fails with `docker daemon is not responding`. Root cause: `/var/run/docker.sock` is a symlink to a *stopped* provider's socket (e.g. `~/.orbstack/run/docker.sock` after switching OrbStack → Docker Desktop), and the Go SDK kept dialing it.

**Explanation:** `dockerclient.FromEnv` reads `DOCKER_HOST`/`DOCKER_CERT_PATH`/`DOCKER_API_VERSION` and otherwise falls back to the built-in default socket. Unlike the `docker` CLI, it does **not** consult `~/.docker/config.json` `currentContext` or the `~/.docker/contexts/meta/<sha256(name)>/meta.json` endpoint store. So `docker context use` retargets the CLI but not any SDK-based tool — they diverge whenever the default socket is stale.

**Fix:** `resolveDockerHost` mirrors the CLI's precedence sourced from the threaded env (§12): `DOCKER_HOST` → active context (`DOCKER_CONTEXT` env, else config.json `currentContext`) endpoint → "" (SDK default). Any parse/read failure degrades to "". As a self-heal for the stale-symlink case with no context switch, when the resolved socket fails `Ping` the auto path probes well-known local sockets (`/var/run`, Docker Desktop, OrbStack, Colima, Rancher Desktop) and adopts the first that answers, printing a one-line stderr notice. An explicitly pinned host (the podman backend) bypasses both. `probe` was widened to match (context endpoint or any existing well-known socket counts as available).

**Code:** `runtime/docker/dockerhost.go` — `resolveDockerHost`, `wellKnownDockerSockets`, `sockExists`; `runtime/docker/docker.go` — `NewWithSocket` (`dialDocker`/`dialFirstAlive` fallback), `probe`.

---

### Docker/Podman: `volume prune` (default filter) removes only *anonymous* volumes; reclaim accounting must be scoped to yoloai's own volumes

**Symptom:** `yoloai doctor`/`system disk` report a large reclaimable "cached" figure (e.g. 484.4 MiB) that is actually the user's **named** volumes — `docker volume ls` shows things like `foley_postgres-data` (a compose database) and `vscode`, which have nothing to do with yoloai. `yoloai system prune` dry-run promises to remove them ("would remove unused volumes …"), then the real prune reports `reclaimed 0 B` because nothing was freed.

**Explanation:** Two compounding problems. (1) Since Docker 23, `docker volume prune` / the SDK `VolumesPrune` with default filters removes only **anonymous** volumes — named volumes survive unless `all=true` is set. So the dry-run estimate (which summed *every* volume's size) over-promised relative to what the prune could remove. (2) More fundamentally, yoloai **creates no Docker volumes at all**, so counting *any* host volume as yoloai-reclaimable is wrong — and threatening to delete the user's database volume is dangerous. The only reason the DB survived was the anonymous-only quirk masking the over-promise. (See also the OrbStack verification note above, which observed `cached_bytes` == the 508MB Local Volumes and mistook it for legitimate yoloai cache.)

**Fix:** Scope both the estimate and the prune to volumes carrying the `com.yoloai.managed` label. `splitCacheBytes` counts only labeled volumes; `PruneCache` calls `VolumesPrune` with `label=com.yoloai.managed` + `all=true` (so named yoloai volumes are removed, not just anonymous ones). yoloai creates no volumes today, so this currently reclaims nothing and reports nothing for volumes — correct. Any future code that creates a volume MUST stamp it with `managedLabel`.

**Podman caveat:** Podman's docker-compat API does **not** accept the `all` volume filter — passing it fails the whole prune with `failed to parse filters for all=true&label=…: "all" is an invalid volume filter`, surfacing as `podman: volumes prune failed: …`. Podman has no anonymous-vs-named distinction (`podman volume prune` removes every unused volume by default), so the `all=true` arg is unnecessary there. `PruneCache` therefore omits it when `binaryName == "podman"` and sends only the `label` filter.

**Code:** `runtime/docker/prune.go` — `managedLabel` const, `splitCacheBytes` (label-gated volume loop), `PruneCache` (label+all `VolumesPrune` filter, `all` omitted for Podman).

---

### Docker: legacy builder commits one dangling intermediate image per Dockerfile step — build with BuildKit

**Symptom:** `yoloai system prune` finds a *different* `<none>` (dangling) image on every run — `Orphaned resources: image faf2f314ca62`, then `8e3deeacf8ac`, etc. — even when no sandboxes are running and nothing has been built. Each removal reports `reclaimed 0 B` and the cycle never ends. `docker images -a --filter dangling=true` shows dozens of `<none>` images, all the same size (e.g. 6.63 GB), all created at the same time. Their IDs match the layers in `docker history yoloai-base`.

**Explanation:** The moby Go SDK's `client.ImageBuild` runs the **legacy builder**, not BuildKit (the `#(nop) COPY …` history format is the tell). The legacy builder commits a separate image per Dockerfile instruction. On the **containerd image store** (Docker Desktop 23+ default) those per-step images are untagged manifests that form the parent chain of the tagged `yoloai-base` and surface as dangling. Without `-a`, the daemon reports only the current *leaf* of that untagged chain — exactly one image — so `pruneDanglingImages` removes one per run; removing it frees nothing (blobs are shared with the live tag) and exposes the next intermediate as the new leaf. The result is an N-deep peel that regenerates on every base rebuild and quietly destroys the layer cache rebuilds would reuse. (BuildKit, by contrast, keeps step results in the *build cache* — pruned by `BuildCachePrune` — not as images, so no dangling intermediates exist.)

**Fix:** Build `yoloai-base` via BuildKit by shelling out to `<binary> build -` (context tar piped to stdin) with `DOCKER_BUILDKIT=1`, instead of `client.ImageBuild`. Podman's `build` (Buildah) likewise never commits per-step images, so the same code path is correct there. Profile builds with secrets already used this CLI path; the base build now matches. After switching, a one-time `docker image prune` clears the legacy intermediates left by prior builds (once `yoloai-base` is rebuilt with BuildKit they are no longer ancestors of any tag, so they prune cleanly and free real disk).

**Code:** `runtime/docker/build.go` — `(*Runtime).buildBaseImage` (CLI/BuildKit via `<binary> build -`), `curatedBuildEnv` (forces `DOCKER_BUILDKIT=1`).

---

### Docker Desktop: ImageInspect transiently NotFounds a present image on the idle containerd store

**Symptom:** Under the multi-provider smoke run (`make smoketest` / `--all-docker-providers`), the **Docker Desktop** docker tier rebuilds `yoloai-base` from scratch (`Building base image (first run only)…`, ~3 min) on *every* run, while the OrbStack tier never does. The image is demonstrably present — `docker images` lists it, `yoloai system check` reports `image ok`, and a `system build` run by hand skips instantly. Running the desktop tier in isolation does **not** reproduce it.

**Explanation:** The tell is an observer effect: a run that polls the desktop daemon every few seconds (keeping it awake) never rebuilds; a natural run does. Docker Desktop is usually the *non-active* docker context (OrbStack is active), so during the long first-provider pass the desktop daemon sits idle and Docker Desktop's Resource Saver stops its engine. When the desktop child finally calls `imageExists`, the just-resumed **containerd image store** answers `ImageInspect("yoloai-base")` with NotFound for a brief window even though the tag is present — reproduced live once as `docker image inspect yoloai-base` → "No such image" while `docker images` still listed it. `Setup` believed the single NotFound and rebuilt. The rebuild is a *cold* ~3 min build (not a fast cache hit) because the prior run's `_prerun_prune` reclaims the BuildKit build cache. OrbStack (classic overlay2 store, and the always-warm active context) never hits either condition.

**Fix:** Make `imageExists` distrust a lone `ImageInspect` NotFound: cross-check with `ImageList` (reference filter), which lists the image even when inspect flaps. On disagreement the image is treated as present and the discrepancy is logged (`containerd-store inspect flap`). If both inspect and list report absent, the listing is retried with bounded backoff (~3.5 s total) in case the store is still settling after a resume — a separate "warm-up" log distinguishes that case. A genuinely-absent image (real first run) pays only the bounded backoff. The two warnings let a real run confirm which case fires; once the inspect-vs-list disagreement is confirmed in the field, the backoff retry can be dropped.

**Code:** `runtime/docker/docker.go` — `(*Runtime).imageExists` (inspect → list cross-check), `imageListedByRef`, `confirmImagePresentByList` (retry/backoff, unit-tested), `imageExistsRetries`/`imageExistsBackoff`.

---

### Docker Desktop: a single-file bind mount serves stale content after the host atomic-renames it (keepalive_only never reaches the entrypoint)

**Symptom:** Every `yoloai new`/`start`/`clone` fails on **Docker Desktop** (macOS) with
`yoloai: substrate not ready within 30s (root provisioning did not complete)`; the
identical command passes on **OrbStack** and on Linux. The failure autopsy shows the
in-container `sandbox.jsonl` jumped straight from `entrypoint.python_start` to
`sandbox.agent_launch` — **no** `entrypoint.keepalive_only` event and **no**
`/yoloai/logs/.substrate-ready` marker — even though the host's
`runtime-config.json` clearly has `"keepalive_only": true`, and a `docker exec … cat
/yoloai/runtime-config.json` seconds later *also* shows `true`.

**Explanation:** The agent-free bring-up (D88 `startViaLaunch`) signals the entrypoint
by patching `keepalive_only:true` into `runtime-config.json` just before `Create`. That
patch is an **atomic rename** (write temp + rename), which gives the file a **new
inode**. `runtime-config.json` is mounted into the container as a **single-file**
read-only bind mount (`buildSystemMounts`). Docker Desktop's gRPC-FUSE file sharing
caches the path→inode mapping and serves the **stale pre-patch content** for that
single file when the entrypoint reads it at container start — so the entrypoint sees
`keepalive_only` absent, evaluates `cfg.get("keepalive_only", not cfg)` to `False`
(config is non-empty), takes the **legacy inline** path, runs sandbox-setup.py itself,
and never writes `.substrate-ready`. The host's `waitForReady` polls for that marker
and times out at 30s. The cache refreshes within a second or two, which is why a later
`exec` shows the correct content — masking the race. OrbStack (and Linux bind mounts)
propagate the new inode immediately, so they never see stale content. **The bug is not
that the host failed to patch the file — it did — but that the patched single file does
not reach the container in time on Docker Desktop.**

**Fix:** Don't rely on the patched single-file bind mount as the only signal.
`startViaLaunch` also sets `YOLOAI_KEEPALIVE_ONLY=1` in the container's env
(`InstanceConfig.ContainerEnv` → `containerConfig.Env`), which is baked into the
container config at create time and is immune to mount-propagation lag. The entrypoint
treats the env var as authoritative (forces `keepalive=True` when set). The file patch
stays as the Linux/OrbStack record and a backstop. General rule: **a host-side change to
a single-file bind mount may not be visible inside a Docker Desktop container promptly;
deliver create-time signals via env vars (or a bind-mounted *directory*, which
propagates in real time) rather than by mutating a bind-mounted file.**

**Code:** `internal/orchestrator/launch/launch.go` (`startViaLaunch` sets
`YOLOAI_KEEPALIVE_ONLY=1`), `runtime/docker/resources/entrypoint.py` (env override of
the `keepalive` decision).

---

### Podman: `build` rejects the docker BuildKit attestation flags

**Symptom:** `make integration-podman` (and any podman-backed `yoloai system build`) fails at the base/profile image build with `Error: unknown flag: --provenance` → `yoloai: podman build exited with code 125`. Docker builds are unaffected.

**Explanation:** `--provenance` / `--sbom` are **BuildKit/`docker buildx`** flags that disable SBOM/provenance attestations. They were added to the shared `<binary> build` path to stop the attestation manifest list from making `yoloai-base` vanish between runs on Docker Desktop's containerd image store. Podman's `build` (Buildah) produces no such attestations and does not implement those flags — passing them is a hard error (podman 4.9.3 confirmed; not in `podman build --help`). Because the docker backend serves podman via the docker-compat path with `binaryName="podman"`, the flags leaked onto the podman command line.

**Fix:** Gate the attestation opt-out flags on the binary — emit `--provenance=false --sbom=false` only when `binaryName == "docker"`, omit for podman (which needs neither). The base and profile build sites both go through one helper so the two stay consistent.

**Code:** `runtime/docker/build.go` — `attestationOptOutFlags(binaryName)`, used by `(*Runtime).buildBaseImage` and `(*Runtime).BuildProfileImage`.

---

### Docker/Podman: `ImagesPrune` can't remove images held by non-stopped containers; the dry-run must name the blockers

**Symptom:** `yoloai system prune --images` (Docker or Podman) prints a confident multi-GB estimate during dry-run — e.g. `docker: cache prune skipped (--dry-run): would remove unused images, volumes, build cache (~7.14 GB)` — the user confirms, and the actual prune reports `docker: reclaimed 0 B` with no error message. Running it again gives the *same* dry-run estimate and the *same* 0 B result. `docker system df` shows `Images TOTAL=1 ACTIVE=1 SIZE=7.753GB RECLAIMABLE=7.753GB (100%)` despite a yoloai sandbox container being `Up 13 days`.

**Explanation:** `PruneCache` runs `ContainersPrune` (which removes only **stopped** containers — `exited`/`dead`, the default filter) followed by `ImagesPrune` (which refuses to remove an image that any container still references — `running`, `paused`, `restarting`, `created`, and `removing` all pin it). A live yoloai sandbox holds `yoloai-base` open, so `ImagesPrune` is a no-op and `before − after` is zero. None of the prune calls return an error (they each succeed at doing nothing), so no `X prune failed` line appears — the user only sees the contradiction. The dry-run estimate, separately, uses `splitCacheBytes(du)` which reports `du.LayersSize` regardless of in-use status: it's a *footprint* total, not a *reclaimable* total, so the promise is misleading whenever any non-stopped container is attached. Docker's own `system df` RECLAIMABLE column has the same blindspot (it shows 100% reclaimable for an image with one active container), which is why neither tool surfaces the cause until you go look at `docker ps`.

**Fix:** In `pruneCacheDryRun`, after the estimate line, scan `du.Containers` for any container whose `State` is not `exited` or `dead` and emit a per-backend warning naming each blocker before the user is prompted to confirm:

```
docker: cache prune skipped (--dry-run): would remove unused images, volumes, build cache (~7.14 GB)
docker: image reclaim is blocked by 1 active container(s) — stop or destroy them to reclaim image layers:
docker:   yoloai-x (running) holds yoloai-base
```

The estimate is left as-is — it's still a valid *upper bound* if the user stops the listed containers — but the warning makes the gap actionable. The check is gated behind `includeImages` because `BuildCachePrune` / `VolumesPrune` are unaffected by attached containers; only the image tier needs this signal. The fix is in the docker package and is inherited by the Podman backend (which embeds `*docker.Runtime`).

**Note on Docker's `system df` RECLAIMABLE column:** it counts an image as reclaimable when nothing references it *for prune purposes* — but its "ACTIVE=1, RECLAIMABLE=100%" output for the same one-image case looks contradictory and is not useful for diagnosis. Trust `docker ps -a` + the new yoloai warning instead.

**Code:** `runtime/docker/prune.go` — `imageReclaimBlockers` (state filter), `(*Runtime).warnImageReclaimBlockers` (the per-line output), `pruneCacheDryRun` (the call site, `includeImages` only). Guard tests: `runtime/docker/prune_test.go::TestImageReclaimBlockers_*`.

---

### Podman: docker-compat API has no build-cache endpoint — `BuildCachePrune` returns 404 (Not Found)

**Symptom:** `yoloai system prune` against the Podman backend prints `podman: build cache prune failed: Error response from daemon: Not Found`.

**Explanation:** Podman's Docker-compatible API has no BuildKit build-cache endpoint; `POST /build/prune` returns HTTP 404. The Podman backend embeds `*docker.Runtime` and inherits its `PruneCache`, which unconditionally calls `BuildCachePrune`. The 404 is expected and harmless, but surfacing it as "failed" is misleading.

**Fix:** In `PruneCache`, swallow the error when `cerrdefs.IsNotFound(err)` is true (it stays a real failure for any other error). Podman has no build cache to free, so skipping is correct.

**Code:** `runtime/docker/prune.go` — `PruneCache` (`BuildCachePrune` error guarded by `!cerrdefs.IsNotFound`).

---

### Podman Machine (macOS): gvproxy host-forward passes a one-shot curl but stalls the agent's streaming connection

**Symptom:** A credential-brokered Claude agent on the podman backend on macOS hangs on its **first** API call — the real-agent smoke (`stop_start`/`tag_transfer`) times out with `sentinel 'done' not seen in 90s`, while the same agent on docker (OrbStack) and apple brokers fine and finishes in ~10s. Confusingly, a one-shot `curl http://192.168.127.254:<port>/...` from inside the same container to the host-bound injector **succeeds** (and repeats 12/12), so a reachability spike looks green.

**Explanation:** On macOS, podman runs inside a podman-machine Linux VM. The agent reaches the Mac host only through the machine's user-space network proxy, **gvproxy**, via the host-forward address `192.168.127.254` (the slirp alias `10.0.2.2` reaches the *machine VM's* host, not the Mac, so it's wrong here). gvproxy forwards a short request/response fine, but it does **not** reliably carry the credential injector's traffic for a real agent — a long-lived / streaming (SSE) LLM connection through it stalls, so the agent never gets its first completion and hangs. The injector itself is alive and host-reachable; the failure is purely the gvproxy hop under sustained/streaming load. The lesson: **a single curl is not sufficient evidence that a host hop works for the credential broker — only the real-agent smoke is.** The injector model is sound on macOS (docker's `host.docker.internal` and apple's vmnet gateway both broker a real agent successfully); gvproxy specifically is the weak link.

**Fix:** podman's `InjectorReach` returns `runtime.ErrInjectorUnsupported` on darwin, so brokering degrades to **direct delivery** (the real credential is delivered into the container as-is — the conservative posture, also used for tart). This restores the working pre-broker behavior. Making podman-macOS broker needs a streaming-safe host hop (follow-up). Guard: `TestIntegration_Podman_DirectDeliveryOnMacOS` asserts no injector starts and the credential is delivered directly on darwin; `TestIntegration_CredentialBroker_Podman` (which asserts brokering) is darwin-skipped — its target is Linux rootless podman.

**Code:** `runtime/podman/reach.go` — `InjectorReach` (darwin → `ErrInjectorUnsupported`). See DF57 in `design/findings-resolved.md`.

---

### Docker exec: an attached exec does not outlive the client that started it

**Symptom:** A long-lived process started with `ContainerExecAttach` (attached `docker exec`) dies when the launching process exits — even though the container itself keeps running.

**Explanation:** An attached exec's lifetime is coupled to its hijacked stdio connection. When the client that opened the attach closes it (e.g. the CLI returns), the exec'd process is terminated / loses its stdio and exits — it is **not** a detached background process. This bit us when the session-runner (`sandbox-setup.py`), launched attached, was killed mid-startup the moment `yoloai new` returned ([DF44](design/findings-unresolved.md)); the agent survived only because tmux self-daemonizes, but the status-monitor never started and the secrets-consumed marker was never written. Inclusion-test check: if you deleted all yoloAI code, `docker exec <ctr> <longproc>` attached *still* would not survive the client disconnect — the surprise is the engine's exec lifetime, not our wiring.

**Fix:** For a process that must outlive its launcher, start it **detached** — `ContainerExecStart` with `container.ExecStartOptions{Detach: true}` (no attach) — and redirect its stdio to files inside the container (detached stdio is otherwise discarded). yoloAI exposes this as `runtime.ProcSpec.Detached`.

**Code:** `runtime/docker/launch.go` (the `Detached` branch); `internal/orchestrator/launch/launch.go::startViaLaunch`. Related: DF44.

---

### A freshly-created ephemeral container can transiently expose an incomplete rootfs under heavy concurrent churn

**Symptom:** `yoloai start --network-isolated` on a container backend fails with `install network-isolation firewall: netns sidecar exited 2: python3: can't open file '/yoloai/bin/install-firewall.py': [Errno 2] No such file or directory`, even though the image demonstrably contains that file (`docker run --rm --entrypoint sh yoloai-base -c 'ls /yoloai/bin/install-firewall.py'` succeeds, and the agent container itself — same image — booted fine via `/yoloai/bin/entrypoint.py`). Observed on OrbStack during the full smoke matrix when a fresh run started the instant a prior 7-backend run finished; **both** concurrent `isolation_check` backends failed at the same second with different container-level errors (one with the file-missing sidecar exit, the other with `substrate not ready within 30s: … container … is not running`). It does **not** reproduce running the test in isolation or on re-run.

**Explanation:** Under heavy concurrent container create/destroy churn, the engine can momentarily return a created container whose overlay rootfs is not fully materialized — so a process inside it sees a missing file that is genuinely in the image, or the container exits immediately. The firewall sidecar is the most exposed surface because it creates a brand-new ephemeral container (`--network container:<target>`) at the worst moment. Inclusion-test check: if you deleted all yoloAI code, `docker run` of an image during the same churn would *still* be able to hand back a briefly-incomplete rootfs — the surprise is the engine's, not our wiring. (The smoke harness already serializes VM-backed `isolation_check` for an analogous reason; the container backends were not serialized.)

**Fix:** Bounded retry of the firewall sidecar install (`runNetnsSidecarWithRetry` — 3 attempts, 500ms backoff). `install-firewall.py` is idempotent (`apply_firewall` flushes the `OUTPUT` chain and the ipset before re-adding rules), so re-running after a partial/failed attempt is safe. The retry still **fails closed**: a persistent failure surfaces after the last attempt and fails the launch rather than running the agent with unenforced isolation.

**Code:** `internal/orchestrator/launch/launch.go::runNetnsSidecarWithRetry`; the single-shot sidecar primitive is `runtime/docker/sidecar.go::RunNetnsSidecar`.

---

### Docker: base-image staleness marker was keyed per backend, not per image store — second provider runs stale

**Symptom:** The same `can't open file '/yoloai/bin/install-firewall.py'` (or any missing embedded resource) as the entry above, but **deterministic and provider-specific**: `--network-isolated` fails every time on one docker provider (e.g. Docker Desktop) while another (e.g. OrbStack) passes. Unlike the transient-rootfs case, the file is **genuinely absent** from the failing provider's `yoloai-base` (`docker run --rm --entrypoint sh yoloai-base -c 'ls /yoloai/bin/install-firewall.py'` errors on that provider, succeeds on the other), and it does **not** self-heal on re-run. Surfaced by the `--all-docker-providers` release smoke; a fresh single-provider install is unaffected.

**Explanation:** The base-image build records a checksum marker so `NeedsBuild` can rebuild when the embedded resources change. The marker was keyed by *backend* (`.base-image-checksum-docker`) — but OrbStack, Docker Desktop, Colima and Rancher are **separate image stores sharing the "docker" backend**. After one provider built and recorded the checksum, `NeedsBuild` returned false for every other docker provider, so a provider whose store held an *older* `yoloai-base` (predating a resource like the netns firewall scripts) was never rebuilt and silently ran the stale image. This is the provider-dimension sibling of DF56 (which split the marker across docker/podman/containerd/apple but treated all docker providers as one store). Network isolation fails *closed*, so it is broken-feature, not a security hole.

**Fix:** Stop tracking docker base-image freshness with a host-side marker at all — stamp the build-inputs checksum **onto the image** as a label (`--label yoloai.base.checksum=<sum>`) and read it back via `Runtime.baseImageStale` (`ImageInspect` → `Config.Labels`). The checksum then travels *with* the image, in whatever store holds it, so each local provider (OrbStack, Docker Desktop, …) is judged by its own image with no key to partition — structurally immune to the provider dimension, the `/var/run/docker.sock` provider-switch symlink, and remote daemons alike. An image with no label (built before this scheme) reads as stale and rebuilds once. A rejected earlier patch keyed the host-side marker by the daemon's `docker info .ID`; that fixed docker but leaned on a property that is empty for podman's docker-compat API, so its correctness varied by daemon (clever-hack smell). The host-side marker (`baseImageChecksumPath`/`NeedsBuild`/`RecordBuildChecksum`) remains, correctly, for the genuinely single-store backends **apple** (a Tart VM image, not OCI — can't carry a label) and **containerd** (one image store; no provider dimension).

**Code:** `runtime/docker/docker.go::baseImageStale` + the `--label` in `runtime/docker/build.go::buildBaseImage` (helper `checksumLabelStale`, const `baseChecksumLabel`); call site `EnsureBaseImage`. Single-store backends keep `baseImageChecksumPath`/`NeedsBuild`/`RecordBuildChecksum`.

## Podman

### Podman: an interrupted `build` leaves mounted buildah working-containers that wedge container create/start

**Symptom:** Intermittent (flaky) `start instance: instance not found` when yoloai creates+starts a podman container — the container is "created" but the immediately-following `ContainerStart` reports it as not found. Passes some runs, fails others, with no code change between. Often appears shortly after a `podman build` (or `yoloai system build --backend=podman`) was interrupted — e.g. SIGTERM'd via `timeout`, Ctrl-C, or a killed parent.

**Explanation:** `podman build` runs each stage in a **buildah working-container**. If the build is killed mid-flight, those working-containers are left behind **mounted** in podman's storage (`podman ps -a --external` shows `*-working-container` rows in state `Storage`; they don't appear in a normal `podman ps -a`). The stale mounts hold storage references and create lock/overlay contention, which sporadically makes a fresh container's create→start race fail with a spurious not-found. A normal `podman run` may still succeed, so the storage looks "healthy" while the symptom is intermittent.

**Fix / cleanup:** never interrupt a podman build. To recover, remove the leftovers (they need force because they're mounted):
```
podman ps -a --external                 # find *-working-container rows + any orphaned run
buildah umount <working-container>...    # unmount first (plain rm fails: "mounted ... state improper")
buildah rm <working-container>...
podman rm -f <orphaned-run>              # also reap any container orphaned by a killed `podman run` client
podman image prune -f                    # drop the dangling layer the partial build left
```
Then the flake disappears (verified: 0/2 → 5/5 on `TestIntegration_CredentialBroker_Podman` after cleanup). Distinct from Tart's "instance not found right after boot" (that's a boot-stabilization delay; this is leftover storage state).

### Podman: `/system/df` reports `LayersSize: 0`

**Symptom:** `yoloai doctor` / `yoloai system disk` reports the podman backend's images as **0 B** even though `podman system df` shows a multi-GB base image (e.g. ~5.5 GB). The cached tier (build cache, volumes) reports correctly; only the image tier reads zero.

**Explanation:** yoloai sizes images from the Docker SDK's `client.DiskUsage()`, taking the deduplicated `du.LayersSize` (see [`Images[].Size` includes shared layers](#diskusageimagessize-includes-shared-layers-summing-it-multiply-counts-them)). Docker populates `LayersSize` with the daemon's deduplicated layer-store total; **Podman's docker-compat `/system/df` always returns `LayersSize: 0`** and only fills the per-image `Size`/`SharedSize` fields. So the inherited docker code, correct for Docker, yields 0 for Podman.

**Fix:** The Podman backend injects a per-image dedup via `docker.Runtime.SetImageBytesFunc`. Summing `img.Size` would multiply-count the shared base (38 build stages sharing one ~5.5 GB base read as ~150 GB — the failure mode of the shared-layers entry above). The deduplicated total is `Σ(img.Size − img.SharedSize) + max(img.SharedSize)`: every image's unique bytes plus the shared layer set counted once. For yoloai's single-base build chain the largest `SharedSize` captures the full shared union exactly; multiple independent bases would slightly underestimate the shared tier.

**Code:** `runtime/podman/podman.go` `podmanImageBytes()` (injected in `New` via `SetImageBytesFunc`); `runtime/docker/prune.go` `splitCacheBytes()` (uses `imageBytesFn` when set, else `du.LayersSize`). Guard tests: `podman_test.go::TestPodmanImageBytes_*`, `docker/prune_test.go::TestSplitCacheBytes_ImageBytesFuncOverride`.

**macOS / version caveat (verified 2026-05-29, Podman 5.8.1 via Podman Machine `applehv`):** `LayersSize` is **NOT 0** on this version — the raw `/system/df` returns `LayersSize: 5018303449`, matching `podman system df` Images SIZE exactly. The `LayersSize: 0` bug above is therefore **Podman-version-specific**, not universal. The `podmanImageBytes` dedup still runs (it's unconditional) and, because every build-stage row shares the one base, it computes the *identical* value (`Σ(unique) + max(shared) == LayersSize` here), so it's harmless redundancy on 5.8.1 — the injected path agrees with the field it was working around. Keep the injection: older Podman (the version the bug was first seen on) still reports 0, and the dedup is correct on both.

---

### Podman: `ImagesPrune` `SpaceReclaimed` is the un-deduplicated image-size sum

**Symptom:** `yoloai system prune --images` on Podman reports a wildly inflated reclaim — e.g. **142.27 GB** freed when the actual footprint is ~5.18 GiB. The over-count scales with the number of images, exactly like the reporting-side bug.

**Explanation:** Podman's docker-compat `ImagesPrune` returns `SpaceReclaimed` as the **sum of every removed image's `Size`**, each of which *includes shared layers* — the same multiply-counting as [`DiskUsage().Images[].Size`](#diskusageimagessize-includes-shared-layers-summing-it-multiply-counts-them), but on the prune path instead of the sizing path. 38 build stages sharing one ~5 GiB base sum to ~140 GB. (Docker on the containerd store has the *opposite* problem — `SpaceReclaimed` undercounts.) So raw `SpaceReclaimed` is untrustworthy in both directions and must not be reported.

**Fix:** Don't use `SpaceReclaimed` at all. Report reclaim as the drop in the backend's own `CacheUsage` across the prune (`before − after`); `CacheUsage` already deduplicates correctly for Podman (via `podmanImageBytes`), so the delta is accurate (verified: 5.18 GB, matching the `/system/df` dedup) and self-attributed per backend. See working-notes D37.

**Code:** `runtime/docker/prune.go` `PruneCache` + `reclaimableBytes` (shared by docker + podman). Note `BuildCachePrune` returns "Not Found" on Podman (no BuildKit cache) — warned and harmless; the before/after delta still captures the actual reclaim.

---

### macOS: Podman Machine socket discovery needs `TMPDIR`; without it `inspect` reports a stale `/tmp` path

**Symptom:** Every yoloAI command that touches the Podman backend on macOS
(`system build`, `new`, …) fails with `no podman socket found (checked
$CONTAINER_HOST, $DOCKER_HOST, $XDG_RUNTIME_DIR/podman/podman.sock,
/run/podman/podman.sock)` even though `podman machine start` reports the machine
already running and `podman machine inspect` works fine from the shell.

**Explanation:** On macOS, Podman runs in a VM and the host-side machine API
socket path is derived from `$TMPDIR`: `podman machine inspect` reports
`$TMPDIR/podman/podman-machine-default-api.sock` (e.g.
`/var/folders/.../T/podman/...`). When `TMPDIR` is **absent** from the
subprocess env, podman falls back to `/tmp/podman/podman-machine-default-api.sock`,
which does not exist. yoloAI's discovery then `os.Stat`s that path, it fails, and
`discoverSocket` reports "no podman socket found". `HOME` is *not* the
determinant — the socket path is computed purely from `TMPDIR`.

This bit us because `TMPDIR` was missing from the daemon-discovery allowlist:
`machineSocketDiscovery` already allowlists `TMPDIR`, but the upstream
`EnvForDaemonDiscovery()` snapshot it curates from had dropped it, so there was
nothing to carry through. Docker users never saw this — only Podman-on-macOS
reads `TMPDIR` for socket discovery.

**Fix:** Keep `TMPDIR` in the daemon-discovery allowlist
(`config.daemonEnvAllowlist` and the mirrored public `runtime.DaemonEnvVars`) so
it survives to `podman.go::defaultMachineSocketDiscovery`. See
`internal/config/host_env.go::daemonEnvAllowlist` and
`runtime/podman/podman.go::defaultMachineSocketDiscovery`.

---

### macOS: `--userns=keep-id` maps the Podman Machine uid (1000), not the macOS uid

On macOS, Podman runs via Podman Machine (a Linux VM). `--userns=keep-id` maps
the VM user's uid (1000) into the container — not the macOS user's uid (e.g.
501). The container then runs as uid 1000, but `/home/yoloai` is owned by uid
1001 (the `yoloai` user), so agents cannot write their config.

Workaround: skip `keep-id` on macOS (`runtime.GOOS == "darwin"`). The
entrypoint uses `gosu` to remap `yoloai` to the correct uid, which is the same
path Docker takes. See `podman.go::Create`.

---

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
`runtime/docker/resources/entrypoint.py` `main()`. Docker-priv and macOS
podman-priv still run the entrypoint as root, so they take the direct (no-sudo)
path unchanged.

---

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

---

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

---

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

**Code:** `runtime/docker/docker.go` `execAttach`/`createExec`/`bridgeExecStreams`/`resizeExec`/`forwardExecResizes`. Conformance guards (run for docker AND podman): `runtime/runtimetest/conformance.go` `StdioExec*`/`InteractiveExec*ExitCode` subtests.

**Belongs here:** the external fact is that the docker/podman bare CLI and the SDK socket can resolve the same container differently under load (cousin of the [docker-context divergence](#the-docker-go-sdk-ignores-docker-context-clientfromenv-honors-only-docker_host)); our fix was to stop straddling both control planes.

## Containerd

### `WithNewSnapshot` does NOT unpack image layers

`client.WithNewSnapshot(name, img)` only calls `Prepare(parent)` on the
top-level chain ID, expecting the snapshot chain to already exist. If the image
was imported via `ctr import` but not yet unpacked, container creation fails
with: `parent snapshot sha256:... does not exist: not found`.

Must explicitly call `img.IsUnpacked()` / `img.Unpack(ctx, snapshotter)` before
`NewContainer()`. See `lifecycle.go::Create`.

---

### `docker save | ctr import` hangs if `ctr` fails early

If `ctr images import` exits with an error (e.g. permission denied on the
containerd socket) while `docker save` is still writing to the pipe, `docker
save` blocks indefinitely on a write to a broken pipe. The parent process hangs.

Must wait on `importCmd.Wait()` first, and if it fails, immediately call
`saveCmd.Process.Kill()` before calling `saveCmd.Wait()`. See `image.go::Setup`.

---

### `os.Stat` on the containerd socket does not detect permission denied

`os.Stat("/run/containerd/containerd.sock")` succeeds even when the process has
no permission to open the socket (EPERM). The stat only checks directory entry
existence. Must use `os.Open()` to distinguish ENOENT from EPERM. See `Fix:
containerd backend: detect socket permission denied` (commit e24d201).

---

### Containerd GC removes child blobs while leaving the root manifest intact

When registering images in a new namespace via cross-namespace content sharing,
the garbage collector can remove platform manifest blobs, config blobs, and
layer blobs while leaving the root manifest list entry intact. Checking only
the root with `cs.Info(root)` is insufficient for verifying image readiness.

Must walk the full descriptor tree with `images.Children` to verify all blobs
are accessible. See `image.go::verifyDescriptorTree`.

---

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

---

### containerd: both overlayfs and devmapper snapshotters hold a copy; prune and sizing must cover both

**Symptom (sizing):** before the fix, `yoloai doctor` reported the containerd backend's image cache as `?` (the `ImageBytes == -1` "unknown" sentinel), hiding several GB of real disk. **Symptom (prune):** `yoloai system prune --images` left thin-pool allocation behind — `dmsetup status containerd-pool` still showed >50% data blocks used after a prune that claimed success — and the leaked snapshots eventually filled the pool (a likely contributor to the `smoke-containerd-disk-pressure` ENOSPC stalls).

**Explanation:** yoloai selects the snapshotter per isolation mode (`lifecycle.go`): **overlayfs** for `--isolation vm`/container, **devmapper** for `--isolation vm-enhanced` (Firecracker). A host that has run both modes therefore holds **two physical copies** of the base image's layers — one in `io.containerd.snapshotter.v1.overlayfs`, one in the devmapper thin-pool. The original `CacheUsage`/`pruneSnapshots` hardcoded `SnapshotService("overlayfs")`, so devmapper snapshots were never counted and never removed.

Sizing must go through the **containerd socket**, not the host filesystem: yoloai may run unprivileged via the `containerd` group, and `/var/lib/containerd` is root-only (so `du`/`dmsetup` are unavailable on the normal path). The snapshot `Usage(ctx, key).Size` API returns real allocated bytes for *both* snapshotters over the socket (devmapper reports per-thin-device allocation, summing to the pool's used-block total), so summing `Usage` across every snapshot in each snapshotter is the portable, root-free measurement.

**devmapper caveat — discard_blocks decides whether host disk is freed:** removing a devmapper thin snapshot always returns its blocks to the pool (the `dmsetup` used-block count drops), but whether the pool's backing loopback file (`/var/lib/containerd/devmapper/data`, host-configured, e.g. ~10 GB) shrinks depends on the **`discard_blocks`** option in the `[plugins."io.containerd.snapshotter.v1.devmapper"]` block of `/etc/containerd/config.toml`:

- **`discard_blocks = true` (recommended):** containerd issues a `BLKDISCARD` on snapshot removal, which is passed down (the thin-pool defaults to `discard_passdown`) and punches the freed regions out of the **sparse** backing file. Host `df` drops. Verified on the dev host: with this set, `/var/lib/containerd/devmapper/data` is 10 GB apparent but only ~5 GB allocated (`ls -ls`), tracking actual usage; a prune shrinks it further.
- **unset/false (the original bad state):** discards are not punched back, the backing file stays fully allocated, and host `df` is unchanged by a prune even though the pool regains free blocks. This is the state the DF59 finding was first observed in.

yoloai **cannot detect which** over the containerd socket (the snapshot API doesn't expose pool config, and `/var/lib/containerd` is root-only), and does **not own the pool** (it's a host prerequisite — devmapper setup script + `config.toml`; yoloai only prunes the snapshots it created inside it). So prune reports the devmapper bytes on their own line with the discard caveat and keeps them out of the counted reclaim total, and the `doctor` `devmapper-snapshotter` capability Fix now includes "set `discard_blocks = true`". **Already-allocated pool (created without discard):** enabling `discard_blocks` only affects *future* removals; to reclaim the space a no-discard pool already holds, recreate the pool fresh (stops containerd + docker, root, `dmsetup`/`losetup` surgery — out of scope for yoloai's unprivileged socket path, so it's an operator step, not a yoloai command): stop the daemons, `dmsetup remove containerd-pool`, detach its loop devices, delete + `truncate` fresh sparse `data`/`metadata` files, re-attach via `losetup`, zero the metadata superblock, `dmsetup create containerd-pool --table "0 <sectors> thin-pool <meta> <data> 128 32768"`, restart containerd + docker. yoloai's `yoloai-base` devmapper snapshot rebuilds on the next `--isolation vm-enhanced` sandbox.

**Code:** `runtime/containerd/prune.go` — `snapshotterNames` (`{overlayfs, devmapper}`), `snapshotInfos` (Walk returning each snapshot's `Info` incl. `Parent`; `present=false` skips an unconfigured snapshotter), `orderLeafFirst` (Kahn topological pass; see below), `pruneSnapshots`/`pruneSnapshotter` (iterate both, remove leaf-first, sum each removed snapshot's `Usage`, print the devmapper caveat), `CacheUsage` (sums `Usage` across both into `ImageBytes`, per-snapshotter breakdown in `Detail`).

---

### containerd: snapshots must be removed leaf-first (children before parents) or removal silently stalls

**Symptom:** `prune --images` removes some snapshots but leaves a chain behind; `SnapshotService.Remove` returns `cannot remove snapshot with child: failed precondition` for layers that still have descendants. A single arbitrary-order `Walk`+`Remove` pass only deletes the chain's leaves, leaving the bulk to be reclaimed by a later GC (which doesn't always root them).

**Explanation:** Image layers form parent→child snapshot chains. containerd refuses to remove a committed snapshot that still has a child. To free a whole chain synchronously you must remove children before their parents.

**Fix:** Order removals leaf-first via a Kahn topological pass over the in-memory `Parent` links (`orderLeafFirst`): enqueue snapshots with no in-set child, emit each, decrement its parent's child-count, enqueue the parent when it reaches zero. Every `Remove` then succeeds in one pass and the returned reclaim total reflects bytes actually freed — no reliance on a later GC. Any snapshot left un-emitted (cycle, or a parent outside the set) is appended at the end so nothing is silently dropped.

**Code:** `runtime/containerd/prune.go` `orderLeafFirst`, called by `pruneSnapshots`.

---

### containerd: image import inconsistently materializes overlayfs snapshots

**Symptom:** After `yoloai system build --backend containerd`, sometimes the import unpacks the image into overlayfs snapshots (e.g. 28 snapshots, so `system disk` immediately reports the footprint) and sometimes it only links the image (0 snapshots, `system disk` reports 0 image bytes for the namespace) — with no change in the command.

**Explanation:** The containerd import/link path doesn't deterministically unpack layers into the snapshotter; whether snapshots materialize at import time vs. lazily at first container `run` varies. `client.WithNewSnapshot` likewise does **not** unpack (see [`WithNewSnapshot` does NOT unpack image layers](#withnewsnapshot-does-not-unpack-image-layers)). So a freshly-built containerd image may carry content blobs but zero snapshots until a container is created from it.

**Consequence for testing:** to get a containerd snapshot footprint to size/prune, create a sandbox (the normal `run` path unpacks via `img.Unpack`) rather than relying on the build to materialize snapshots. Avoid `ctr images mount` for this — see the lease entry below.

---

### containerd: a leftover lease GC-roots an orphaned child, blocking base-layer removal

**Symptom:** `prune --images` removes every layer except the base, which refuses removal with `cannot remove snapshot with child: failed precondition` — yet `ctr -n yoloai snapshots ls` (and every other namespace) shows **no** snapshot claiming it as parent. Retrying `Remove` keeps failing; the snapshot only disappears after the responsible lease is deleted and GC runs.

**Explanation:** A lease with a `containerd.io/gc.expire` label (created automatically by `ctr images mount`, among others) GC-roots the snapshots it pinned, including an active/View child of the base layer. That child keeps the base un-removable, but it isn't a normal committed snapshot so it doesn't appear in `snapshots ls`. The synchronous `Remove` precondition check still sees it. Dropping the lease lets the next GC pass collect both.

**Consequence:** This is a **test-scaffolding artifact** (a leftover `ctr images mount` lease), not something yoloai's own create/destroy/prune flow produces — yoloai never creates such leases. If you manually `ctr images mount` to populate a testbed, `ctr -n yoloai leases rm <id>` afterward, or expect the base layer to linger until the 1-hour `gc.expire` elapses.

---

### Kata: orphaned snapshots from crashed runs must be pre-cleared

When a Kata container run crashes after snapshot creation but before container
deletion, a snapshot without a corresponding container record is left behind.
The next `NewContainer()` with `WithNewSnapshot` fails because a snapshot of
the same name already exists.

Must call `r.client.SnapshotService(snapshotter).Remove(ctx, name)` before
`NewContainer()` in addition to the existing stale-container pre-clear. Errors
are silently ignored (snapshot may not exist). See `lifecycle.go::Create` and
commit bf23e95.

---

### `task.Start` returns before the VM is actually running

For Kata Containers (full Linux VM boot), `task.Start` returns as soon as the
shim acknowledges the `Start` RPC — the VM is still in `Created` state and
may take 10–60 seconds to reach `Running`. Callers that check running state
immediately after `Start()` returns will see `Created`.

Must poll `task.Status()` until the status is `Running` or `Stopped`. The
60-second timeout is chosen based on observed Kata boot times (Dragonball ~5s,
Firecracker ~10s on fast hardware; slow CI can be 30s+). See `lifecycle.go::Start`.

---

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
`runtime/tart/tart.go` (180s), `orchestrator/create_instance.go::effectiveSecretsConsumedTimeout`.

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

---

### `netns.NewNamed()` switches the OS thread via `unshare(CLONE_NEWNET)` and never restores it

`netns.NewNamed()` internally calls `unshare(CLONE_NEWNET)`, which moves the
**calling OS thread** into the new network namespace and does not restore it.
When CNI plugin executables are subsequently spawned, they inherit the switched
OS thread's namespace. The bridge plugin then sees `CNI_NETNS == current netns`
and rejects the call with "netns and CNI_NETNS should not be the same".

Fix: call `runtime.LockOSThread()` before `netns.NewNamed()`, then manually
save (`netns.Get()`) and restore (`netns.Set(origNS)`) the original namespace
around the call. See `cni.go::createNetNS`.

---

### Stale named netns files at `/var/run/netns/<name>` persist after failed runs

If a previous run failed after `createNetNS()` but before `teardownCNI()` had a
chance to call `deleteNetNS()`, the named netns file persists at
`/var/run/netns/yoloai-<name>`. The next run's `netns.NewNamed()` fails with
"file exists" (EEXIST).

Must call `deleteNetNS(nsName)` unconditionally before `createNetNS()`. This is
safe because `deleteNetNS` is idempotent (ignores ENOENT). See `cni.go::setupCNI`.

## Tart (macOS VMs)

### `tart list` reports a pulled OCI image twice (tag + digest) over one on-disk copy; sizing and prune must dedup and remove both rows

**Symptom:** `yoloai system disk` reported tart as `IMAGES: ?` and `CACHE: 0 B` while `~/.tart` held **~56 GiB**, and `yoloai system prune --images` reported **0 reclaimed** even though it removed the base image. Tart implemented `PruneCache` but **no `DiskUsageReporter`**, so `CacheUsageFor` returned `ImageBytes=-1` ("unknown", rendered `?`) and the reclaim came back hardcoded `0`.

**Explanation (verified 2026-05-29, Tart 2.31.0, Apple Silicon):** a single pulled OCI base (`ghcr.io/cirruslabs/macos-sequoia-base:latest`) appears as **two** `tart list` rows — one by tag (`:latest`) and one by digest (`@sha256:…`) — both reporting the same `Size` (e.g. 31 GB) but backed by **one** on-disk directory under `~/.tart/cache/OCIs/<repo>/sha256:<digest>/`. Naively summing `tart list` Size double-counts the OCI base; and `tart delete <tag>` removes only the tag row, leaving the digest row pinning the on-disk copy, so a tag-only prune frees ~0. The provisioned local VM (`yoloai-base`) is a separate clone under `~/.tart/vms/` with its own footprint (additive, no sharing). `tart list --format json` Size is **whole-GB** (decimal, rounded), so the figure is coarse (±~0.5 GB/image) but reconciles with `du`.

**Fix:** Tart now implements `DiskUsageReporter`. `CacheUsage` sums the provisioned VM + the base-repo OCI rows **deduped to one** (max Size per repo, mirroring the podman "count shared once" approach), reporting it as `ImageBytes` (tart has no no-rebuild cache → `CachedBytes` always 0). `PruneCache` deletes the provisioned VM **and every base-repo OCI row** (tag *and* digest), then reports reclaim as the `CacheUsage` before−after delta (D37), same as docker/podman. Scope is deliberately yoloai's base images only — not every VM tart tracks, nor live sandbox clones — so the IMAGES column reconciles with what `prune --images` actually frees (unlike docker/podman, tart is the user's general VM tool and must not imply it'll delete unrelated personal VMs). Result: tart now reports **55.88 GiB** (matching `du`'s ~56 GiB) and the dry-run estimate includes it.

**Code:** `runtime/tart/diskusage.go` (`CacheUsage`, `ownedImageBytes`, `ownedImageRefs`, `baseImageRepo`); `runtime/tart/prune.go::PruneCache` (before/after delta, deletes all owned refs). Tests: `diskusage_test.go::{TestBaseImageRepo,TestCacheUsageCountsOwnedImagesDedupingOCI,TestPruneCacheReportsReclaimDelta,TestPruneCacheDryRunReturnsEstimate}`.

---

### VirtioFS only supports directory mounts, not individual files

`tart run --dir name:path` only accepts directories. Any per-file bind mount
(e.g. a `/run/secrets/API_KEY` file) is silently skipped — no error is returned
by `tart run`, the file simply does not appear inside the VM.

Workaround: copy file contents into a sandbox directory and share the directory
via VirtioFS. For secrets, copy all secret files into `sandboxDir/secrets/` and
share `sandboxDir` as the `yoloai` VirtioFS share. See `tart.go::Create`.

---

### VirtioFS mount path inside the VM contains spaces

Tart mounts VirtioFS shares at `/Volumes/My Shared Files/<share-name>` inside
the macOS VM. The path contains a space. Any shell command constructing this
path must quote it. The setup script uses: `'%s/bin/sandbox-setup.py'` with
`%s = /Volumes/My Shared Files/yoloai`. See `tart.go::runSetupScript`.

---

### `ln -sfn` won't replace a directory; must use `rm -rf` first

Inside the Tart VM, when creating symlinks from expected mount target paths to
VirtioFS paths, `ln -sfn target link` silently creates the symlink *inside* the
target directory rather than replacing it, if a directory already exists at
`link`. Must explicitly `rm -rf link` before `ln -sfn`. See the symlink command
in `tart.go::runSetupScript`.

---

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

---

### `tart run` process must use `exec.Command`, not `exec.CommandContext`

`tart run <vmName>` is a long-lived process that keeps the VM alive. Using
`exec.CommandContext` with the parent's context would kill the VM when the
`Start()` function's context is cancelled (e.g. on HTTP request completion or
timeout). Must use bare `exec.Command`, then set `SysProcAttr{Setpgid: true}`
to detach it from the parent process group. See `tart.go::Start`.

**Belongs here:** the surprise is Go stdlib's `exec.CommandContext` killing the child when its context is cancelled — so a request-scoped context must never own the long-lived VM process. That Go behavior is the trap; which context we pass is our part.

---

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

---

### macOS guest BSD `sleep` rejects `sleep infinity` (GNU-only)

**Symptom:** On a Tart (macOS) guest — or any BSD `sleep` — `sleep infinity` exits 1 with `usage: sleep number[unit] ...`. The `idle` agent used `sleep infinity`, so on Tart the agent pane exited immediately and the sandbox went `failed`; the Linux/GNU container backends (docker/podman/containerd/apple) were unaffected.

**Explanation:** `sleep infinity` is a GNU-coreutils extension. macOS/BSD `sleep` accepts only a numeric duration, so `infinity` is an argument error. The launch is `exec sleep infinity`, so the failure killed the pane and the agent never came up.

**Fix:** Keep-alive commands must be portable. The `idle` agent uses `tail -f /dev/null` — blocks event-driven (kqueue on BSD, inotify on Linux) at ~0% CPU and works on both. Any future "idle forever" command must avoid `sleep infinity`.

**Code:** `internal/agent/agent.go` — the `idle` agent's `InteractiveCmd`/`HeadlessCmd`.

---

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

**Caveat — older logs may show "instance not found" for an unrelated exec failure.** Until DF30, `runTart` funneled every `tart exec` through `mapTartError`, which turned *any* inner-command stderr containing `"no such"`/`"not found"` into `runtime.ErrNotFound` → "instance not found" — so a guest command that simply failed (e.g. `ln: /mnt/test: No such file or directory`) reported the same message as a not-yet-ready guest agent. DF30 fixed that (exec stderr is now surfaced verbatim), so a genuine stabilization-race failure is no longer masked by look-alikes. Still, when reading **pre-DF30 logs**, confirm the VM was actually unreachable (`isRunning` false, or a trivial probe exec also failed) before attributing "instance not found" to this race.

**Code:** `runtime/tart/tart.go::Start` after `waitForBoot`

---

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

---

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

---

### Tart attach renders ASCII: tmux downgrades a non-UTF-8 client

**Symptom:** Under Tart (not Docker), the attached agent renders in "ASCII compatibility mode" — Claude Code's logo shows as `_______` instead of `▐▛███▜▌`, and status-line emoji (🏠 📁 🤖 🧠) and the `⏵⏵` glyph all show as `_`. Looks identical to the old "TERM unset → safe defaults" failure, but the agent's environment is fully healthy (`TERM=tmux-256color`, `LANG=en_US.UTF-8`, `LC_CTYPE=C.UTF-8`).

**Tell:** Selecting the screen and copying gives ASCII (`_______`); letting *tmux* copy the same region (copy-mode / OSC 52) gives Unicode (`▐▛███▜▌`). So tmux's server grid holds correct UTF-8 — the downgrade happens when tmux *paints* to the client. `tmux -S … list-clients -F '#{client_utf8}'` reports `utf8=0`.

**Explanation:** tmux decides UTF-8 support per *client*, from the first of `LC_ALL`/`LC_CTYPE`/`LANG` that is set containing "UTF-8". `yoloai attach` runs `tmux attach` via `tart exec`, and a `tart exec` session inherits an empty/`C` locale (none of those vars are set). tmux therefore flags the client `utf8=0` and substitutes `_` for every non-ASCII glyph on output — even though the agent (server side) was launched with a UTF-8 locale and its grid is UTF-8. Docker/containerd avoid this because the container image carries `LANG=C.UTF-8` (`orchestrator/launch`), which the in-container tmux client inherits. macOS VMs have no `C.UTF-8` locale (only `<lang>_<REGION>.UTF-8` forms), so the container trick doesn't port.

**Fix:** Pass `-u` to the attach client (`tmux -u … attach`). `-u` forces tmux to treat the terminal as UTF-8 regardless of locale — exactly the case here (the user's outer terminal is UTF-8; only the intermediate `tart exec` session dropped the locale hint). Verified on a live VM with a throwaway server: plain client → `utf8=0`; `tmux -u` → `utf8=1`; `env LC_ALL=en_US.UTF-8 tmux` → `utf8=1`. `-u` is preferred over injecting a locale because it needs no valid-locale lookup on macOS and no guest-env plumbing through `tart exec`.

**Code:** `runtime/tart/tart.go::AttachCommand` (prepends `-u`). Container equivalent: `orchestrator/launch/launch.go` (`ContainerEnv: ["LANG=C.UTF-8"]`).

---

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

---

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

---

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

---

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

**Code:** `runtime/tart/tart.go::ResolveCopyMount`, `runtime/tart/tart.go::Create`, `orchestrator/lifecycle.go::Reset` (needs implementation)

---

### A host-side change probe is blind to the in-VM workdir — `info` showed `Changes: no` on a dirty Tart sandbox, and `destroy` skipped its gate

**Symptom:** A Tart sandbox with real, unapplied work (`yoloai diff x` lists a new file) reported `Changes: no` in `yoloai sandbox x info`, and `yoloai destroy x` tore it down **without** demanding `--abandon-unapplied`. Silent data loss.

**Root cause:** Because [VirtioFS corrupts git repositories](#virtiofs-corrupts-git-repositories), the working copy of a Tart `:copy`/`:overlay` sandbox lives on **VM-local storage**, not on the host. The host-side `work/` tree is only the *seed* copied in at creation — it never receives the agent's edits. The old change probe ran `git status --porcelain` against that host seed, so it always saw the pristine baseline and answered "no changes." The destroy/replace gate trusted that answer and let the teardown through. (Host-bind-mount backends — Docker/Podman/Containerd/Seatbelt — are unaffected: their workdir *is* the host path, so a host probe is correct.)

**Fix:** Route change detection through the runtime via `runtime.GitExecFor` (`patch.HasUnappliedWorkVia`), so the probe runs *inside* the VM where the real working copy lives, exactly like `diff`/`apply`. The probe is tri-state (`WorkClean`/`WorkDirty`/`WorkUnknown`): when the VM-local backend is **stopped**, its `GitExec` returns `runtime.ErrNotRunning` and the probe reports `WorkUnknown` — the change state genuinely can't be read from the host. Callers **fail safe** on `WorkUnknown`: `info`/`list` surface `Changes: unknown` (public `yoloai.ChangesUnknown`), and the destroy/replace and reset gates block with a "sandbox is stopped, so unapplied changes can't be verified (start it to check, or use --abandon-unapplied)" message rather than reading a stale host seed and silently proceeding.

**Why not just read the host seed:** there is no coherent host-side view to read — that's the whole reason git runs in-VM (see the VirtioFS section above). A host probe isn't merely stale, it's structurally incapable of seeing in-VM edits.

**Code:** `copyflow/changes.go::HasUnappliedWorkVia` (+ `WorkProbe` tri-state), `runtime/runtime.go::GitExecFor`/`ErrNotRunning`, gates in `internal/orchestrator/create/create.go`, `internal/orchestrator/lifecycle/reset.go::NeedsConfirmation`, and the read-model in `internal/orchestrator/status/status.go::detectWorkdirChanges`. The engine opens the backend best-effort (`Engine.TryEnsure`) before the gate so a running VM can be probed.

**Belongs here:** the external constraint is VirtioFS forcing the work copy to live in-VM (host-side git corrupts it — see [VirtioFS corrupts git repositories](#virtiofs-corrupts-git-repositories)), which is *why* a host-side probe is structurally wrong; the probe bug it caused was ours.

---

### Provisioned tool dirs live only on the *login* PATH (Cirrus base image)

**Environmental fact:** The Cirrus-based Tart image composes its tool PATH in `~/.zprofile` — Homebrew, keg-only `node@22`, and `~/.local/bin` where the native Claude Code binary lives. That file is sourced only by *login* shells. The agent, however, is launched via `tart exec bash -c` (non-login) and, on restart, from Go via `respawn-pane`; neither sources `~/.zprofile`, so without intervention `claude` is not on PATH and the agent silently fails to start (a shell prompt, no agent process).

**How yoloAI handles it:** The backend's launch wrap (`PATH="$HOME/.local/bin:/opt/homebrew/opt/node@22/bin:/opt/homebrew/bin:$PATH"`) is a compile-time constant declared on the backend descriptor (`BackendDescriptor.AgentLaunchPrefix`). It is computed once at sandbox creation and stored as `agent_launch_prefix` in `runtime-config.json` — the single source of truth. Every launch path prepends that stored value: Go restart in `lifecycle/restart.go` and Python first-launch in `sandbox-setup.py` both read the field directly (older sandboxes are backfilled by the v1→v2 schema migration), so the two paths can never drift. (Historical note: an earlier base installed Claude Code via npm with a `#!/usr/bin/env node` shebang that the Cirrus image's `node@24` shadowed; switching to the native standalone binary removed that whole class of node-version shadowing, but the agent still needs `~/.local/bin` on the non-login PATH.)

**Code:** `runtime/tart/tart.go` (descriptor `AgentLaunchPrefix` + `PrepareAgentCommand`), `runtime/tart/build.go` (provisionCommands compose the login PATH), `orchestrator/create/create.go` (stores the prefix), `orchestrator/lifecycle/restart.go` (relaunch prepends it), `config/schema.go` (v1→v2 backfill)

---

### Tart: vmnet session wedges on a long-idle VM (host sleep / subnet re-pick) — guest drops to a 169.254 link-local address, agent gets ConnectionRefused

**Symptom:** The agent in a Tart sandbox left idle for days can no longer
reach its API: Claude shows `Unable to connect to API (ConnectionRefused)`
and `(FailedToOpenSocket)` and retries forever. The VM is running and
`tart exec` works normally (its control channel is Virtualization.framework,
not IP), but `tart ip <vm>` returns "no IP address found" and the guest's
`en0` holds a self-assigned `169.254.x.x` address.

**Why:** two compounding host-side facts, observed 2026-07 on a sandbox
idle for one week.

1. macOS re-picks the vmnet shared subnet across host sleep / network
   transitions. The guest's original gateway was `192.168.64.1`, but the
   host's `bridge100` had moved to `192.168.139.3/23`. The guest's DHCP
   lease expired and its renewals were answered by nobody — the DHCP
   server it knew no longer exists on that subnet — so it fell back to a
   link-local address.
2. The vmnet session backing the long-running `tart run` process wedges
   outright. Even after manually configuring the guest onto the new
   subnet (`sudo ipconfig set en0 MANUAL <ip> <mask>` + default route),
   **zero frames crossed the link**: ARP stayed `(incomplete)` in *both*
   directions between guest `en0` and host `bridge100`, and host→guest
   ping was 100% loss. The stale addressing is a symptom; the dead L2
   link is the disease. A wedged vmnet session cannot be reattached from
   user space — only recreating the virtio NIC (a VM restart) recovers it.

**Diagnosis ladder:** `tart ip <vm>` → nothing, on a VM `tart list` says
is running; in-guest `/sbin/ifconfig en0` → `inet 169.254.*`; in-guest
`sudo ipconfig set en0 DHCP` never obtains a lease; a manual static IP on
the host bridge's current subnet still can't ARP the gateway (both-ways
`(incomplete)`) — that last step rules out "merely stale lease" and
confirms the wedge.

**Fix for the user:** restart the VM: `yoloai stop <name> && yoloai start
<name>`. The VM disk persists, so the `:copy` workdir and the agent's
on-disk session state survive; resume the agent's conversation after the
restart (e.g. Claude's `--resume`). In-guest network surgery is pointless
— don't spend time on it once ARP shows both-ways `(incomplete)`.

**Fix in code:** none yet — detection/surfacing is designed in
[`design/plans/tart-network-liveness.md`](design/plans/tart-network-liveness.md).
A running VM whose `en0` is link-local is a reliable, cheap tell.

## Seatbelt (macOS sandboxing)

### Seatbelt has no backend image/cache store — `CacheUsage`/`PruneCache` are correctly absent

**Symptom / question:** `yoloai system disk` shows seatbelt as `IMAGES: ?` and `CACHE: 0 B`. Is that a reporting gap like the Tart one was?

**Explanation (verified 2026-05-29):** No. Seatbelt runs agents **directly on the host** via `sandbox-exec` using the host's own tools — its `Setup` only *checks* that required binaries are on `PATH` (`runtime/seatbelt/build.go`); it pulls/builds/caches **nothing**. There is no VM, no image, no layer store. The only on-disk state a seatbelt sandbox accumulates is the per-sandbox directory under `~/.yoloai/sandboxes/<name>/` (work dirs, agent-state, logs) — and that's already reported by the `sandboxes` row of `system disk`, the same for every backend. So seatbelt implements neither `DiskUsageReporter` nor `CachePruner`, and its core `Prune` is a no-op (no central registry of instances). The `?` in the IMAGES column is `CacheUsageFor`'s "unknown" fallback (`ImageBytes=-1`); it's cosmetically imperfect (a true "—"/0 would read better) but functionally correct — there is genuinely nothing for `prune`/`prune --images` to reclaim. **Leave it a no-op; do not invent a cache to measure.**

**Code:** `runtime/seatbelt/build.go::Setup` (PATH check only), `runtime/seatbelt/prune.go` (no-op `Prune`, no `PruneCache`/`CacheUsage`); fallback in `runtime/runtime.go::CacheUsageFor`.

**Belongs here (caveat):** this is an architectural invariant, not a tool quirk — Seatbelt is a host-process backend with no image store. Recorded so nobody hunts for a cache (or files the `?` in the `IMAGES` column) that structurally cannot exist.

---

### macOS `sandbox-exec` doesn't nest — Swift PM needs the swift-wrapper sourced

**Environmental fact:** macOS sandboxes don't support nesting, so a project's own Swift PM commands — which internally invoke `sandbox-exec` — fail inside a Seatbelt sandbox with nesting errors. The workaround is `~/.swift-wrapper.sh`, which intercepts swift commands and adds `--disable-sandbox`; it must be sourced into the agent's shell before launch, or Swift build/test breaks.

**How yoloAI handles it:** The backend's launch wrap (`source ~/.swift-wrapper.sh && `) is a compile-time constant declared on the backend descriptor (`BackendDescriptor.AgentLaunchPrefix`), computed once at sandbox creation and stored as `agent_launch_prefix` in `runtime-config.json` — the single source of truth. Both launch paths prepend that stored value: Go restart in `lifecycle/restart.go` and Python first-launch in `sandbox-setup.py` read the field directly (older sandboxes are backfilled by the v1→v2 schema migration), so the wrapper is sourced identically whether the agent starts via the Python path or a later Go-driven restart.

**Code:** `runtime/seatbelt/seatbelt.go` (descriptor `AgentLaunchPrefix` + `PrepareAgentCommand`), `orchestrator/create/create.go` (stores the prefix), `orchestrator/lifecycle/restart.go` (relaunch prepends it), `config/schema.go` (v1→v2 backfill)

---

### Agent dies silently (SIGTRAP) — SBPL subpath rules must use vnode-resolved paths

**Symptom:** Under Seatbelt the agent (claude/Node) dies 0.5–3.5s after launch with no output; the tmux pane is already dead at the post-launch check. `sandbox-exec -f profile.sb claude --version` exits 133 (128+5 = SIGTRAP). A `.ips` crash report in `~/Library/Logs/DiagnosticReports/` shows `EXC_BREAKPOINT`/`SIGTRAP` ("pointer authentication trap IB") on the main thread inside ICU `std::__call_once` / `uenum_count`. The macOS unified log shows `deny file-read-data /private/var/db/timezone/...`.

**Explanation:** macOS firmlinks `/var` → `/private/var` (also `/etc`, `/tmp`), and the sandbox enforces access at the **vnode level — after symlink resolution**. An SBPL rule for `(subpath "/var/db")` does **not** match a read of the resolved `/private/var/db`. ICU loads timezone data from `/private/var/db/timezone/tz/<ver>/zoneinfo/...` at startup; when that read is denied, ICU aborts the process via SIGTRAP before any agent output. `writeProfileSystemPaths` was the only profile section that emitted raw `systemReadPaths()` entries without running them through `resolvePathVariants`, so `/var/db` and `/var/run` rules never covered their `/private/var/...` targets.

**Fix:** Wrap every `systemReadPaths()` entry in `resolvePathVariants()` so the resolved `/private/var/...` variant is emitted alongside the original — matching what every other profile section already does.

**Code:** `runtime/seatbelt/profile.go::writeProfileSystemPaths` (+ `resolvePathVariants`); regression test `seatbelt_test.go::TestGenerateProfile_SystemPathsSymlinkResolved`

---

### Seatbelt: `sandbox-exec`-wrapping git for confinement has two escape surfaces (mach-lookup, process-exec) + the `/usr/bin/git` shim can't run confined

**Context:** to close the copy-mode RCE (audit C1, `confine-host-side-git.md`), seatbelt runs work-copy git under a dedicated `sandbox-exec` SBPL profile so any agent-planted `filter.<x>.clean` inherits the confinement. Three non-obvious macOS facts shaped that profile (all verified on macOS 26, Apple Silicon):

1. **`/usr/bin/git` is Apple's `xcrun` shim, not git.** It re-invokes `xcrun`/`xcodebuild`, which need the per-user Darwin temp dir (`confstr(_CS_DARWIN_USER_TEMP_DIR)`), a writable `/tmp/xcrun_db` cache, and `mach-lookup` — none of which a tight git profile grants. Under the profile the shim dies with `couldn't create cache file '/tmp/xcrun_db-…' (errno=Operation not permitted)` and `unable to load libxcrun (file system sandbox blocked open())`. **Fix:** resolve the REAL toolchain binary via `xcrun -f git` (outside the sandbox) and exec *that* under `sandbox-exec`, bypassing the shim entirely (`seatbelt.resolveGitBinary`). The profile then allows `process-exec` on the resolved toolchain's Developer dir so git's `libexec/git-core` subcommands run.

2. **`mach-lookup` is the primary escape vector — and git does NOT need it.** A `(deny default)` profile already denies `mach-lookup`; the surprise is that git, its clean/textconv filters, `/bin/sh`, and even **git-lfs (a Go binary)** all run correctly for `status`/`add`/`diff`/`format-patch` with `mach-lookup` fully denied. So no allowlist is required — leave it denied. (The permissive *agent* profile grants `(allow mach-lookup)` unrestricted, which is why the git op must NOT reuse the agent profile.)

3. **A writable temp grant re-opens the hole.** git needs no writable dir outside the work copy for the diff path, so the profile grants `file-write*` on the **work copy only**. It is tempting to also allow `/private/tmp` or `/private/var/folders` "for git temp" — doing so lets a malicious `filter.pwn.clean` write a marker to `/tmp` or the per-user temp and **defeats containment**. Likewise `process-exec` must be confined to tool dirs (system bins, `/opt/homebrew`, the toolchain Developer dir) — never the work copy — so a payload the filter drops in-tree cannot be exec'd.

**Net:** the dedicated git profile bounds a malicious filter to container-equivalent blast radius (run installed tools + read/write the work copy, no host escape). Behaviorally teeth-checked: with confinement off the marker leaks; on, it is contained.

**Code:** `runtime/seatbelt/profile.go::GenerateGitProfile`, `runtime/seatbelt/seatbelt.go::{GitExec,resolveGitBinary}`; tests `runtime/seatbelt/gitprofile_test.go`, `internal/orchestrator/integration_macos_test.go`.

---

### Interactive error output "stair-steps" — local-PTY backends must bridge, not inherit host stdio (also Tart)

**Symptom:** On seatbelt (and tart), when an interactive command fails early — e.g. `tmux` can't open its socket — the error message cascades down-and-to-the-right, each line starting one column further right than the last, instead of printing as clean left-aligned lines.

**Explanation:** The CLI boundary (`cliutil.WithTerminal`) puts the host tty in raw mode (`term.MakeRaw`) for *every* interactive command, which clears `OPOST`/`ONLCR` — so a bare `\n` no longer gets an implicit carriage return. The bridged backends (docker/podman/containerd) are unaffected because their child runs under a *remote* PTY whose slave still has `OPOST` on, emitting proper `\r\n` that the library copies verbatim. Seatbelt and tart used to hand the child the host's `os.Stdin/Stdout/Stderr` directly (`cmd.Stdout = streams.Out`); the child then wrote bare `\n` into the raw host tty → stair-step. Inheriting host `*os.File`s also violated the `IOStreams` abstraction — it only worked when the streams happened to be real terminals, breaking any non-CLI embedder.

**Fix:** Run the child under a *locally* allocated PTY (`runtime.PTYBridgeExec`, via `creack/pty.StartWithSize`) and `io.Copy` the master to the caller's streams — the same model docker uses, but with a host-local PTY. The PTY slave keeps `OPOST` on, so the child emits `\r\n` and the raw host tty renders it correctly. This also makes `IOStreams.Resize` work uniformly (forwarded via `pty.Setsize`) and keeps both backends embedder-safe. **Tart caveat:** `tart exec -t` already allocates a PTY inside the VM, so wrapping it locally is a double-PTY (local + remote, like `script ssh -t`) — correct, but only exercisable on a macOS host with Tart.

**Code:** `runtime/interactive_pty.go::PTYBridgeExec`; `runtime/seatbelt/seatbelt.go::InteractiveExec`; `runtime/tart/tart.go::InteractiveExec`. The CLI raw-mode owner is `cli/cliutil/streams.go::WithTerminal` (unchanged — now uniform across backends).

**Belongs here:** the durable fact is POSIX raw mode stripping `OPOST`/`ONLCR` CR-translation (a terminal-layer behavior we can't change); our wiring merely triggered it, and the cross-backend rendering divergence is the lesson to keep.

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

**Code:** `runtime/monitor/sandbox-setup.py::TartBackend.setup`, `scripts/smoke_test.py::MACOS_BACKENDS`

---

### Tart: mount_map uses Docker-style paths, triggering macOS automount hang

**Symptom:** `yoloai new --attach` with a Tart VM hangs indefinitely after printing "Sandbox created". Python's sandbox-setup.py stops producing log entries after `tart.symlinks` and never creates the tmux session. The `done` sentinel never appears in smoke tests even after 180s.

**Explanation:** `addMountMapToConfig` writes mount targets into `runtime-config.json`'s `mount_map` using the original Docker-style paths (e.g. `/home/yoloai/.config/git`). Python's `TartBackend.setup()` reads this map and calls `sudo mkdir -p /home/yoloai/.config` to create the symlink parent. On macOS, `/home` is managed by `automountd` — attempting to mkdir inside it triggers a network automount lookup for the `yoloai` home directory, which hangs until the lookup times out (60-120+ seconds). The Go-side `createVMMountSymlinks` correctly applies `remapTargetPath` (mapping `/home/yoloai/...` to `/Users/admin/...`), but the Python-side `mount_map` was missing this translation.

**Fix:** Apply `remapTargetPath` to mount targets in `addMountMapToConfig` before writing to `mount_map`. Python now receives `/Users/admin/.config/git` instead of `/home/yoloai/.config/git` and creates the parent dir at a valid macOS path with no automount involvement.

**Code:** `runtime/tart/tart.go::addMountMapToConfig` (apply `remapTargetPath`), `runtime/monitor/sandbox-setup.py::TartBackend.setup` (uses mount_map targets)

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

**Code:** `orchestrator/create.go::buildMounts` (vscodeTunnel section)

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

**Code:** `runtime/monitor/tmux_io.py` (`tmux_bin`, `_probe_tmux_bin`,
`_in_firstlaunch_context`, `set_firstlaunch_marker`, `_RESOLVE_ATTEMPTS`,
`_FIRSTLAUNCH_MAX_WAIT_SECONDS`); `runtime/monitor/sandbox-setup.py`
(firstlaunch launch in `TartBackend.setup()`, plus `setup_tmux_session` and the
`main()` `wait-for` block).

**Same storm, host side — the baseline-SHA `git` fails with exit 69:** the host
runs the work-dir git baseline (`git init/add/commit`, then `git rev-parse HEAD`)
via `tart exec` in `ExecuteVMWorkDirSetup` (`internal/orchestrator/launch/vmworkdir.go`),
*after* the secrets-consumed barrier — so the monitor has already accepted the
Xcode license, yet `git` (the `xcode-select` shim on macOS) intermittently fails
with **`exit 69: You have not agreed to the Xcode license agreements`**. Cause is
the identical storm: while backgrounded `xcodebuild -runFirstLaunch` runs, the
license check transiently fails. It passes on a cold retry because firstlaunch
state persists in the host Xcode.app via VirtioFS, so the second VM finds it done
and never raises the storm. The failure tends to surface on `rev-parse` rather
than `git init` only because the earlier commands sometimes slip through before
the storm peaks. **Fix:** `ExecuteVMWorkDirSetup` wraps each VM exec in
`execVMSetupWithStormRetry`, which re-probes once per second to the same 240s
ceiling while the error matches the storm signature (`isFirstlaunchStormTransient`:
exit 69 + "Xcode license", or exit 127 command-not-found for binaries the storm
hides). Non-transient errors return immediately; the happy path runs once and
never sleeps. Mirrors the tmux resolver's "probe to a ceiling, ignore completion"
strategy because the host cannot observe the firstlaunch marker.

### Tart: the fall-to-shell wrapper path must derive from `yoloai_dir`, not the container `/yoloai`

**Symptom:** a `stop_start`/`tag_transfer` smoke test fails **only on Tart** with
"sentinel 'done' not seen in 180s" and the autopsy "Python traceback in guest setup".
The preserved `terminal-snapshot.txt` shows the pane died immediately after launch:

```
… && exec /yoloai/bin/agent-run.sh claude --dangerously-skip-permissions …
zsh: no such file or directory: /yoloai/bin/agent-run.sh
Pane is dead (status 127, …)
```

`setup.log` then shows `NameError: name 'log_error' is not defined` from
`deliver_prompt` — a red herring that only fires *because* the pane is already dead.

**Explanation:** `sandbox-setup.py`'s `launch_agent` hardcoded the fall-to-shell
wrapper (D96) as the container path `/yoloai/bin/agent-run.sh`. That path only exists
on container backends, where `YOLOAI_DIR=/yoloai`. On Tart the yoloai dir is the
VirtioFS share `/Volumes/My Shared Files/yoloai` (the backend writes `agent-run.sh`
there and passes that path to the script as `sys.argv[2]`); there is **no** `/yoloai`
in the VM (only `/Users/admin/.yoloai`, a symlink to the share). So `exec` failed and
the agent never ran, so the agent never created the `files/done` sentinel the harness
waits on. Two compounding failures hid the root cause: (1) `deliver_prompt` then tried
to paste into the dead pane, hit the undefined `log_error`, and crashed `main()`
**before** `launch_monitor` ran — so the monitor never recorded the pane-death `done`
either; (2) the cascade only surfaced on Tart because the Tart VM pane runs **zsh**,
which **exits on a failed `exec`** (pane dies, status 127). The seatbelt pane runs
**bash 3.2**, whose failed-`exec` behavior differs, so the same hardcoded-path bug did
not kill that pane the same way — which is why seatbelt appeared to pass and masked a
backend-agnostic bug as Tart-specific.

**Fix:** derive the wrapper from the per-backend `yoloai_dir`
(`os.path.join(yoloai_dir, "bin", "agent-run.sh")`), exactly as `launch_monitor`
already does for `status-monitor.py` — `/yoloai/bin/...` for containers, the share for
Tart, the sandbox dir for seatbelt. The path is single-quoted in
`build_agent_launch_command` so the spaces in the Tart share (`/Volumes/My Shared
Files/…`) don't split the `exec` argv. Separately, `log_error` was defined (it was only
ever referenced, never declared) so a genuine paste failure logs instead of crashing
the whole setup.

**Code:** `runtime/monitor/sandbox-setup.py` (`launch_agent` wrapper derivation,
`log_error`), `runtime/monitor/setup_helpers.py` (`build_agent_launch_command` wrapper
quoting); tart passes `yoloai_dir` via `runtime/tart/mounts.go` `runSetupScript`.

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
`store/paths.go`) verbatim as the containerd container ID and does
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
ID construction in `store/paths.go` (`InstanceName`).

## OS & POSIX semantics

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

**Code:** `store/lock_unix.go` (`RemoveLockFile`,
`SweepStaleLocks`); `internal/orchestrator/lifecycle.go` (Destroy);
`internal/orchestrator/create.go` (Create rollback).

**Belongs here:** the durable fact is POSIX `flock(2)` semantics — the lock binds to the open fd, not the path — which is what makes eager unlink-while-held safe; the design choice it licenses is ours.

## Apple container (`container` CLI)

### Apple: `--mount type=virtiofs` rejects a file source; use `-v` for file mounts

**Symptom:** `container create … --mount type=virtiofs,source=<file>,target=<file>`
fails with `Error: path '<file>' is not a directory`. yoloai injects
credentials/config as individual file bind mounts (e.g. `~/.claude.json`,
`~/.claude/settings.json`), so a docker-style file mount aborts `yoloai new`.

**Explanation:** Apple's `--mount type=virtiofs` only accepts a **directory**
source. The `-v`/`--volume` bind-mount flag, however, accepts both files and
directories (and the `:ro` suffix), and propagates rw live in both directions.

**Fix:** the apple backend builds every mount as `-v <host>:<guest>[:ro]`, not
`--mount type=virtiofs`. Verified: `-v <file>:<file>` and `-v <file>:<file>:ro`
both work; `-v <dir>:<dir>` propagates rw.

**Code:** `runtime/apple/apple.go` (`Create`, mount loop).

---

### Apple: `container build .` silently drops a relative `.` context; pass an absolute dir

**Symptom:** `container build -t img .` runs the `RUN` steps but every `COPY`
fails with `failed to compute cache key: "/x": not found`; build output shows
`transferring context: 2B` (an empty context).

**Explanation:** Apple's `container build` silently transfers an empty context
for a relative `.` path. An **absolute** context dir works
(`transferring context: …` shows real bytes).

**Fix:** `Setup` materializes the build context to a temp directory and passes
its **absolute** path (`container build -t yoloai-base <abs-dir>`). The builder
VM must also be started first (`container builder start`).

**Code:** `runtime/apple/apple.go` (`buildBaseImage`).

### Apple: `container exec -t` forces ONLCR on the host-local bridge PTY, corrupting the app's column tracking on scroll

**Symptom:** Inside an Apple (`container`) sandbox, a full-screen TUI (Claude
Code) renders fine until you **scroll in tmux**, then lines lose their left
gutter and their first 1–2 characters orphan onto the row above (e.g. `Ra`
above `Ran 2 shell commands`), with occasional single-cell corruption. `Ctrl-b r`
(full redraw) heals it; scrolling re-breaks it. Docker on the same terminal is
clean, and it reproduces in **both** iTerm2 and Terminal.app — so it is not the
emulator.

**Explanation:** These backends run the interactive app under a **remote** PTY
inside the guest (`container exec -t` / `tart exec -t`) wrapped in a **second,
host-local** PTY (`ptybridge`). tmux draws a fixed-column gutter by moving the
cursor **down while staying in the same column** — `cud1`, a bare `\n`. The app
sets its own guest PTY raw, but cannot reach the host-local bridge slave, whose
ONLCR the exec CLI **forces on and re-asserts even if you clear the termios** —
so every `\n` becomes `\r\n`. A bare-`\n` cursor-down thus arrives as
carriage-return-to-column-0, and the app's next write lands 2 columns left of
its gutter, leaving the old leading chars behind. In the byte stream this shows
as **single `\r\n` = a mangled `cud1`** and **`\r\r\n` = the app's own `nel`**;
bare-LF count is 0 because ONLCR already converted them (so counting bare LFs in
the *captured* stream misleads). Docker escapes this — one daemon-side PTY that
tmux itself sets raw, so bare LFs pass verbatim — and so does Seatbelt, whose app
owns and raws the single local PTY.

**Fix:** Since the exec CLI re-asserts ONLCR (termios can't hold), `ptybridge`
undoes it in the output copy under `WithRemotePTY()`: strip the one CR the ONLCR
injected before each LF (`\r\n`→`\n`, `\r\r\n`→`\r\n`; a lone CR is preserved).
Enabled for the apple backend only. **Tart is the same double-PTY shape but was
tested and is *not* affected** — `tart exec -t` does not force ONLCR — so it is
intentionally left disabled; enabling the strip there would corrupt its `nel`.
Seatbelt is single-PTY (the app raws the only PTY) and likewise unaffected.

**Code:** `runtime/ptybridge/bridge.go` (`WithRemotePTY`, `crStripper`),
`runtime/apple/apple.go` (`InteractiveExec`). Related: the OPOST/stair-step note
in the Seatbelt section.

### Apple: build cache lives inside the running builder container, invisible to `system df`'s `reclaimable` field

**Symptom:** `container system df --format json` reports
`containers.reclaimable: 0` even while a multi-GB BuildKit build cache is
sitting on disk; there is no `container build cache prune` command to free it.

**Explanation:** Apple's `container` CLI builds images via a long-running
`buildkit` builder container (`container builder start`, started by yoloai's
`Setup` on every run). Its accumulated build cache is internal state of that
running container, which `system df` counts as "active", not "reclaimable" —
so the reclaimable figure never reflects it, no matter how large the cache
grows. The only lever that frees it is deleting the builder outright
(`container builder delete --force`); it is recreated automatically the next
time `Setup` runs `container builder start`.

**Fix:** `PruneCache` always runs `container builder delete --force` (both
prune depths) instead of relying on any per-category reclaimable count. Since
the builder's freed bytes don't show up as "reclaimable" before deletion, the
prune's honest reclaim figure is measured as the drop in `system df`'s **total**
`sizeInBytes` (images + containers + volumes) taken before vs. after the whole
prune sequence, not the `reclaimable` field.

**Code:** `runtime/apple/prune.go` (`PruneCache`, `systemDF`, `reclaimDelta`).

## Docker: `procReady not received` usually means the container is exiting, not a broken runtime

**Symptom:** An exec into a yoloai container (`runtime.Launch`, `Exec`, or a raw
`docker exec`) fails with `Error response from daemon: OCI runtime exec failed:
exec failed: unable to start container process: procReady not received`.
Intermittent — sometimes the same test passes.

**Explanation:** This is almost never a broken Docker/runc. `procReady` is runc
reporting that the exec process's init never signalled ready over its sync pipe —
which happens when the container's PID 1 is **gone or going** at the moment of the
exec. The classic cause here is the container **crashing on startup** and the exec
racing the crash: a substrate-only container (created via `runtime.Create` with a
bare `InstanceConfig`, no orchestrator `runtime-config.json`) whose `entrypoint.py`
used to `json.load()` an empty config and exit 1. The exec lands during that brief
dying window → `procReady`; when the exec wins the race the test passes, hence the
flake.

**How to tell it apart (do this before suspecting Docker):**
- Plain `docker exec <vanilla-alpine> echo hi` works → runc is fine.
- `docker exec <the yoloai container> echo hi` a few seconds after start works →
  the container accepts execs once settled; the failure was a startup race.
- `docker ps -a --filter name=<ctr>` shows `Exited (1)` → the container is
  crashing; `docker logs <ctr>` shows why (here: `JSONDecodeError` from
  `read_config`).

**Fix:** Two parts. (1) The entrypoint now treats a missing/empty
`runtime-config.json` as the agent-free keepalive case instead of crashing
(`fa8d7fe5`), so a bare substrate box stays up. (2) Production already gates
`Launch` on readiness (`internal/orchestrator/launch.startViaLaunch` →
`waitForReady` → `Ready()`); tests that exec right after `Start` must do the same
(`launchTestInstance`). Don't add a retry to `Launch` itself — it would mask a
dying container.

**Code:** `runtime/docker/resources/entrypoint.py` (`read_config`, `keepalive`),
`runtime/docker/launch.go` (`Ready`/`Launch`),
`internal/orchestrator/launch/launch.go` (`waitForReady`).

## Claude: the "fullscreen renderer" upsell re-execs claude and drops `--dangerously-skip-permissions`

**Symptom:** Agent smokes (clone, every `stop_start/*` backend) stall — the `done`
sentinel never fires. The preserved `terminal-snapshot.txt` shows claude parked on a
**Bash tool-permission prompt** ("Do you want to proceed? 1. Yes / 2. Yes, and don't
ask again / 3. No") **even though `claude --dangerously-skip-permissions` was launched**
(confirmed in `agent.log`). A *"Try the new fullscreen renderer?"* modal appears earlier
in the same run. Pinning claude to an "older" version does **not** help — the same prompt
appears on 2.1.177.

**Explanation:** Claude Code added a fullscreen-renderer upsell. Decompiling the
`claude.exe` bundle: the upsell-gate `In9()` shows it unless `EK()` (already fullscreen),
`n6().tui !== undefined` (renderer already chosen by the user), `!WF8()`, or a seen-count
cap. When the upsell is **accepted**, claude relaunches itself via
`yTH({freshIfNoTranscript:true, extraArgs})` — and for a fresh session the args are
**only the upsell's own `extraArgs`**, so the original `--dangerously-skip-permissions`
is **dropped**. The re-execed session therefore runs in default *ask* permission mode and
blocks on the first tool call, with no human in the tmux pane to answer. The real-run
`agent.log` corroborates the order: the `fullscreen renderer` text precedes the `[?1049h`
(enter-alternate-screen) escape — i.e. the upsell was accepted, *then* claude switched to
fullscreen via the flagless re-exec. Note `skipDangerousModePermissionPrompt` only skips
the bypass-mode **dialog**; it does not re-select bypass mode after a flagless relaunch.
This is **not** version drift and **not** a yoloAI infra bug (launch/keepalive/tmux/
prompt-delivery are all fine) — only the agent-under-test changed.

**Fix:** Default the renderer to classic at the agent layer — claude's `ApplySettings`
sets `settings.tui = "default"` **only when the user hasn't already chosen a `tui`**. That
makes `In9()`'s `n6().tui !== undefined` check treat the renderer as already chosen, so the
upsell **never appears** → no flagless re-exec → `--dangerously-skip-permissions` stays in
effect. An explicit user `tui` (default *or* fullscreen) is respected as-is — any value
suppresses the upsell, so we never clobber it. `tui` is a real persisted Claude setting
(`"Set the terminal UI renderer (default | fullscreen)"`), so this is version-robust rather
than chasing each new onboarding modal. With the root cause fixed, the Dockerfile claude
pin was removed (no-pin policy restored).

**Code:** `internal/agent/agent.go` (claude `ApplySettings`, `s["tui"] = "default"`);
`internal/agent/agent_test.go` (`TestApplySettings_Claude`).

## gVisor (container-enhanced): `docker exec --user <name>` resolves against the stale image passwd, not the live one

**Symptom:** On the docker backend under `--isolation container-enhanced` (gVisor/
runsc), `yoloai new` returns exit 0 and `yoloai ls` shows `active`, but the agent
never actually runs: the smoke `done` sentinel never fires, `sandbox.jsonl` stops at
`entrypoint.keepalive_only`, `agent-status.json` is `{}`, and inside the container the
only process is `sleep infinity` — no tmux, no `sandbox-setup.py`, no monitor. The same
sandbox on plain docker (runc) works.

**Explanation:** The D88 keepalive+Launch bring-up boots the box on a neutral
keepalive holder, then the host launches `sandbox-setup.py` over it with a detached
`docker exec --user yoloai` (`runtime.ProcessLauncher`). The image ships `yoloai` as
UID **1001**; the entrypoint's uid-remap rewrites the **live** `/etc/passwd` to make
`yoloai` = the **host UID** (e.g. 1000) and chowns `/yoloai` to match. Under **runc**,
`docker exec --user yoloai` re-reads the live passwd → resolves to 1000 → owns
`/yoloai/logs` → the launched process writes its log and runs. Under **gVisor**,
`docker exec --user yoloai` resolves the username against the image's **original**
`/etc/passwd` (snapshotted at container start) → **1001** → the process runs as the
stale UID, which no longer owns the remapped `/yoloai` dirs (now 1000, mode 750). Its
first action — the log redirect `>> /yoloai/logs/session-runner.log` — hits `EACCES`,
so `sh -c 'exec python3 …'` dies at the redirect **before** exec'ing python. The agent
never welds, and because the launch is detached the error is swallowed (`new` still
exits 0). Confirmed directly: `docker exec -u yoloai <gvisor-box> id` → `uid=1001`,
while `docker exec -u 1000 …` and the live `/etc/passwd` both say 1000.

**Fix:** Route `container-enhanced` to the **legacy in-entrypoint weld** instead of
the D88 keepalive+Launch path (`runtime.SupportsAgentFreeLaunch` returns false for it;
`usesAgentFreeLaunch` ANDs it in, so both bring-up and secrets delivery stay in sync).
The legacy path runs `sandbox-setup.py` from the entrypoint's own process tree and
drops to `yoloai` **in-container** (against the live, remapped passwd), so there is no
host-side `exec --user` and no stale-UID write. This mirrors the podman reroute. Note a
numeric `--user 1000:1000` would also write correctly but drops supplementary groups
(the `docker` group needed for dind under `--isolation container-privileged`, which
shares the same `ProcSpec`), so the legacy reroute is preferred over changing the user
form globally.

**Code:** `runtime/isolation.go` (`SupportsAgentFreeLaunch`);
`internal/orchestrator/launch/launch.go` (`usesAgentFreeLaunch` gate);
`internal/orchestrator/launch/launch_reroute_test.go`
(`TestBuildAndStart_ContainerEnhancedTakesLegacyPath`); `runtime/isolation_test.go`
(`TestSupportsAgentFreeLaunch`).

---

## macOS: overlayfs on a VirtioFS bind-mount silently downgrades to a container-local tmpfs upper (uncommitted changes lost on restart)

**Symptom.** A macOS `:overlay` sandbox shows the agent's changes via `yoloai diff`
while it is running, but after `stop`+`start` (or `restart`, or a `kill`) the
changes are **gone** — files revert to their baseline and newly-created files
vanish. The host upper dir (`~/.yoloai/library/sandboxes/<name>/work/<encoded>/upper/`)
is empty the whole time.

**Root cause (external).** Linux overlayfs stores per-file metadata (whiteouts,
opaque markers, origin) in `trusted.*` extended attributes on the upper dir. macOS
container backends bind-mount host directories over **VirtioFS** (OrbStack, Apple
`container`) or **`fakeowner` over VirtioFS** (Docker Desktop), and none of these
expose `trusted.*` xattrs. With no xattr support overlayfs cannot use that upper, so
the mount is unusable for writes. This is a property of overlayfs + VirtioFS, not of
yoloAI — delete all our code and an `overlay` mount with `upperdir` on a VirtioFS
share still fails the same way.

**Our workaround (the thing that makes the loss observable).** `apply_overlays()` in
`runtime/docker/resources/entrypoint.py` (~lines 240-276) detects the read-only
overlay via a write probe and remounts with a **tmpfs-backed upper** at
`/run/yoloai-overlay/<base64>/upper`. It copies any prior host-upper state *in* at
mount time, but nothing copies the tmpfs upper back to the host — **there is no
tmpfs→host sync on shutdown**, graceful or not. So all changes live only in
container-local tmpfs and die with the container. The log line
`overlay.local_upper … "changes won't persist across restarts"` is emitted when this
path is taken.

**Verified 2026-06-30** (macOS 26.5.1, Docker 29.4.0): the fallback triggers and
changes are lost on both graceful `stop`+`start` and non-graceful `kill`+`start`, on
**all three** macOS container backends — OrbStack, Docker Desktop, and Apple
`container`. `yoloai diff` works only because it execs `git` *inside* the live
container against the merged overlay (overlay diff/apply is container-bound).

**Podman Machine (applehv) fails harder — no tmpfs downgrade.** Verified on-device
2026-07-01 (podman 5.8.2, applehv), using the `main` binary's base image. A macOS
`:overlay` create does **not** reach the tmpfs fallback above: the very first
`mount -t overlay` inside the container fails outright — `mount: …/merged: cannot
mount overlay read-only` (exit 32) — so `apply_overlays()` raises and the entrypoint
exits 1 before any write-probe runs. The result is not a lossy-but-running sandbox but
an **incomplete v3 sandbox**: the container is `Exited`, and the host overlay dirs
(`lower/upper/ovlwork/merged`) exist but are often empty (the crash preceded `lower`
seeding). Why podman differs: its guest kernel/overlayfs rejects a VirtioFS `upperdir`
at mount time, where OrbStack/Docker Desktop/Apple `container` let the mount succeed
read-only and only fail the later write probe. Same conclusion — `:overlay` is unusable
on macOS — but the failure mode and on-disk residue differ.

**Consequences.**
- A *stopped* macOS `:overlay` sandbox has already lost its uncommitted changes; do
  not assume the host upper is authoritative on macOS.
- The overlay→copy flatten migration (D109 / v3→v4) treats a stopped or removed macOS
  overlay sandbox via the **abandon** path: the upper is already lost, so it flattens
  onto the pristine `lower` and requires `--abandon-stopped-overlay` (a plain `--yes`
  refuses). Live **capture** (`flattenRunning`) is Linux-only by construction — a macOS
  host can never hold a live overlay container (podman fails the mount; the others run
  only with a container-local tmpfs upper). Verified end-to-end on macOS 2026-07-01:
  podman (stopped→abandon) and docker (removed→abandon). An earlier draft of this entry
  assumed the flatten would convert "while the sandbox is live" on macOS — that path is
  unreachable there. See DF69 and
  [design/research/reflink-vs-hardlink.md](design/research/reflink-vs-hardlink.md)
  §B/§C.

**Code:** `runtime/docker/resources/entrypoint.py` (`apply_overlays`, the
`overlay.virtofs_fallback` / `overlay.local_upper` branch).

## Linux: overlay flatten migration and host-side ownership of container-written state

**Symptom:** on Linux, `yoloai system migrate` (the v3→v4 overlay flatten) of a
real running `:overlay` sandbox behaves differently by backend:
- **rootful Docker:** the migration used to fail at the dispose step with
  `drop orig: openfdat …/work/<enc>/ovlwork/work: permission denied` — *after*
  the sandbox was already converted to `:copy` (data safe) but *before* the realm
  stamped v4, leaving it at v3 with a leftover `_^^_orig` sentinel that wedged
  re-runs.
- **podman-rootless:** the migration refuses up front with `cannot migrate
  sandbox "X": its runtime state (agent-status.json) is owned by uid 100999, not
  you (uid 1000) …`.

**Explanation.** The flatten runs host-side: it captures the container's merged
overlay tree, then the crash-safe promotion repopulates the sandbox's non-`work/`
state into the new dir and disposes the old one — so the invoking user must be
able to read and remove every host-side file the container wrote.
- The overlay `upper`/`ovlwork` layers are **root-owned kernel dirs**, and the
  kernel creates the overlayfs workdir (`ovlwork/work`) at mode **0000** — even
  its owner can't traverse it, defeating `os.RemoveAll`.
- **Rootful Docker** maps the container's users to the host user or root, so the
  rest of the sandbox state (`agent-status.json`, `logs/`, …) is host-manageable;
  only the overlay layers are the problem.
- **Podman-rootless** maps the container's users to host **subuids** (per
  `/etc/subuid`, e.g. `karl:100000:65536` → container uid 999 = host **100999**),
  so *all* container-written state (`agent-status.json`, `cache/`, `files/`,
  `logs/`) lands under a subuid at mode 0600 — unreadable and unremovable by the
  host process. This is a general podman-rootless trait (`teardown.go` already
  warns about it on destroy), not specific to migration.

**Fix (rootful Docker):** during capture, the migrator chowns **and** chmods
(`u+rwX`) the `upper`/`ovlwork` layers to the invoking user via the running
container (root) — chown alone is insufficient because of the 0000 workdir. The
capture itself runs as root and hands the staged copy to the host user, so it
works regardless of the backend's uid mapping. Verified end-to-end on real
rootful Docker (committed + uncommitted + gitignored preserved, `:copy`, v4).

**Limitation (podman-rootless):** normalizing subuid ownership host-side needs a
privileged helper (a throwaway root container, or `podman unshare chown`), which
isn't wired in — overlay-on-podman-rootless across a D109 upgrade is a rare edge.
So the migrator detects the foreign-uid state and surfaces it as a hard
**blocked** op: `migrate --check`/`--dry-run` lists it (`[✗] sandbox "X" can't be
migrated in place …`, `blocked:true` in `--json`), and a real `migrate`
**refuses the whole run** — crucially **before any mutation**: `refuseIfBlocked`
runs *ahead of* the frozen v0→v3 ladder (relocation/stamp), so a blocked sandbox
never triggers an irreversible schema bump the user would then have to downgrade
past to fix it. The sandbox is left **untouched** (mode `overlay`, realm at its
prior schema, no sentinels). The refusal prints **downgrade guidance naming real
release tags** for the data dir's current schema (`config.LibrarySchemaReleases`
/ `PriorReleaseRange` → e.g. "switch back to a yoloai release from v0.4.0 up to …
recover with `yoloai diff`/`apply`, destroy + recreate as `:copy`, upgrade
again"). The block is an `AuthBlocked` plan op (`Plan` calls
`hostUnmanageableReason`; no `Decision` satisfies it) plus the apply-time
`assertHostOwnedState` backstop. A quarantine (rename-only) is exempt.

**Code:** `internal/orchestrator/migrate_overlay.go` — `reclaimOverlayLayers`,
`captureMerged` (capture-as-root + stage chown), `assertHostOwnedState`;
`fileOwnerUID` in `migrate_overlay_unix.go`. Verified on real hardware
2026-07-01: docker migrate passes end-to-end; podman-rootless refuses cleanly
with the sandbox state unchanged.

### containerd: restart (Stop→Start) must re-establish the netns that Stop tore down

**Symptom:** restarting a containerd/Kata sandbox — `Stop` then `Start` on the
same container — fails at task creation with `create task: failed to create shim
task: ttrpc: closed`. Consistent, not a flake; retrying `NewTask` does **not**
help (the failure is a missing resource, not a transient one).

**Explanation:** `Create` calls `setupCNI`, which creates a **named** network
namespace at `/var/run/netns/yoloai-<name>` and pins it into the container's OCI
spec (`specs.NetworkNamespace{Path: netnsPath}`). `Stop`'s `teardownCNIForSandbox`
runs CNI DEL and then **deletes that netns**. But `Start` only re-creates the
task on the existing container — it did *not* re-create the netns. So on a restart
the Kata shim boots into a netns path that no longer exists, dies during init, and
containerd surfaces the dropped shim connection as `ttrpc: closed`. (Docker/Podman
don't hit this: the daemon owns and re-establishes the container's network on
start; only the containerd backend manages the named netns itself.)

**Fix:** `Start` re-runs `setupCNI` when the netns is absent (guarded on
`os.Stat(netnsPathFor(name))` so the normal create→start path, where the netns
still exists, is untouched). The path is deterministic and `setupCNI` is
idempotent, so re-creating it at the same path satisfies the pinned spec. This
also re-applies the CNI firewall/isolation rules to the restarted sandbox.

**Code:** `runtime/containerd/lifecycle.go` `Start` (netns-absent → `setupCNI`);
`netnsNameFor`/`netnsPathFor` in `runtime/containerd/cni.go` are the single source
of truth for the `yoloai-<name>` netns convention shared by setup and the restart
check. Regression: `TestIntegration_ContainerLifecycle` (the "Restart should
succeed" assertion) — was DF72.

## Base image (trixie): gold linker is a separate package; arm64 cgo link fails

**Symptom.** Building `yoloai-base` **on arm64** (e.g. an Apple-Silicon Mac) fails at
the `go install golangci-lint` step:

```
/usr/local/go/pkg/tool/linux_arm64/link: running gcc failed: exit status 1
/usr/bin/gcc ... -fuse-ld=gold ...
collect2: fatal error: cannot find 'ld'
```

amd64 builds are unaffected, so this only shows up on Apple-Silicon hosts.

**Explanation.** The Go toolchain links cgo binaries with `-fuse-ld=gold` on
`linux/arm64` — a long-standing workaround for old GNU BFD-linker bugs on ARM.
Debian **trixie** split the gold linker out of the `binutils` package into a
separate `binutils-gold` package (gold is upstream-deprecated), so a default
trixie image has no `ld.gold` and the cgo link fails. bookworm bundled gold, so
this never surfaced before the base bump. On amd64 Go uses bfd, so it doesn't
trigger there.

**Fix.** Install `binutils-gold` in the base image (it restores
`<arch>-linux-gnu-ld.gold`). Done in `runtime/docker/resources/Dockerfile`'s apt
list. If a future Debian drops gold entirely, switch Go to bfd/lld instead.

**Code:** `runtime/docker/resources/Dockerfile` (apt list, `binutils-gold`).

## Base image (trixie): `aider-chat` does not support Python 3.13; install it isolated on 3.12 via uv

**Symptom.** `pip install --break-system-packages --no-cache-dir aider-chat` on the
trixie base fails — pip resolves an ancient `aider-chat==0.16.0` and its pinned deps
fail to build (`Cannot import 'setuptools.build_meta'`).

**Explanation.** trixie ships Python **3.13** as the system `python3`. The current
`aider-chat` (0.86.2) caps `requires-python` at `<3.13` (it pins exact dependency
versions that don't yet support 3.13), so pip filters out every modern release and
backtracks to 0.16.0, whose decade-old deps can't build on 3.13. The other four
agents are npm/Node and are unaffected.

**Fix.** Install aider isolated on a uv-managed Python 3.12:
`uv tool install --python 3.12 aider-chat`. This yields the *current* aider, keeps it
unpinned, and is aider's own recommended install path. Tools/interpreters live under
`/opt/uv`; the `aider` launcher lands in `/usr/local/bin`. Revisit (back to a plain
system install) once aider supports 3.13.

**Code:** `runtime/docker/resources/Dockerfile` (uv install + `uv tool install aider-chat`).

---

## tmux `capture-pane` can emit invalid UTF-8 (a multibyte char sliced at the pane edge)

**Symptom.** The smoke harness crashes mid-test with `UnicodeDecodeError:
'utf-8' codec can't decode byte 0xe2 in position N: invalid continuation byte`,
flakily — the same test passes on a rerun. Seen after Claude Code's TUI grew
heavier box-drawing/Unicode (`─ ● ❯ ⎿ ✻`).

**Explanation.** `tmux capture-pane -p` renders the pane as a fixed-width
character grid and emits it cell by cell. A multibyte UTF-8 char (e.g. `─` =
`E2 94 80`) that lands at the right pane edge, or a wide char straddling the
wrap column, can be cut so only its lead byte (`0xe2`) survives followed by an
unrelated byte — an invalid UTF-8 sequence. yoloai embeds such a capture in its
`--debug`/`--bugreport` snapshot, so any consumer that strict-decodes that
output chokes. It is inherent to `capture-pane` + terminal wrapping, not our
code: delete yoloai and the truncated bytes still come out of tmux.

**Fix.** Decode tmux-derived output leniently (`errors="replace"`), never
strict. The Python smoke harness's command runner does this
(`scripts/smoke_test.py::Test.run`); Go holds bytes as-is so it doesn't crash,
but any Go path that does `utf8`-validating work on a capture must tolerate it.

**Code:** `scripts/smoke_test.py` (`Test.run`, `errors="replace"`);
regression test `scripts/tests/test_smoke_runner.py`.

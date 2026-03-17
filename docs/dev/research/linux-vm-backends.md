# Linux VM Backends for AI Agent Sandboxing

Research date: 2026-03-17

## Problem Statement

Standard Docker containers use Linux namespaces and cgroups for isolation, but the kernel surface remains shared. A container escape — via a kernel vulnerability, a namespace bypass, or access to a mounted Docker socket — gives the escaped process full host access. For AI coding agents that can write and execute arbitrary code, this threat is meaningful and growing.

This document researches VM-level backends that would provide hardware-enforced isolation between the agent sandbox and the host.

---

## 1. Firecracker (Amazon)

**Repository:** https://github.com/firecracker-microvm/firecracker
**Stars:** ~33,000 (verified 2026-03-17)
**Language:** Rust (79.9%), Python (17.4%)
**License:** Apache 2.0
**Maintainer:** Amazon Web Services
**Latest release:** v1.15.0 (March 9, 2026) — actively maintained

### What It Is

Firecracker is a Virtual Machine Monitor (VMM) that runs microVMs using Linux KVM. Designed specifically for serverless and container workloads, it minimizes the device model to reduce attack surface: it emulates only virtio-net, virtio-block, virtio-vsock, a serial console, and a minimal keyboard controller.

### Isolation Level

Hardware-level VM isolation via KVM. The guest runs its own Linux kernel with its own memory space. A kernel exploit inside the guest cannot escape the VM without also exploiting either the KVM hypervisor or the Firecracker VMM process. The attack surface is vastly smaller than a full QEMU (tens of thousands of lines vs. hundreds of thousands).

**Jailer:** A security wrapper that applies defense-in-depth before launching Firecracker:
- `pivot_root()` and mount namespace isolation (chroot jail)
- Drops privileges (switches to specified UID/GID)
- Applies cgroup resource limits
- Restricts file descriptors
- Sanitizes environment variables
- Optionally creates a new PID namespace
- Only creates `/dev/net/tun` and `/dev/kvm` device nodes inside the jail

Even if the VMM process is compromised, the jailer's restrictions limit what an attacker can reach.

### Performance

- **Boot time:** < 125 ms to user space (published by AWS, confirmed by Weaveworks Ignite, Fly.io)
- **Memory overhead per VM:** < 5 MiB
- **Creation rate:** Up to 150 microVMs/second per host

These numbers are what make per-request/per-sandbox VM spawning practical. Fly.io reports ~300 ms round-trip for a Firecracker VM start including network latency.

### API and Management

Firecracker exposes a **REST API over a Unix socket** (`/tmp/firecracker.socket`). All configuration is done by sending HTTP requests to the socket:

```bash
# Start firecracker
sudo ./firecracker --api-sock /tmp/firecracker.socket

# Configure kernel
curl -X PUT --unix-socket /tmp/firecracker.socket \
  --data '{"kernel_image_path": "/path/to/vmlinux", "boot_args": "..."}' \
  http://localhost/boot-source

# Configure root filesystem (ext4 image)
curl -X PUT --unix-socket /tmp/firecracker.socket \
  --data '{"drive_id": "rootfs", "path_on_host": "/path/to/rootfs.ext4", "is_root_device": true, "is_read_only": false}' \
  http://localhost/drives/rootfs

# Start the VM
curl -X PUT --unix-socket /tmp/firecracker.socket \
  --data '{"action_type": "InstanceStart"}' \
  http://localhost/actions
```

Alternatively, `--config-file <json>` starts a VM from a single JSON config without individual API calls.

**Go SDK:** https://github.com/firecracker-microvm/firecracker-go-sdk (629 stars, last release v1.0.0 September 2022 — appears stale; wraps the REST API).

### Exec Inside the VM

Firecracker has **no built-in exec mechanism**. Standard approaches:

1. **SSH** — requires SSH daemon inside rootfs and key injection at build time. Used in getting-started guide.
2. **virtio-vsock** — a fast host↔guest channel over a Unix socket. The kata-agent and custom daemons (like E2B's `envd`) use vsock for exec RPC. This is the production-grade approach.
3. **Serial console** — functional but not scriptable for programmatic use.

E2B's open-source infrastructure (`github.com/e2b-dev/infra`, Go, 83.8%) uses Firecracker with a custom `envd` daemon inside each VM, communicating via Connect RPC over vsock to implement process management and filesystem APIs. This is a proven pattern for AI agent sandboxing.

### Filesystem / File Sharing

No native bind-mount equivalent. Options:
- **virtio-block device** — attach an ext4 disk image (read-write or read-only). Supports attach at boot or hot-plug.
- **virtio-9p** — 9P protocol filesystem sharing (supported but less common with Firecracker).
- **NBD (Network Block Device)** — used by E2B for persistent storage across VMs.
- The rootfs is a full ext4 image. You build a rootfs image containing your environment, then boot from it. OCI images can be converted to ext4 rootfs (firecracker-containerd does this).

**No VirtioFS support** in Firecracker itself — VirtioFS requires a separate virtiofsd daemon, which Firecracker doesn't ship.

### Snapshots

Full snapshot/restore support:
- **Pause** the VM, create snapshot files (memory + device state), **restore** later
- Memory file is mapped MAP_PRIVATE — extremely fast restore (pages loaded on demand)
- Supports Full and Diff snapshots
- **Critical:** Resuming the same snapshot multiple times creates security issues (shared RNG state, entropy pool). VMGenID mitigates this in guests that support it.
- Enables "pre-warmed" VM pools: boot many VMs to a ready state, snapshot them, restore snapshots for each new sandbox request. This is how E2B achieves fast sandbox creation despite Firecracker's already-fast cold boot.

### KVM Requirement

**Yes, requires `/dev/kvm`.**

- Linux x86_64: KVM available on bare metal and most cloud VMs
- Linux aarch64/ARM64: KVM available on bare metal (AWS Graviton); nested virtualization on ARM cloud VMs varies
- **Cloud VM support:**
  - AWS: `.metal` instances provide `/dev/kvm`. Most standard Nitro-based instances **do not** expose `/dev/kvm` to guests.
  - GCE: Nested virtualization supported on Intel x86 instances (not E2, not AMD, not ARM). ~10% performance penalty for I/O-bound workloads.
  - Azure: Nested virtualization on Dv3/Ev3 and newer series
  - Hetzner Cloud: Most instances expose `/dev/kvm` (popular for Firecracker deployments)
- **Apple Silicon / macOS:** Not supported. Firecracker is Linux-KVM-only. No macOS port exists or is planned. Documentation confirms support is Linux x86_64 and aarch64 only.
- **Guest/host architecture:** Guest OS must match host CPU architecture. Cannot run x86_64 guests on ARM hosts.

### Real-World Users

- **AWS Lambda & Fargate** (original use case, millions of VMs daily)
- **Fly.io** — entire platform built on Firecracker (custom init: `github.com/superfly/init-snapshot`)
- **E2B** — AI sandbox infrastructure for coding agents
- **Kata Containers** — uses Firecracker as a hypervisor backend via shim
- **Northflank, Koyeb** — container-as-a-service platforms

### Integration Complexity for yoloAI

**High.** Firecracker requires:
1. A pre-built rootfs ext4 image for each sandbox environment
2. A pre-built Linux kernel (vmlinux) compatible with your guest requirements
3. Network configuration (TAP interfaces, routing, CNI or manual)
4. An exec mechanism (vsock daemon or SSH)
5. File sharing mechanism (disk image mount, 9P, or NBD)
6. Root privileges (or jailer setup)

Not a drop-in replacement for Docker. Requires building a mini-orchestration layer. E2B's Go infra is open-source and could serve as reference.

**Linux-only** — macOS users would remain on the Docker/Tart backends.

---

## 2. gVisor (Google)

**Repository:** https://github.com/google/gvisor
**Stars:** ~17,900 (verified 2026-03-17)
**Language:** Go
**License:** Apache 2.0
**Maintainer:** Google
**Latest commit:** Active (master branch), daily commits

### What It Is

gVisor is a **userspace kernel** — the "Sentry" — written in Go that intercepts system calls from container workloads and handles them in userspace, acting as a proxy kernel that implements the Linux system call interface. The container's processes never directly reach the host kernel for most operations.

This is a "third way" between containers (shared kernel) and VMs (separate kernel in hardware). The Sentry is software isolation, not hardware isolation.

### Isolation Level

**Strong, but not equivalent to hardware VMs.** The attack surface reduction is significant:
- Container processes call the Sentry (Go, memory-safe) rather than the host kernel
- A kernel exploit in the container sees the Sentry's Go implementation, not the raw host kernel
- But: the Sentry itself still makes host kernel calls, so a Sentry bug could expose the host kernel
- **Does not fully prevent all container escapes** — if the Sentry has a vulnerability, or if an attack targets the platform layer (KVM backend), escalation remains possible

Google uses gVisor for Cloud Run Gen1 and GKE Sandbox. They consider it sufficient for multi-tenant workloads but acknowledge it is not equivalent to hardware VM isolation.

### Platforms (Sentry backends)

**systrap** (default since mid-2023):
- Uses `seccomp SECCOMP_RET_TRAP` → `SIGSYS` signal to intercept syscalls
- Works without KVM
- Works inside VMs (good for cloud deployments)
- Replaces the deprecated ptrace backend

**KVM platform:**
- Uses hardware virtualization extensions for isolation at the syscall boundary
- Better performance on bare metal
- Does **not** work well inside VMs (nested virtualization overhead)
- Requires `/dev/kvm`

### Docker Integration

Drop-in Docker integration via `runsc` OCI runtime:
```bash
sudo runsc install  # installs as Docker runtime named "runsc"
sudo systemctl restart docker
docker run --runtime=runsc --rm hello-world
```

This is the primary advantage over Firecracker: **zero changes to your existing Docker workflow** except adding `--runtime=runsc`.

### Performance Overhead

- **CPU-bound workloads:** No overhead — native code executes at full speed
- **Syscall-heavy workloads:** Significant overhead
  - Redis small ops: large relative overhead (syscall per operation)
  - Apache serving 100KB files: poor (VFS serialization + syscall overhead)
  - ffmpeg transcoding: minimal impact (CPU-bound, few syscalls)
  - ML/AI model inference: near-native (CPU-bound)
- **Network:** Additional overhead from gVisor's userspace network stack

For AI coding agent use (running build tools, compilers, tests): moderate overhead. The compiler invocations and I/O operations are syscall-intensive. Expect 1.5x-3x slower for build-heavy workloads.

### Compatibility Gaps

Known incompatibilities and limitations:
- **`io_uring`:** Disabled by default, limited when enabled — affects high-performance async I/O
- **`nftables`:** Limited support
- **KVM from within sandbox:** Not supported — cannot run VMs inside gVisor
- **Block device filesystems:** Cannot mount ext3/ext4/fat32 from within sandbox
- **Custom hardware devices:** Generally unsupported (exceptions: NVIDIA GPU, TPU)
- **`iptables`:** Partially supported
- **Resource limits:** Not enforced within the sandbox

**For AI coding agents:** The critical incompatibility is that agents cannot run Docker or KVM from within a gVisor sandbox. Claude Code and similar agents that try to spin up child containers (Docker-in-Docker patterns) will fail. Build tools, compilers, and test runners generally work.

### Availability

- **Linux x86_64:** Full support
- **Linux ARM64:** Builds on ARM64 (documented)
- **macOS:** Not supported as a runtime — some test tooling supports macOS, but `runsc` is Linux-only
- **Cloud VMs:** systrap platform works everywhere Linux runs. KVM platform requires `/dev/kvm` (same as Firecracker)

### Real-World Users

- **Google Cloud Run (Gen1)** — gVisor used for container sandboxing
- **Google Kubernetes Engine (GKE Sandbox)** — opt-in node pool security mode
- Cloud Run Gen2 dropped gVisor in favor of full Linux compatibility (users were hitting incompatibilities)

### Integration Complexity for yoloAI

**Low.** The key advantage: gVisor is a drop-in Docker runtime. yoloAI already uses Docker. Adding gVisor support means:
1. Install gVisor (`runsc install`)
2. Add `--runtime=runsc` to `docker run` arguments
3. Optionally expose a `security_mode: gvisor` config option

No rootfs images, no kernel builds, no vsock daemons, no TAP interfaces. Existing Docker images work unchanged (within gVisor's compatibility limits).

The Sentry adds a fixed memory overhead (~100-200 MB per container based on community reports) and syscall latency, but setup complexity is minimal.

---

## 3. Cloud Hypervisor (Intel/Microsoft)

**Repository:** https://github.com/cloud-hypervisor/cloud-hypervisor
**Stars:** ~5,400 (verified 2026-03-17)
**Language:** Rust
**License:** Apache 2.0 (REUSE compliant, multiple licenses)
**Maintainer:** Community (originated from Intel and Microsoft contributions)
**Activity:** Active (9,519 commits, recent releases)

### What It Is

Cloud Hypervisor is a VMM targeting "modern Cloud workloads" — broader scope than Firecracker's narrow serverless focus. It shares lineage with Firecracker and crosvm (both Rust VMMs) but aims to be a general-purpose hypervisor.

Runs on KVM (Linux) and MSHV (Microsoft Hypervisor on Azure).

### Comparison with Firecracker

| | Firecracker | Cloud Hypervisor |
|---|---|---|
| Scope | Serverless/FaaS microVMs | General cloud workloads |
| Device model | Extremely minimal (5 devices) | Minimal but broader (hotplug support) |
| CPU hotplug | No | Yes |
| Memory hotplug | Limited (FC 1.x) | Yes |
| VM-to-VM migration | No | Yes |
| Windows guest | No | Yes |
| PCI | Optional (v1.13+) | Yes |
| Language | Rust | Rust |
| Boot time | <125ms | Comparable (not officially benchmarked) |
| Primary users | AWS Lambda, Fly.io | Kata Containers |

Cloud Hypervisor is used as a hypervisor backend in Kata Containers (alongside QEMU and Firecracker). It offers more features than Firecracker but with a slightly larger footprint.

### Exec / File Sharing

No native exec mechanism. Like Firecracker, relies on SSH, vsock, or an in-VM agent for command execution. Kata Containers' `kata-agent` handles this when Cloud Hypervisor is used as the Kata backend.

### Integration Complexity for yoloAI

**High** — same class of complexity as Firecracker. Cloud Hypervisor is not the right direct backend for yoloAI. It's primarily useful as a Kata Containers hypervisor, where Kata handles the exec/file-sharing complexity.

---

## 4. Kata Containers

**Repository:** https://github.com/kata-containers/kata-containers
**Stars:** ~7,600 (verified 2026-03-17)
**Language:** Rust (58.3%), Go (23.7%), Shell (10.2%)
**License:** Apache 2.0
**Latest release:** v3.28.0 (March 17, 2026) — actively maintained

### What It Is

Kata Containers is an **OCI-compatible VM sandbox runtime**. Each container runs in its own lightweight VM, but from Docker's or Kubernetes' perspective, Kata behaves like runc — the same `docker run` commands work unchanged.

The architecture layers:
```
docker run / kubectl apply
    ↓
containerd (CRI)
    ↓
Kata shimv2 (host side)
    ↓
VMM (QEMU, Firecracker, or Cloud Hypervisor)
    ↓
kata-agent (inside VM, ttrpc over vsock)
    ↓
Container workload
```

### How It Integrates with Docker

Configuration via containerd runtime class. For Docker:
```bash
# /etc/docker/daemon.json
{
  "runtimes": {
    "kata-qemu": {
      "path": "/usr/bin/kata-runtime",
      "runtimeArgs": ["--config", "/etc/kata-containers/configuration-qemu.toml"]
    }
  }
}
docker run --runtime=kata-qemu ubuntu bash
```

The kata-agent inside the VM handles all exec/file-sharing operations transparently.

### Exec Mechanism (shimv2 + kata-agent)

**shimv2:** A single containerd shim process manages any number of containers within a single VM. Reduces per-pod overhead from 2N+1 shim processes to 1.

**kata-agent:** A long-running supervisor inside the VM, listening on a vsock. Uses a ttrpc-based protocol (gRPC-like over vsock). Handles:
- `CreateContainer` / `StartContainer` / `ExecProcess`
- TTY management
- File descriptor passing
- Metrics

`docker exec` commands are transparently forwarded through the shimv2→vsock→kata-agent chain. From the user's perspective, `docker exec container /bin/bash` works identically to runc.

### Hypervisors Supported

| Hypervisor | Feature set | Container creation speed | Memory density | Primary use |
|---|---|---|---|---|
| QEMU | Lots of features | Good | Good | Most users, best compatibility |
| Cloud Hypervisor | Minimal, modern | Excellent | Excellent | High performance |
| Firecracker | Extremely minimal | Excellent | Excellent | Serverless/FaaS |
| Dragonball | Built-in (Rust) | Good | Good | Kata's default Rust runtime |

**Firecracker limitations in Kata:** Firecracker's minimal device model means some Kata features don't work with the FC backend — fewer device types, no hotplug, no CPU/memory resize. For most agent workloads this doesn't matter.

### File Sharing

Kata uses VirtioFS (via virtiofsd) for sharing host directories into the VM. This means bind mounts in `docker run -v /host:/container` work transparently — virtiofsd serves the host directory into the VM's filesystem namespace.

Performance of VirtioFS is good for warm/cached reads (near-native), with some penalty for stat-heavy cold operations (~3x slower than local disk).

### Performance Overhead

No quantitative numbers in official docs, but community measurements suggest:
- Container start time: 1-2 seconds (vs ~0.3s for runc) — VM boot time amortized through shimv2 batching
- Memory: ~100-150MB overhead for the VM + kata-agent (baseline for each sandbox)
- CPU: Near-native for workloads inside the VM
- I/O: VirtioFS adds some overhead for bind-mounted directories

### Nested Virtualization

Kata requires hardware virtualization (KVM or similar). Running Kata inside a standard cloud VM requires nested virtualization — same constraints as Firecracker:
- AWS: `.metal` or nested-virt-capable instances
- GCE: Intel x86 instances only
- Hetzner, bare metal providers: generally available

### Availability

- Linux x86_64: Full support
- Linux aarch64: Full support (all architectures listed: x86_64, aarch64, ppc64le, s390x)
- macOS: Not supported (requires KVM on Linux)

### Integration Complexity for yoloAI

**Medium.** The big advantage over raw Firecracker: Kata provides the exec/file-sharing/OCI-compat layer. Integration steps:
1. Install Kata Containers on the host
2. Configure containerd/Docker to use Kata runtime
3. Add `--runtime=kata-qemu` (or `kata-fc`) to `docker run` calls

This is substantially simpler than raw Firecracker. Existing Docker images and `docker exec` work as-is. The main yoloAI change is adding `--runtime=kata` to the container launch arguments.

**Drawback:** Requires Kata installed on the host — can't be bundled in the yoloAI binary. Users must install Kata separately. Also Linux-only.

---

## 5. Lima

**Repository:** https://github.com/lima-vm/lima
**Stars:** ~20,500 (verified 2026-03-17)
**Language:** Go (74.6%)
**License:** Apache 2.0
**Maintainer:** CNCF Incubating Project
**Latest release:** Active (commit December 2024)

### What It Is

Lima ("Linux Machines") launches Linux VMs on macOS (and Linux) with automatic file sharing and port forwarding, similar to WSL2. Originally designed to promote containerd/nerdctl on macOS.

### VM Types Supported

| vmType | Platform | Notes |
|---|---|---|
| `qemu` | macOS (Intel/ARM), Linux | Default before Lima v1.0 |
| `vz` | macOS 13+ | Default since Lima v1.0; uses Apple Virtualization.framework |
| `wsl2` | Windows | WSL2 backend |
| `krunkit` | macOS, Linux | libkrun-based, GPU-accelerated workloads |

### File Sharing / Mount Types

| Mount type | Backend | Performance | Notes |
|---|---|---|---|
| `virtiofs` | VirtioFS daemon | Best | Default for VZ vmType on macOS 13+. ~70-90% native read |
| `9p` | QEMU virtio-9p-pci | Medium | QEMU default (Lima v1.0). Incompatible with CentOS/Rocky/Alma |
| `reverse-sshfs` | SFTP over SSH | Slow | QEMU default before Lima v1.0 |
| `wsl2` | WSL2 native | Medium | Windows only |

On Apple Silicon with VZ + virtiofs: near-native I/O performance for most workloads.

### Exec Interface

`limactl shell <instance> [command]` — runs commands inside the Lima VM via SSH. For programmatic use:
```bash
limactl shell myinstance -- uname -a
lima -- docker ps   # if Docker is installed in the Lima VM
```

Alternatively: `lima` (shorthand for `limactl shell default --`).

### Container Runtime Support

Lima can run Docker, Podman, containerd/nerdctl, or Kubernetes inside the VM. It is essentially the engine behind **Colima** (27,600 stars) — "Containers on Lima" — which provides `colima start` and `colima docker` as a simpler interface.

### Could Lima Replace Docker Desktop as a yoloAI macOS Backend?

**Partially yes, but indirectly.** Lima itself manages Linux VMs and runs Docker (or containerd) inside them. A yoloAI "Lima backend" would actually be "start a Lima VM, run Docker inside it, launch Docker containers in that VM." This adds a layer of indirection.

**More relevant:** Lima/VZ could power a lightweight Linux sandbox backend on macOS that is faster than Docker Desktop for cold starts. Apple's own Containerization framework (macOS 26, `github.com/apple/containerization`) takes this approach — each container gets its own Virtualization.framework VM, achieving 0.92s cold start with better CPU/memory throughput than Docker Desktop.

Lima is better suited as a **developer convenience tool** than as a yoloAI runtime backend. It doesn't expose the VM lifecycle APIs yoloAI needs programmatically.

### Availability

- macOS Apple Silicon: Best support (VZ vmType)
- macOS Intel: Supported (QEMU vmType)
- Linux: Supported (QEMU)
- Windows: WSL2 vmType

---

## 6. Podman with VM-backed Runtimes

**Podman** (`--runtime=runsc` or `--runtime=kata`) — does switching from Docker to Podman offer security advantages?

Short answer: **No, not inherently.** Podman and Docker both use the OCI runtime interface. Security comes from the runtime (runc, runsc, kata), not the container manager (Docker vs Podman).

- `podman run --runtime=runsc` → gVisor sandbox (same as `docker run --runtime=runsc`)
- `podman run --runtime=kata` → Kata VM sandbox (same as `docker run --runtime=kata`)

Podman's main security advantage over Docker is **rootless by default** — no root daemon, no Docker socket to compromise. This matters for privilege escalation paths but doesn't affect guest-to-host isolation inside the sandbox.

**Rootless Podman** is worth noting: since there is no privileged daemon, an attacker who escapes a container has only user-level access to the host, not root. This is a meaningful improvement over Docker's root daemon model. However, rootless Podman still uses runc by default — you'd still want `--runtime=runsc` or `--runtime=kata` for VM-level isolation.

**For yoloAI:** Podman is worth supporting as a Docker replacement for users who prefer rootless operation. The VM runtime integrations (gVisor, Kata) work identically.

---

## 7. QEMU/KVM Direct

Running VMs via QEMU directly (without Firecracker or Kata) is how traditional VMs work.

### Comparison with Firecracker

| | QEMU/KVM | Firecracker |
|---|---|---|
| Boot time | ~2-15 seconds (full BIOS/firmware) | <125 ms |
| Memory overhead | ~100-300 MB | <5 MB |
| Device model | Hundreds of emulated devices | 5 devices |
| Attack surface | Very large | Minimal |
| Exec mechanism | SSH, QEMU QMP, virtio-serial | SSH, vsock |
| File sharing | virtio-9p, virtiofs (with virtiofsd) | virtio-block, 9p |

QEMU's flexibility is its strength for general virtualization but makes it impractical for per-sandbox VM spinning at scale. The 2-15 second boot time and 100-300 MB overhead per sandbox would be prohibitive for yoloAI's use case of spinning up a VM per coding session.

**Direct QEMU/KVM is not appropriate for yoloAI** unless you want to use QEMU as a Kata hypervisor (where Kata optimizes the launch path and manages the lifecycle).

---

## 8. Apple Virtualization.framework (Go Bindings)

### code-hex/vz

**Repository:** https://github.com/code-hex/vz
**Stars:** 792 (verified 2026-03-17)
**Language:** Go (64%), Objective-C (35.7%)
**License:** MIT
**Latest release:** v3.7.1 (August 27, 2025)
**Used by:** Lima, vfkit, LinuxKit

### What It Provides

Go bindings for Apple's `Virtualization.framework` — the same framework used by Lima (VZ vmType), Docker Desktop, OrbStack, and Apple's own Containerization framework.

Capabilities:
- Virtualize Linux on macOS (x86_64 and arm64)
- Virtualize macOS on Apple Silicon
- Rosetta 2 for running Intel binaries in Linux VMs on Apple Silicon
- Shared directories (VirtioFS)
- Virtio sockets (vsock)
- EFI boot
- GUI windows

Requirements: macOS 11.0+ (Big Sur). Supports last two major Go releases.

### Could This Power a macOS Linux Backend for yoloAI?

**Yes, in theory, but this is what Lima already does.** Instead of shelling out to `lima` or `docker`, you could use `code-hex/vz` to manage Linux VMs directly from Go:

1. Boot a Linux VM using vz
2. Mount the project directory via VirtioFS
3. Communicate via vsock for exec
4. Clean up VM when done

**Comparison with Tart (already researched in sandboxing.md):**
- Tart uses `code-hex/vz` under the hood and adds CLI management, OCI image support, `tart exec`, APFS cloning
- Using `code-hex/vz` directly means building what Tart already provides
- For macOS Linux sandboxes, Tart is still the better choice (documented in sandboxing.md)

**Apple Containerization framework** (`github.com/apple/containerization`, macOS 26) is the first-party answer to this question — it uses Virtualization.framework to run each OCI container in its own lightweight VM. 0.92s cold start, better CPU/memory throughput than Docker Desktop. Requires macOS 26 Tahoe (not widely deployed until late 2026+).

---

## 9. Additional: Sysbox

**Repository:** https://github.com/nestybox/sysbox
**Stars:** ~3,500
**Maintainer:** Nestybox (acquired by Docker in 2022)
**License:** Apache 2.0 (community-maintained)

Sysbox is an alternative to runc that provides stronger isolation than standard Docker containers **without requiring hardware virtualization**. It uses Linux user namespaces aggressively — mapping container root to an unprivileged host user — plus procfs/sysfs virtualization and host information hiding.

Key feature: Sysbox containers can run Docker-in-Docker, Kubernetes, and systemd without `--privileged`. This is achieved through the OS-virtualization layer, not VMs.

Usage:
```bash
docker run --runtime=sysbox-runc ubuntu bash
```

**Isolation level:** Stronger than standard runc (user namespace mapping, procfs virtualization), weaker than hardware VMs (still a single kernel). A kernel vulnerability could still escape, but the attack surface is reduced because the container root has no host privileges.

**For AI agent sandboxing:** Sysbox provides a meaningful improvement over default Docker without the overhead or complexity of Kata/Firecracker. Particularly useful if agents need to run Docker commands inside the sandbox (Docker-in-Docker without `--privileged`).

**Limitation:** Not included in Docker's standard distribution; must be installed separately on the host.

---

## Comparison Matrix

| Technology | Isolation Level | Boot Time | Memory Overhead | Exec Interface | File Sharing | Linux x86 | Linux ARM | macOS | KVM Required | Integration Complexity |
|---|---|---|---|---|---|---|---|---|---|---|
| **Standard Docker (runc)** | Namespace (kernel shared) | ~0.3s | ~5-10 MB | `docker exec` | Bind mounts | Yes | Yes | Via Docker Desktop | No | Baseline |
| **gVisor (runsc)** | Userspace kernel | ~0.5s | ~100-200 MB | `docker exec` | Bind mounts | Yes | Yes | No | No (systrap default) | Low |
| **Sysbox (sysbox-runc)** | OS-virtualized namespace | ~0.3s | ~20-50 MB | `docker exec` | Bind mounts | Yes | Yes | No | No | Low-Medium |
| **Kata + QEMU** | Hardware VM | ~1-2s | ~100-150 MB | `docker exec` (via shimv2) | VirtioFS | Yes | Yes | No | Yes | Medium |
| **Kata + Firecracker** | Hardware VM (minimal surface) | ~0.5s | ~100 MB | `docker exec` (via shimv2) | VirtioFS | Yes | Yes | No | Yes | Medium |
| **Firecracker (raw)** | Hardware VM (minimal surface) | <125ms | <5 MB (+rootfs) | vsock/SSH | virtio-block/9p | Yes | Yes | No | Yes | High |
| **Cloud Hypervisor (raw)** | Hardware VM | <200ms est. | Similar to FC | vsock/SSH | virtiofs | Yes | Yes | No | Yes | High |
| **QEMU/KVM (direct)** | Hardware VM | 2-15s | 100-300 MB | SSH/QMP | 9p/virtiofs | Yes | Yes | No | Yes | High |
| **Lima/VZ (macOS)** | Hardware VM per instance | ~3-5s | ~200 MB | `limactl shell` | VirtioFS | Via QEMU | Via QEMU | Yes (Apple Silicon) | No (uses Hypervisor.framework) | Medium |
| **Apple Containerization** | Hardware VM per container | ~0.92s | Low | docker-compatible | EXT4 block | No | No | macOS 26+ only | No | Low-Medium |

**Isolation level ranking (strongest to weakest for container escape prevention):**
1. Firecracker/QEMU/Kata (hardware VMs) — kernel vulnerability in guest cannot reach host
2. gVisor — host kernel not directly reachable; requires Sentry vulnerability first
3. Sysbox — kernel shared but no host root privileges
4. Standard Docker — full kernel shared, container root = host root in most deployments

---

## Recommendation for yoloAI

### What to Build First: gVisor Integration

**gVisor is the right first step for VM-level isolation.**

Rationale:
- Drop-in Docker runtime: `--runtime=runsc` — minimal changes to yoloAI
- No KVM required — works on any Linux host including cloud VMs without nested virt
- Existing Docker images work unchanged (within compatibility limits)
- Well-maintained by Google, deployed at Cloud Run scale
- Meaningful isolation improvement: host kernel not directly exposed to agent code

Implementation:
1. Add a `security` config key: `security: gvisor` (default: `standard`)
2. When `gvisor`, add `--runtime=runsc` to the `docker run` invocation
3. Document that gVisor must be installed on the host (`apt install runsc` or `runsc install`)
4. Add a preflight check: if `security: gvisor` but runsc is not found, fail with a clear error

**Known limitation for yoloAI:** AI coding agents that try to run Docker-in-Docker from within their sandbox will fail under gVisor (KVM is blocked inside the sandbox). Claude Code and Gemini CLI don't do this by default. It is an acceptable tradeoff.

### What to Build Second: Kata Containers Integration

**Kata provides hardware-level isolation with the same Docker exec interface.**

Kata is stronger than gVisor (hardware VM boundary), and `docker exec` still works transparently. The integration path is similar to gVisor: add `--runtime=kata-qemu` to the docker run.

Why defer this:
- Requires KVM on the host (excludes most cloud VMs without nested virt or .metal instances)
- Adds ~1-2 second overhead to container startup
- Larger memory footprint per sandbox (~100-150 MB vs gVisor's ~100-200 MB — comparable)
- Kata must be installed on the host; harder to distribute than gVisor

Implement as `security: kata` config option. For Kata with Firecracker backend: `security: kata-firecracker` (faster start, but Firecracker's device model restrictions).

### What to Defer: Raw Firecracker Backend

**Firecracker as a native yoloAI runtime backend is too complex for now.**

The benefits (< 125ms boot, <5 MB overhead, VM isolation) are real but only achievable if yoloAI manages the full orchestration stack:
- Build and maintain rootfs images (one per supported agent environment)
- Build/distribute compatible Linux kernels
- Implement vsock exec daemon
- Implement file sharing (NBD or 9P)
- Handle networking (TAP devices, routing)
- Implement snapshot pool for fast sandbox creation

This is essentially building what E2B built. It's a significant project (E2B's infra repo is 83% Go, 9% Terraform/HCL). Unless yoloAI's roadmap includes becoming a hosted sandbox platform, this complexity is not justified when Kata + Firecracker backend achieves comparable isolation through the existing Docker interface.

**Revisit when:** yoloAI needs per-invocation (not per-session) sandboxes, where boot time is critical, or when targeting a hosted deployment model.

### What's Not Worth It

**Direct QEMU/KVM management:** Boot times and overhead make per-sandbox VMs impractical. Use Kata instead, which abstracts QEMU behind a compatible Docker interface.

**Cloud Hypervisor as a direct backend:** Use it only as a Kata hypervisor (already supported in Kata's configuration). Not worth managing directly.

**Lima as a backend:** Lima manages per-user VMs, not per-sandbox VMs. It's a developer tool, not an orchestration runtime. It doesn't expose the lifecycle APIs yoloAI needs.

**code-hex/vz directly:** Reimplements what Tart already provides for macOS. For macOS Linux sandboxes, use Tart (see sandboxing.md). Apple Containerization framework is the better long-term answer when macOS 26 is widely deployed.

**Sysbox:** A reasonable intermediate option (stronger than runc, simpler than Kata). But gVisor is better supported and more widely deployed. Consider Sysbox only if users specifically need Docker-in-Docker capability inside sandboxes.

### Priority Ordering

1. **gVisor integration** — low complexity, meaningful isolation, no KVM required, good for most cloud VMs. Ship this.
2. **Kata integration** — hardware VM isolation, docker-exec compatible, requires KVM. Ship after gVisor is validated.
3. **Apple Containerization support (macOS 26)** — when macOS 26 reaches ~50% adoption, evaluate as Docker Desktop replacement. Per-container VM isolation, better throughput.
4. **Raw Firecracker backend** — only if yoloAI pivots to a hosted/SaaS model needing maximum density with VM isolation.

### Configuration Design

```yaml
# profile config
security: standard   # standard | gvisor | kata | kata-firecracker
```

The existing Docker backend remains unchanged for users who don't opt in. Each security level is a runtime constraint added to `docker run`:
- `standard` → no change (runc)
- `gvisor` → `--runtime=runsc`
- `kata` → `--runtime=kata-qemu`
- `kata-firecracker` → `--runtime=kata-fc`

Preflight checks should detect whether the requested runtime is installed and emit a clear error if not.

---

## Related Projects Tracking the Same Problem

- **E2B** (11,300 stars) — uses Firecracker for AI agent sandboxing. Production-grade, self-hostable. Their open-source infra is good reference architecture.
- **Daytona** (66,100 stars) — AI code execution sandboxes using OCI/Docker containers. Claims <90ms creation. Likely using optimized Docker, not VMs.
- **Flintlock** (1,300 stars, community-maintained) — microVM lifecycle management via containerd, supports Firecracker and Cloud Hypervisor.
- **Apple Containerization** — per-container VMs on macOS 26.
- **Sysbox** (3,500 stars) — Docker-acquired, stronger-than-runc OS virtualization.

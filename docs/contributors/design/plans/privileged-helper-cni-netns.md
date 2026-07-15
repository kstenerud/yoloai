> **ABOUTME:** Extract CNI/netns setup into a small privileged helper binary so the containerd
> backend stops requiring the whole `yoloai` binary to run as root.

# Privileged helper for CNI/netns setup

- **Status:** UNSPECIFIED — idea only; not started.
- **Depends on:** —

The containerd backend currently requires running the entire `yoloai` binary as root because CNI network namespace creation (`netns.NewNamed`, bridge plugin, IPAM) requires `CAP_SYS_ADMIN` + `CAP_NET_ADMIN`. This is terrible UX — users shouldn't need `sudo` for the main binary.

Fix: extract CNI/netns operations into a small privileged helper binary (`yoloai-netsetup` or similar). The main binary calls it via exec, passing namespace name and config path. The helper is either setuid root or granted file capabilities (`setcap cap_net_admin,cap_sys_admin+ep`). This follows the same pattern Podman uses for `newuidmap`/`newgidmap`.

The helper should handle:
- `setup <nsname> <containerName> <cniConfDir>` — create netns, run CNI ADD, return JSON state
- `teardown <nsname> <containerName> <cniConfDir>` — run CNI DEL, delete netns

The main binary retains ownership of sandbox directories (written as the calling user), so `yoloai destroy` and git ops work without permission errors.

See [linux-vm-backends research](../research/linux-vm-backends.md) for full analysis.

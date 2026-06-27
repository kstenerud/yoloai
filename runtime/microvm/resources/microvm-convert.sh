#!/bin/bash
# ABOUTME: Builds the microvm golden ext4 rootfs and extracts the kernel + initrd
# ABOUTME: into /out. Runs as root inside the yoloai-base-microvm image so mkfs
# ABOUTME: writes correct root-owned inodes without any host privilege or userns.
#
# /out is a host bind mount (the backend's <DataDir>/microvm/ dir). The golden
# ext4 is the read-only base every sandbox boots via a per-instance qcow2 overlay.
set -euo pipefail

# Stage under /out (the host bind mount) rather than /tmp. /out is a separate
# mount, so `rsync --one-file-system` from / skips it entirely — which both
# prevents the staging dir from being copied into itself (infinite recursion
# when staging lives on the root fs) and gives the staging tree host-disk space.
ROOT=/out/.microvm-staging
rm -rf "$ROOT"
mkdir -p "$ROOT"

# Copy the image's own root, staying on the single root filesystem. --one-file-system
# stops at mount boundaries, which skips /proc, /sys, /dev, and the /out bind mount
# (and therefore $ROOT, which lives under /out).
rsync -aHAX --one-file-system / "$ROOT/"

# Re-create the runtime mountpoints the rsync skipped, plus the workdir mount target.
mkdir -p "$ROOT"/proc "$ROOT"/sys "$ROOT"/dev "$ROOT"/run "$ROOT"/tmp "$ROOT"/mnt/workdir

# Size the golden to the staged tree plus headroom (ext4 overhead + a little
# room for the first boot before the per-instance overlay takes writes). The
# file is sparse, so a generous size costs only the actual data on disk. An
# explicit MICROVM_DISK_SIZE override wins when set.
if [ -n "${MICROVM_DISK_SIZE:-}" ]; then
    truncate -s "$MICROVM_DISK_SIZE" /out/rootfs.ext4
else
    bytes=$(du -sB1 "$ROOT" | cut -f1)
    truncate -s "$(( bytes + bytes / 4 + 536870912 ))" /out/rootfs.ext4
fi
mkfs.ext4 -F -L microvm-root -d "$ROOT" /out/rootfs.ext4 >/dev/null
rm -rf "$ROOT"

# Newest kernel + matching initrd (a fresh image has exactly one).
cp "$(ls -t /boot/vmlinuz-* | head -1)" /out/vmlinuz
cp "$(ls -t /boot/initrd.img-* | head -1)" /out/initrd.img

# World-readable so the (unprivileged) host user can read the golden + kernel.
# Per-instance writes never touch these — they go to the qcow2 overlay.
chmod 0644 /out/rootfs.ext4 /out/vmlinuz /out/initrd.img
echo "microvm-convert: golden rootfs + kernel + initrd written to /out"

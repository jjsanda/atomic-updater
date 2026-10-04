# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# Minimal read-only system image for atomic-updater devices
# (design.md §7.1 read-only rootfs, §7.4 health, §7.5 agent).

SUMMARY = "Minimal read-only A/B system image for atomic-updater devices"
DESCRIPTION = "systemd + RAUC + atomic-agent + atomic-health on a read-only \
rootfs; writable state lives on the 'data' partition (rofs-overlays)."
LICENSE = "Apache-2.0"

inherit core-image extrausers

IMAGE_LINGUAS = ""

# design.md §7.1: rootfs is mounted read-only; /etc, /var, /home writable
# state is provided by rofs-overlays from /data.
IMAGE_FEATURES += "read-only-rootfs"

# NOTE: do NOT add meta-rauc's rauc-mark-good here — slot confirmation is
# atomic-health's job (design.md §7.4), not a timer-based mark-good.
IMAGE_INSTALL = " \
    packagegroup-core-boot \
    rauc \
    ca-certificates \
    tzdata \
    chrony \
    e2fsprogs-e2fsck \
    atomic-agent \
    atomic-health \
    rofs-overlays \
"

# --- machine-specific bootloader tooling (design.md §6.2, §7.2) ------------
# rpi: fw_setenv/fw_printenv for the U-Boot env in FAT. In scarthgap the
# historic "u-boot-fw-utils" package is provided by libubootenv
# (libubootenv-bin RPROVIDES u-boot-fw-utils).
# VERIFY-ON-BUILDER: confirm the package name split of libubootenv on the
# pinned oe-core revision.
IMAGE_INSTALL:append:rpi = " libubootenv-bin kernel-image kernel-devicetree"
# kernel-image/kernel-devicetree in the rootfs: with the upstream RAUC
# bootmeth the kernel must live INSIDE each rootfs slot so a bundle update
# also updates the kernel atomically (documented deviation from the design
# table §7.1 which put kernels on the shared FAT partition — that layout
# cannot be updated atomically by a rootfs-only bundle).
# VERIFY-ON-BUILDER: wire the in-slot boot flow (extlinux.conf or bootmeth
# script) during Milestone 1 bring-up; see README "validate first".

# x86: RAUC's efi backend shells out to efibootmgr (design.md §6.2).
IMAGE_INSTALL:append:x86-64 = " efibootmgr"

# --- mount points for the partitions of design.md §7.1 ---------------------
# The rootfs is read-only at runtime, so every mount point must exist in the
# image: /data (partition 4) and, on rpi, /boot/firmware (FAT boot partition,
# for U-Boot env access via fw_setenv).
create_atomic_mountpoints() {
    mkdir -p ${IMAGE_ROOTFS}/data
    mkdir -p ${IMAGE_ROOTFS}/boot/firmware
}
ROOTFS_POSTPROCESS_COMMAND += "create_atomic_mountpoints;"

# --- development-only access (NEVER in production images) ------------------
# Build with ATOMIC_DEV_IMAGE=1 (passed through by the kas files) to get an
# ssh server, debug-tweaks (empty root password!) and a 'dev' user.
ATOMIC_DEV_IMAGE ??= "0"

IMAGE_FEATURES += "${@'ssh-server-openssh debug-tweaks' if d.getVar('ATOMIC_DEV_IMAGE') == '1' else ''}"

# extrausers: -P sets the clear-text password at rootfs-assembly time.
# Dev convenience only; the flag gates it out of production builds.
EXTRA_USERS_PARAMS = "${@'useradd -m -s /bin/sh -P atomicdev dev;' if d.getVar('ATOMIC_DEV_IMAGE') == '1' else ''}"

# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# rofs-overlays — writable state for the read-only rootfs (design.md §7.1):
#   * mount the 4th partition ("data") at /data
#   * seed /data on first boot via systemd-tmpfiles (atomic-data-seed)
#   * overlay /etc and bind-mount /var and /home from /data subdirectories,
#     all ordered Before=local-fs.target
#   * rpi only: mount the FAT boot partition at /boot/firmware so fw_setenv
#     can reach the U-Boot environment file (uboot.env)

SUMMARY = "Writable-state mounts for the read-only atomic-updater rootfs"
LICENSE = "Apache-2.0"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/Apache-2.0;md5=89aea4e17d99a7cacdbeed46a0096b10"

inherit systemd

SRC_URI = " \
    file://data.mount \
    file://atomic-data-seed.service \
    file://atomic-data-seed.conf \
    file://etc.mount \
    file://var.mount \
    file://home.mount \
    file://boot-firmware.mount \
"

S = "${WORKDIR}"

# The data-partition device differs per machine, so the package is
# machine-specific.
PACKAGE_ARCH = "${MACHINE_ARCH}"

# x86/GPT: resolved via the GPT partition name written by
# wic/atomic-x86-efi.wks.in (--part-name data).
ATOMIC_DATA_DEVICE ?= "/dev/disk/by-partlabel/data"
# rpi/MBR: no GPT partition names exist on an msdos table (design.md §7.3
# uses raw mmcblk0pN paths for the same reason) -> partition 4.
ATOMIC_DATA_DEVICE:rpi = "/dev/mmcblk0p4"
# rpi FAT boot partition (partition 1)
ATOMIC_BOOTFW_DEVICE ?= "/dev/mmcblk0p1"

do_configure[noexec] = "1"
do_compile[noexec] = "1"

do_install() {
    install -d ${D}${systemd_system_unitdir}

    sed -e "s|@ATOMIC_DATA_DEVICE@|${ATOMIC_DATA_DEVICE}|g" \
        ${WORKDIR}/data.mount > ${D}${systemd_system_unitdir}/data.mount
    chmod 0644 ${D}${systemd_system_unitdir}/data.mount

    install -m 0644 ${WORKDIR}/etc.mount ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/var.mount ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/home.mount ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/atomic-data-seed.service ${D}${systemd_system_unitdir}/

    # Seeding manifest, applied explicitly by atomic-data-seed.service.
    # Deliberately NOT installed under ${libdir}/tmpfiles.d: it must run
    # after data.mount and before the overlay/bind mounts, not at the stock
    # systemd-tmpfiles-setup point (which is after local-fs.target).
    install -D -m 0644 ${WORKDIR}/atomic-data-seed.conf \
        ${D}${nonarch_libdir}/atomic-updater/atomic-data-seed.conf
}

do_install:append:rpi() {
    sed -e "s|@ATOMIC_BOOTFW_DEVICE@|${ATOMIC_BOOTFW_DEVICE}|g" \
        ${WORKDIR}/boot-firmware.mount > ${D}${systemd_system_unitdir}/boot-firmware.mount
    chmod 0644 ${D}${systemd_system_unitdir}/boot-firmware.mount
}

# systemd.bbclass accepts .mount units in SYSTEMD_SERVICE; all carry an
# [Install] WantedBy=local-fs.target section.
SYSTEMD_SERVICE:${PN} = "data.mount atomic-data-seed.service etc.mount var.mount home.mount"
SYSTEMD_SERVICE:${PN}:append:rpi = " boot-firmware.mount"
SYSTEMD_AUTO_ENABLE:${PN} = "enable"

FILES:${PN} += "${nonarch_libdir}/atomic-updater"

RDEPENDS:${PN} += "systemd"

# VERIFY-ON-BUILDER (interplay with oe-core read-only-rootfs):
#   * read-only-rootfs images ship volatile-binds + tmpfs on /var/volatile.
#     Our var.mount bind-mounts /data/var over /var BEFORE local-fs.target;
#     the stock /var/volatile tmpfs then mounts on top of the bind — this is
#     intended (logs under /var/log stay symlinked to volatile unless the
#     seeded copy is adjusted). Review on first boot; consider removing
#     volatile-binds if fully persistent /var is preferred.
#   * wic parts are created with --no-fstab-update so these units are the
#     single source of truth for runtime mounts (no fstab duplicates).

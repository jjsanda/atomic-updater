# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# U-Boot with the upstream RAUC bootmeth for the RPi CM4 (design.md §6.2,
# §7.2). Everything here is guarded with :rpi — the x86 target boots GRUB
# via EFI and does not build U-Boot at all.
#
# VERIFY-ON-BUILDER (critical): CONFIG_BOOTMETH_RAUC is upstream from
# U-Boot v2025.01; the default u-boot recipe in scarthgap is older
# (2024.01). The first real build must select a >= v2025.01 recipe
# (PREFERRED_VERSION_u-boot in the kas file / a mixin layer) or carry the
# bootmeth as patches. This bbappend is version-agnostic (%) on purpose.

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI:append:rpi = " \
    file://rauc-bootmeth.cfg \
    file://uboot-initial-env.txt \
"
# rauc-bootmeth.cfg is a Kconfig fragment; oe-core's u-boot.inc merges
# SRC_URI *.cfg fragments into .config during do_configure (cml1/merge_config).
# VERIFY-ON-BUILDER: confirm fragment merging on the pinned u-boot recipe —
# if absent, apply via do_configure:append with merge_config.sh.

# mkenvimage (host tool) renders the initial environment
DEPENDS:append:rpi = " u-boot-tools-native"

# Must equal CONFIG_ENV_SIZE in rauc-bootmeth.cfg and the size column in
# files/fw_env.config (16 KiB).
ATOMIC_UBOOT_ENV_SIZE ?= "0x4000"

# Deploy the initial A/B environment (BOOT_ORDER / BOOT_x_LEFT, design.md
# §7.2) as a CRC-wrapped env image. The kas rpi config adds "uboot.env" to
# IMAGE_BOOT_FILES so wic's bootimg-partition copies it into the FAT boot
# partition, where both U-Boot (CONFIG_ENV_FAT_FILE) and userspace
# fw_setenv (/etc/fw_env.config) find it.
do_deploy:append:rpi() {
    mkenvimage -s ${ATOMIC_UBOOT_ENV_SIZE} \
        -o ${DEPLOYDIR}/uboot.env \
        ${WORKDIR}/uboot-initial-env.txt
}

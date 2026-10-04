# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# Install /etc/fw_env.config for the fw_printenv/fw_setenv tools
# ("u-boot-fw-utils", provided by libubootenv in scarthgap). rpi only —
# x86 uses the RAUC efi backend and has no U-Boot environment.
#
# Placed under recipes-bsp/u-boot/ next to the u-boot bbappend so both share
# one files/ directory (fw_env.config must stay in lock-step with
# rauc-bootmeth.cfg). BBFILES matches recipes-*/*/*.bbappend, so the
# directory name not matching the recipe name is fine.

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI:append:rpi = " file://fw_env.config"

do_install:append:rpi() {
    install -D -m 0644 ${WORKDIR}/fw_env.config ${D}${sysconfdir}/fw_env.config
}

# The config is board-specific
PACKAGE_ARCH:rpi = "${MACHINE_ARCH}"

# VERIFY-ON-BUILDER: package split of libubootenv on the pinned oe-core —
# the tools live in ${PN}-bin; the config must travel with them.
FILES:${PN}-bin:append:rpi = " ${sysconfdir}/fw_env.config"

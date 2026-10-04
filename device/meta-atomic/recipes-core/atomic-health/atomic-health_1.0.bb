# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# atomic-health — post-boot health check service (design.md §7.4).
# C sources live in the monorepo at device/health (design.md §14).

SUMMARY = "Post-boot health check for atomic-updater (mark-good / mark-bad)"
LICENSE = "Apache-2.0"
# The recipe license tracks the monorepo license (Apache-2.0, see /LICENSE)
# via oe-core's bundled Apache-2.0 text; the checksum below is the stock
# common-licenses/Apache-2.0 md5.
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/Apache-2.0;md5=89aea4e17d99a7cacdbeed46a0096b10"

inherit externalsrc systemd

# --- monorepo layout choice (documented) ------------------------------------
# The health sources are NOT copied into the layer and NOT fetched over a
# FILESEXTRAPATHS file:// mirror: this is a monorepo (design.md §14) and the
# layer sits at device/meta-atomic while the sources sit at device/health.
# externalsrc with a relative path from this recipe keeps exactly one copy of
# the code, so a source change always rebuilds without version-bump churn.
# ${THISDIR} = .../device/meta-atomic/recipes-core/atomic-health, therefore
# ../../../health = .../device/health. The := is required: THISDIR is only
# valid while this file is being parsed.
EXTERNALSRC := "${THISDIR}/../../../health"
EXTERNALSRC_BUILD = "${WORKDIR}/build"

do_configure[noexec] = "1"

do_compile[dirs] = "${B}"
do_compile() {
    # VERIFY-ON-BUILDER: device/health may grow its own Makefile; switch to
    # oe_runmake then. Until that lands, compile all C sources directly.
    ${CC} ${CFLAGS} -Wall -Wextra ${LDFLAGS} -o ${B}/atomic-health ${S}/src/*.c
}

do_install() {
    # binary + probe directory (design.md §7.4: probes in
    # /usr/lib/atomic-updater/health.d/*.sh). nonarch_libdir is /usr/lib
    # regardless of multilib, matching the paths hard-coded in the design.
    install -d ${D}${nonarch_libdir}/atomic-updater/health.d
    install -m 0755 ${B}/atomic-health ${D}${nonarch_libdir}/atomic-updater/atomic-health

    for probe in ${S}/probes.d/*.sh; do
        [ -e "$probe" ] || continue
        install -m 0755 "$probe" ${D}${nonarch_libdir}/atomic-updater/health.d/
    done

    # VERIFY-ON-BUILDER: unit filename in device/health/systemd/ must be
    # atomic-health.service (design.md §7.4 unit fragment).
    install -D -m 0644 ${S}/systemd/atomic-health.service \
        ${D}${systemd_system_unitdir}/atomic-health.service
}

SYSTEMD_SERVICE:${PN} = "atomic-health.service"
SYSTEMD_AUTO_ENABLE:${PN} = "enable"

FILES:${PN} += "${nonarch_libdir}/atomic-updater"

# rauc: `rauc status mark-good|mark-bad`; chrony: clock-sync probe (§7.4/4)
RDEPENDS:${PN} += "rauc"
# fw_setenv for BOOT_x_LEFT reset on rpi (§7.2); provided by libubootenv
RDEPENDS:${PN}:append:rpi = " libubootenv-bin"
# efibootmgr is what RAUC's efi backend drives on x86 — no direct runtime
# dependency from the health binary itself (rauc handles it).

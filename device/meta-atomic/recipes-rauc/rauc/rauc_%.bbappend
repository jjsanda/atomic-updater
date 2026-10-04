# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# Machine-specific RAUC system.conf (design.md §7.3) + device keyring.
#
# The system.conf files live in unique subdirectories (files/rpi-cm4/,
# files/x86-64/) instead of machine-override subdirs on purpose: the
# meta-rauc-community demo layers ship their own rauc bbappend with a plain
# "system.conf", and unique relative paths keep the file:// lookup
# unambiguous. Our do_install:append then installs OUR copy last —
# meta-atomic has the higher layer priority (10), so its append body runs
# after the community one.
# VERIFY-ON-BUILDER: bitbake -e rauc | grep 'do_install' — confirm append
# ordering on the pinned layer stack, and drop the community layer from the
# kas file if it fights this append.

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

# system.conf differs per machine
PACKAGE_ARCH = "${MACHINE_ARCH}"

SRC_URI:append:rpi = " file://rpi-cm4/system.conf"
SRC_URI:append:x86-64 = " file://x86-64/system.conf"

do_install:append:rpi() {
    install -D -m 0644 ${WORKDIR}/rpi-cm4/system.conf ${D}${sysconfdir}/rauc/system.conf
}

do_install:append:x86-64() {
    install -D -m 0644 ${WORKDIR}/x86-64/system.conf ${D}${sysconfdir}/rauc/system.conf
}

# --- keyring (/etc/rauc/ca.cert.pem, design.md §7.3) ------------------------
# RAUC_KEYRING_FILE is an ABSOLUTE path injected by CI/site.conf (see the
# kas files' env: block and docs/design.md §9.2). Shipped read-only in the
# image; atomic-health checks its integrity every boot (§7.3).
# VERIFY-ON-BUILDER: the path lives outside SRC_URI, so sstate does NOT hash
# its content — CI must bump/clean rauc when rotating the keyring (or move
# the cert into the layer once the PKI stabilizes).
do_install:append() {
    if [ -n "${RAUC_KEYRING_FILE}" ] && [ -f "${RAUC_KEYRING_FILE}" ]; then
        install -D -m 0644 "${RAUC_KEYRING_FILE}" ${D}${sysconfdir}/rauc/ca.cert.pem
    else
        bbwarn "RAUC_KEYRING_FILE not set/found - image will lack /etc/rauc/ca.cert.pem (bundle verification will fail on device)"
    fi
}

FILES:${PN} += "${sysconfdir}/rauc"

# Runtime tools behind RAUC's bootloader backends (design.md §6.2):
RDEPENDS:${PN}:append:rpi = " libubootenv-bin"
RDEPENDS:${PN}:append:x86-64 = " efibootmgr"

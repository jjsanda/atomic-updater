# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# atomic-agent — check-in agent (design.md §7.5). Go sources live in the
# monorepo at device/agent (design.md §14).

SUMMARY = "atomic-updater device check-in agent"
DESCRIPTION = "Polls atomic-server over mTLS, downloads assigned bundles to \
/data/atomic-updater and drives rauc install."
LICENSE = "Apache-2.0"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/Apache-2.0;md5=89aea4e17d99a7cacdbeed46a0096b10"

# go-mod: module-mode Go builds in scarthgap (inherits go/goarch and brings
# the cross Go toolchain via virtual/${TARGET_PREFIX}go).
inherit go-mod systemd externalsrc

# Same monorepo/externalsrc rationale as atomic-health_1.0.bb: one copy of
# the source, reached relative to this recipe. ${THISDIR} = .../device/
# meta-atomic/recipes-core/atomic-agent -> ../../../agent = device/agent.
EXTERNALSRC := "${THISDIR}/../../../agent"
EXTERNALSRC_BUILD = "${WORKDIR}/build"

# VERIFY-ON-BUILDER: must equal the module line in device/agent/go.mod.
GO_IMPORT = "atomic-updater/device/agent"

# design.md §7.5: statically linked, no CGO.
export CGO_ENABLED = "0"

# do_compile has no network in scarthgap. The agent is stdlib-only by design
# (§7.5); if a third-party module is ever added it MUST be vendored in
# device/agent/vendor/ (go mod vendor) — the build will then use it
# automatically in module mode.
do_compile[dirs] = "${B}"
do_compile() {
    cd ${S}
    # ${GO} and ${GOBUILDFLAGS} come from go.bbclass (GOOS/GOARCH exported
    # for the target; -trimpath included for reproducibility).
    # VERIFY-ON-BUILDER: main package path under device/agent/cmd/.
    ${GO} build ${GOBUILDFLAGS} -o ${B}/atomic-agent ./cmd/atomic-agent
}

# The systemd unit is authored here in the layer (files/atomic-agent.service);
# externalsrc keeps file:// entries in SRC_URI working.
SRC_URI = "file://atomic-agent.service"

do_install() {
    install -D -m 0755 ${B}/atomic-agent ${D}${bindir}/atomic-agent
    install -D -m 0644 ${WORKDIR}/atomic-agent.service \
        ${D}${systemd_system_unitdir}/atomic-agent.service
}

SYSTEMD_SERVICE:${PN} = "atomic-agent.service"
SYSTEMD_AUTO_ENABLE:${PN} = "enable"

# agent shells out to / D-Bus-talks to rauc for `rauc install`;
# ca-certificates for TLS to the object store presigned URLs.
RDEPENDS:${PN} += "rauc ca-certificates"

# Statically linked pure-Go binaries carry no GNU_HASH section and trip the
# ldflags QA check; standard practice for Go recipes.
INSANE_SKIP:${PN} += "ldflags"

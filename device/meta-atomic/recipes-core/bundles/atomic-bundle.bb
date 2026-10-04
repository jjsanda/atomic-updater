# NOTE: authored for the org CI runners; NOT built/validated in this workspace.
#
# RAUC update bundle carrying the atomic-image rootfs (design.md §9,
# api.md compatible string, §7.3 system.conf [system] compatible=).

SUMMARY = "RAUC update bundle for atomic-updater devices"
DESCRIPTION = "verity-format RAUC bundle with a single rootfs slot image \
built from atomic-image."
LICENSE = "Apache-2.0"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/Apache-2.0;md5=89aea4e17d99a7cacdbeed46a0096b10"

# meta-rauc bundle class: builds ${DEPLOY_DIR_IMAGE}/bundles/*.raucb
inherit bundle

# Must match [system] compatible= in /etc/rauc/system.conf (both machine
# variants set acme-device-v1, see recipes-rauc/rauc/files/*/system.conf).
RAUC_BUNDLE_COMPATIBLE = "acme-device-v1"

# CI overrides with the release semver (design.md §11.1 monotonic versions;
# the server enforces downgrade rejection, api.md §1.2 offer step 4).
RAUC_BUNDLE_VERSION ?= "${PV}"
RAUC_BUNDLE_DESCRIPTION = "atomic-updater OS bundle (${MACHINE})"

# design.md §9: verity format (RAUC >= 1.7) for streamable install and
# per-chunk verification; also the prerequisite for Phase-2 adaptive updates.
RAUC_BUNDLE_FORMAT = "verity"

RAUC_BUNDLE_SLOTS = "rootfs"
RAUC_SLOT_rootfs = "atomic-image"
RAUC_SLOT_rootfs[fstype] = "ext4"

# --- signing (design.md §9.2) -----------------------------------------------
# RAUC_KEY_FILE / RAUC_CERT_FILE / RAUC_KEYRING_FILE are intentionally NOT
# set here. They arrive as absolute paths from site.conf or from the CI
# environment: the kas files declare them under `env:`, which puts them on
# BB_ENV_PASSTHROUGH_ADDITIONS so the exported values become BitBake
# variables visible to bundle.bbclass.
#
#   CI (build:device stage):   signs with the DEV key -> staging/ prefix
#   signer runner (sign stage): re-signs with the release cert
#                               (`rauc resign`) -> signed/ prefix
#
# A build without these variables set MUST fail in do_bundle — that is the
# desired behavior (no accidentally unsigned/unsignable bundles).
#
# VERIFY-ON-BUILDER: exact variable spelling against the pinned meta-rauc
# revision (RAUC_KEY_FILE, RAUC_CERT_FILE, RAUC_KEYRING_FILE as of
# meta-rauc scarthgap).

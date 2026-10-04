#!/bin/bash
# DEGRADED install path for environments where `rauc install` cannot
# loop-mount the bundle (no privileged containers). Performs the same
# security-relevant steps rauc would:
#
#   1. signature chain verification against the device keyring (rauc info —
#      pure userspace, no mounts)
#   2. extraction of manifest + slot image (unsquashfs tolerates the appended
#      CMS signature)
#   3. per-image sha256 verification against the SIGNED manifest
#   4. write to the inactive slot, then flip BOOT_ORDER via the backend
#
# The agent invokes this via install_cmd when ATOMIC_AGENT_INSTALL_CMD is set
# by the e2e harness (fallback tier). See docs/security.md.
set -euo pipefail

bundle="${1:?usage: rauc-install-fallback.sh <bundle.raucb>}"
keyring=/etc/rauc/ca.cert.pem
slot="${ATOMIC_SLOT:?ATOMIC_SLOT not set}"
target=$([[ "$slot" == "A" ]] && echo B || echo A)

log() { echo "[rauc-install-fallback] $*"; }
log "DEGRADED MODE: unprivileged install of $bundle to slot $target"

# 1. Signature verification — hard gate, identical trust anchor to rauc install.
if ! rauc info --keyring="$keyring" "$bundle" >/dev/null; then
    log "signature verification FAILED — refusing to install"
    exit 1
fi
compatible="$(rauc info --keyring="$keyring" --output-format=shell "$bundle" \
    | sed -n 's/^RAUC_MF_COMPATIBLE=//p' | tr -d "'\"")"
if [[ "$compatible" != "acme-device-v1" ]]; then
    log "compatible mismatch: '$compatible'"
    exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# 2. Extract the manifest and slot image.
unsquashfs -q -n -f -d "$work" "$bundle" manifest.raucm rootfs.img >/dev/null

# 3. Verify the image hash against the signed manifest.
want="$(sed -n '/^\[image.rootfs\]/,/^\[/{s/^sha256=//p}' "$work/manifest.raucm" | head -1)"
got="$(sha256sum "$work/rootfs.img" | awk '{print $1}')"
if [[ -z "$want" || "$want" != "$got" ]]; then
    log "image sha256 mismatch (want ${want:-<none>}, got $got)"
    exit 1
fi

# 4. Write inactive slot + flip boot order (what rauc's install step 3c does).
cp "$work/rootfs.img" "/data/slots/rootfs-${target}.img.new"
mv "/data/slots/rootfs-${target}.img.new" "/data/slots/rootfs-${target}.img"
custom-backend.sh set-primary "$target"
log "installed to slot $target, BOOT_ORDER flipped"

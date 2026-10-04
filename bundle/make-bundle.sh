#!/usr/bin/env bash
# Produce a SIGNED RAUC bundle + .meta.json sidecar (docs/api.md §2.3).
# Runs inside the rauc-tools container; the PKI directory is mounted in.
#
#   make-bundle.sh --version 1.1.0 --hardware x86_64-generic \
#                  --pki /pki --out-dir /out \
#                  [--format verity|plain] [--panic] [--unhealthy] [--bad-key]
#
# --bad-key signs with the out-of-hierarchy key (pki/badkey) — devices must
# reject it. RAUC cannot produce truly unsigned bundles (that is a feature);
# "reject unsigned" is therefore exercised as "reject untrusted signature",
# see docs/security.md.
set -euo pipefail

VERSION="" HARDWARE="" PKI="" OUT_DIR="" FORMAT="verity"
ROOTFS_ARGS=() BAD_KEY=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)    VERSION="$2"; shift 2 ;;
        --hardware)   HARDWARE="$2"; shift 2 ;;
        --pki)        PKI="$2"; shift 2 ;;
        --out-dir)    OUT_DIR="$2"; shift 2 ;;
        --format)     FORMAT="$2"; shift 2 ;;
        --payload-mb) ROOTFS_ARGS+=(--payload-mb "$2"); shift 2 ;;
        --panic)      ROOTFS_ARGS+=(--panic); shift ;;
        --unhealthy)  ROOTFS_ARGS+=(--unhealthy); shift ;;
        --bad-key)    BAD_KEY=1; shift ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done
[[ -n "$VERSION" && -n "$HARDWARE" && -n "$PKI" && -n "$OUT_DIR" ]] || {
    echo "usage: $0 --version V --hardware HW --pki DIR --out-dir DIR [...]" >&2; exit 2; }

if [[ "$BAD_KEY" == 1 ]]; then
    CERT="$PKI/badkey/bad.cert.pem"
    KEY="$PKI/badkey/bad.key.pem"
    INTERMEDIATE=()
else
    CERT="$PKI/bundle/bundle.cert.pem"
    KEY="$PKI/bundle/bundle.key.pem"
    # Ship the signing CA in the CMS so devices can chain leaf → root.
    INTERMEDIATE=(--intermediate "$PKI/signing/ca.cert.pem")
fi
KEYRING="$PKI/keyring.pem"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$OUT_DIR"

mkdir -p "$work/content"
make-rootfs.sh --version "$VERSION" --out "$work/content/rootfs.img" \
    ${ROOTFS_ARGS[@]+"${ROOTFS_ARGS[@]}"}

cat > "$work/content/manifest.raucm" <<EOF
[update]
compatible=acme-device-v1
version=$VERSION

[bundle]
format=$FORMAT

[image.rootfs]
filename=rootfs.img
EOF

name="acme-device-v1-${HARDWARE}-${VERSION}$([[ "$BAD_KEY" == 1 ]] && echo '-badkey' || true)"
out="$OUT_DIR/$name.raucb"
rm -f "$out"
rauc bundle --cert="$CERT" --key="$KEY" ${INTERMEDIATE[@]+"${INTERMEDIATE[@]}"} \
    "$work/content" "$out"

# Self-check: a good bundle must verify against the device keyring; a bad-key
# bundle must NOT (otherwise the negative e2e test is meaningless).
if [[ "$BAD_KEY" == 1 ]]; then
    if rauc info --keyring="$KEYRING" "$out" >/dev/null 2>&1; then
        echo "FATAL: bad-key bundle verifies against the keyring" >&2
        exit 1
    fi
    echo "verified: $name is correctly UNTRUSTED by the device keyring"
else
    rauc info --keyring="$KEYRING" "$out" >/dev/null
    echo "verified: $name validates against the device keyring"
fi

sha256="$(sha256sum "$out" | awk '{print $1}')"
size="$(stat -c %s "$out")"
signed_by="$(openssl x509 -in "$CERT" -outform DER | sha256sum | awk '{print $1}')"
jq -n \
    --arg version "$VERSION" \
    --arg hw "$HARDWARE" \
    --arg sha "$sha256" \
    --argjson size "$size" \
    --arg signer "$signed_by" \
    '{version: $version, hardware_type: $hw, sha256: $sha, size_bytes: $size, signed_by: $signer}' \
    > "$out.meta.json"

echo "bundle: $out ($size bytes, sha256 $sha256)"

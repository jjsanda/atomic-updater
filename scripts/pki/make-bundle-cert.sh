#!/usr/bin/env bash
# Per-release bundle signing certificate (90-day rotation, design doc §9.1).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pki_leaf "$PKI_DIR/bundle" bundle \
    "/O=Acme Devices/CN=Acme Dev Bundle Signer $(date -u +%Y-%m)" \
    90 "$PKI_DIR/signing" \
    "keyUsage=critical,digitalSignature"

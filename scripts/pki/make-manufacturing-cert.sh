#!/usr/bin/env bash
# Shared manufacturing client certificate (design doc §11.3 / open question Q2:
# per-device factory certs would be strictly better; this is the MVP shape).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pki_leaf "$PKI_DIR/manufacturing" mfg-fleet \
    "/O=Acme Devices/CN=Acme Dev Manufacturing Fleet Cert" \
    365 "$PKI_DIR/manufacturing" \
    "keyUsage=critical,digitalSignature" \
    "extendedKeyUsage=clientAuth"

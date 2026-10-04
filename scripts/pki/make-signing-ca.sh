#!/usr/bin/env bash
# Dev stand-in for the offline bundle-signing CA (2y, root-signed).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pki_child_ca "$PKI_DIR/signing" "/O=Acme Devices/CN=Acme Dev Signing CA" 730 "$PKI_DIR/root"

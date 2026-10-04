#!/usr/bin/env bash
# ONLINE device-issuing CA. atomic-server holds this key and signs device
# certificates during first-boot enrollment (design doc §11.3).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pki_child_ca "$PKI_DIR/device-ca" "/O=Acme Devices/CN=Acme Dev Device CA" 1825 "$PKI_DIR/root"

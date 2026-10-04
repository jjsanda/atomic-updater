#!/usr/bin/env bash
# Dev stand-in for the offline organization root CA (design doc §9.1).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pki_selfsigned_ca "$PKI_DIR/root" "/O=Acme Devices/CN=Acme Dev Root CA" 3650

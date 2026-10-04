#!/usr/bin/env bash
# Manufacturing CA — signs the (shared) manufacturing certificate flashed at
# the factory, used only to authenticate first-boot enrollment.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pki_child_ca "$PKI_DIR/manufacturing" "/O=Acme Devices/CN=Acme Dev Manufacturing CA" 1825 "$PKI_DIR/root"

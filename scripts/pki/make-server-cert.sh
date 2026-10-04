#!/usr/bin/env bash
# TLS server certificate for the device-facing listener. Root-signed in dev so
# agents can pin exactly one trust anchor (the root cert / keyring.pem).
# Extra SANs: SERVER_CERT_SANS="DNS:foo,IP:10.0.0.5" ./make-server-cert.sh
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

SANS="DNS:localhost,DNS:atomic-server,IP:127.0.0.1${SERVER_CERT_SANS:+,$SERVER_CERT_SANS}"

pki_leaf "$PKI_DIR/server" server \
    "/O=Acme Devices/CN=atomic-server" \
    365 "$PKI_DIR/root" \
    "keyUsage=critical,digitalSignature" \
    "extendedKeyUsage=serverAuth" \
    "subjectAltName=$SANS"

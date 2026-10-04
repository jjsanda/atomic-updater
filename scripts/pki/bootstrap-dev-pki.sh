#!/usr/bin/env bash
# One-shot development PKI bootstrap (design doc §9.1 hierarchy + the online
# device CA, manufacturing chain, server TLS cert and negative-test material).
#
#   PKI_DIR=./pki scripts/pki/bootstrap-dev-pki.sh
#
# Idempotent: existing certificates are kept.
set -euo pipefail
here="$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source "$here/lib.sh"

echo "== atomic-updater dev PKI → $PKI_DIR"
"$here/make-root-ca.sh"
"$here/make-signing-ca.sh"
"$here/make-bundle-cert.sh"
"$here/make-device-ca.sh"
"$here/make-manufacturing-ca.sh"
"$here/make-manufacturing-cert.sh"
"$here/make-server-cert.sh"
"$here/make-badkey-cert.sh"

# Device-side trust anchor (RAUC keyring + agent's server pin) is the root CA.
cp "$PKI_DIR/root/ca.cert.pem" "$PKI_DIR/keyring.pem"
# Chain handed to devices at enrollment (their client cert's provenance).
cat "$PKI_DIR/device-ca/ca.cert.pem" "$PKI_DIR/root/ca.cert.pem" > "$PKI_DIR/ca-chain.pem"

"$here/gen-crl.sh"

echo "== verifying chains"
v() { openssl verify "$@" >/dev/null && echo "ok: ${*: -1}"; }
v -CAfile "$PKI_DIR/root/ca.cert.pem" "$PKI_DIR/signing/ca.cert.pem"
v -CAfile "$PKI_DIR/root/ca.cert.pem" "$PKI_DIR/device-ca/ca.cert.pem"
v -CAfile "$PKI_DIR/root/ca.cert.pem" "$PKI_DIR/manufacturing/ca.cert.pem"
v -CAfile "$PKI_DIR/root/ca.cert.pem" -untrusted "$PKI_DIR/signing/ca.cert.pem" "$PKI_DIR/bundle/bundle.cert.pem"
v -CAfile "$PKI_DIR/root/ca.cert.pem" -untrusted "$PKI_DIR/manufacturing/ca.cert.pem" "$PKI_DIR/manufacturing/mfg-fleet.cert.pem"
v -CAfile "$PKI_DIR/root/ca.cert.pem" "$PKI_DIR/server/server.cert.pem"
if openssl verify -CAfile "$PKI_DIR/root/ca.cert.pem" "$PKI_DIR/badkey/bad.cert.pem" >/dev/null 2>&1; then
    echo "FATAL: badkey cert verifies against the root — negative tests would be meaningless" >&2
    exit 1
fi
echo "ok: badkey cert correctly NOT trusted by root"
echo "== dev PKI ready"

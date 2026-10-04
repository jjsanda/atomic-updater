#!/usr/bin/env bash
# Self-signed certificate OUTSIDE the org hierarchy. Used by the e2e suite to
# sign a bundle that devices must reject (RAUC cannot emit unsigned bundles,
# so "reject unsigned" is exercised as "reject untrusted signature" — see
# docs/security.md).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

dir="$PKI_DIR/badkey"
mkdir -p "$dir"
[[ -f "$dir/bad.cert.pem" ]] && { echo "exists: $dir/bad.cert.pem (skip)"; exit 0; }
pki_gen_key "$dir/bad.key.pem"
openssl req -x509 -new -key "$dir/bad.key.pem" -sha256 -days 90 \
    -subj "/O=Eve Corp/CN=Untrusted Bundle Signer" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=critical,digitalSignature" \
    -out "$dir/bad.cert.pem"
echo "created: $dir/bad.cert.pem"

#!/usr/bin/env bash
# Generate a CRL for the device CA.
#
# The operative revocation mechanism is the server's per-request fingerprint
# check against the revoked_certs table (design doc §11.1). This script exists
# for external consumers that want a standard CRL artifact: it synthesizes an
# openssl `ca` database from pki/device-ca/revoked.csv and emits
# pki/crl/device-ca.crl.pem.
#
# revoked.csv line format:  <hex_serial>,<subject CN>
# (hex serial without 0x prefix, as printed by `openssl x509 -serial`)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

CA_DIR="$PKI_DIR/device-ca"
DB_DIR="$CA_DIR/db"
CRL_DIR="$PKI_DIR/crl"
mkdir -p "$DB_DIR" "$CRL_DIR"

: > "$DB_DIR/index.txt"
[[ -f "$DB_DIR/crlnumber" ]] || echo 1000 > "$DB_DIR/crlnumber"

# openssl index.txt: status \t expiry \t revdate[,reason] \t serial \t file \t subject
now="$(date -u +%y%m%d%H%M%SZ)"
expiry="$(date -u -d '+2 years' +%y%m%d%H%M%SZ)"
if [[ -f "$CA_DIR/revoked.csv" ]]; then
    while IFS=, read -r serial cn; do
        [[ -z "$serial" ]] && continue
        printf 'R\t%s\t%s,unspecified\t%s\tunknown\t/CN=%s\n' \
            "$expiry" "$now" "$serial" "$cn" >> "$DB_DIR/index.txt"
    done < "$CA_DIR/revoked.csv"
fi

cfg="$(mktemp)"
trap 'rm -f "$cfg"' EXIT
cat > "$cfg" <<EOF
[ca]
default_ca = crl_ca
[crl_ca]
database = $DB_DIR/index.txt
crlnumber = $DB_DIR/crlnumber
default_md = sha256
default_crl_days = 30
EOF

openssl ca -config "$cfg" -gencrl \
    -keyfile "$CA_DIR/ca.key.pem" -cert "$CA_DIR/ca.cert.pem" \
    -out "$CRL_DIR/device-ca.crl.pem" 2>/dev/null

openssl crl -in "$CRL_DIR/device-ca.crl.pem" -noout
echo "created: $CRL_DIR/device-ca.crl.pem ($(grep -c '^R' "$DB_DIR/index.txt" || true) revoked entries)"

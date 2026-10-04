#!/usr/bin/env bash
# Shared helpers for the atomic-updater development PKI.
#
# These scripts create a DEVELOPMENT hierarchy only. The production root and
# signing CAs are air-gapped (design doc §9.1); the ceremony runbook lives in
# docs/security.md. Everything here targets openssl 3.0.
#
# Layout produced under $PKI_DIR (default: ./pki):
#   root/           ca.cert.pem ca.key.pem            10y, self-signed
#   signing/        ca.cert.pem ca.key.pem            2y, root-signed
#   bundle/         bundle.cert.pem bundle.key.pem    90d, signing-CA-signed
#   device-ca/      ca.cert.pem ca.key.pem            5y, root-signed (ONLINE)
#   manufacturing/  ca.cert.pem ca.key.pem            5y, root-signed
#                   mfg-fleet.cert.pem/.key.pem       1y, mfg-CA-signed
#   server/         server.cert.pem server.key.pem    1y, root-signed
#   badkey/         bad.cert.pem bad.key.pem          self-signed, OUTSIDE the
#                                                     hierarchy (negative tests)
#   keyring.pem     root CA cert (device RAUC keyring + agent server pin)
#   ca-chain.pem    device CA + root (returned to devices on enrollment)
#   crl/            device-ca.crl.pem

set -euo pipefail

PKI_DIR="${PKI_DIR:-$(pwd)/pki}"

# All dev keys are EC P-256: small, fast, supported by RAUC/OpenSSL/Go alike.
pki_gen_key() { # <out.key.pem>
    openssl ecparam -name prime256v1 -genkey -noout -out "$1"
    chmod 600 "$1"
}

pki_random_serial() {
    printf '0x%s' "$(openssl rand -hex 16)"
}

# Self-signed CA: pki_selfsigned_ca <dir> <subject> <days>
pki_selfsigned_ca() {
    local dir="$1" subj="$2" days="$3"
    mkdir -p "$dir"
    [[ -f "$dir/ca.cert.pem" ]] && { echo "exists: $dir/ca.cert.pem (skip)"; return 0; }
    pki_gen_key "$dir/ca.key.pem"
    openssl req -x509 -new -key "$dir/ca.key.pem" -sha256 -days "$days" \
        -subj "$subj" \
        -addext "basicConstraints=critical,CA:TRUE" \
        -addext "keyUsage=critical,keyCertSign,cRLSign" \
        -addext "subjectKeyIdentifier=hash" \
        -out "$dir/ca.cert.pem"
    echo "created: $dir/ca.cert.pem"
}

# Intermediate CA signed by a parent: pki_child_ca <dir> <subject> <days> <parent_dir>
pki_child_ca() {
    local dir="$1" subj="$2" days="$3" parent="$4"
    mkdir -p "$dir"
    [[ -f "$dir/ca.cert.pem" ]] && { echo "exists: $dir/ca.cert.pem (skip)"; return 0; }
    pki_gen_key "$dir/ca.key.pem"
    openssl req -new -key "$dir/ca.key.pem" -subj "$subj" -out "$dir/ca.csr.pem"
    openssl x509 -req -in "$dir/ca.csr.pem" -sha256 -days "$days" \
        -CA "$parent/ca.cert.pem" -CAkey "$parent/ca.key.pem" \
        -set_serial "$(pki_random_serial)" \
        -extfile <(printf '%s\n' \
            "basicConstraints=critical,CA:TRUE,pathlen:0" \
            "keyUsage=critical,keyCertSign,cRLSign" \
            "subjectKeyIdentifier=hash" \
            "authorityKeyIdentifier=keyid:always") \
        -out "$dir/ca.cert.pem"
    rm -f "$dir/ca.csr.pem"
    echo "created: $dir/ca.cert.pem"
}

# Leaf certificate: pki_leaf <dir> <name> <subject> <days> <parent_dir> <extensions...>
# Each extra argument is one x509v3 extension line.
pki_leaf() {
    local dir="$1" name="$2" subj="$3" days="$4" parent="$5"
    shift 5
    mkdir -p "$dir"
    [[ -f "$dir/$name.cert.pem" ]] && { echo "exists: $dir/$name.cert.pem (skip)"; return 0; }
    pki_gen_key "$dir/$name.key.pem"
    openssl req -new -key "$dir/$name.key.pem" -subj "$subj" -out "$dir/$name.csr.pem"
    openssl x509 -req -in "$dir/$name.csr.pem" -sha256 -days "$days" \
        -CA "$parent/ca.cert.pem" -CAkey "$parent/ca.key.pem" \
        -set_serial "$(pki_random_serial)" \
        -extfile <(printf '%s\n' \
            "basicConstraints=critical,CA:FALSE" \
            "subjectKeyIdentifier=hash" \
            "authorityKeyIdentifier=keyid:always" \
            "$@") \
        -out "$dir/$name.cert.pem"
    rm -f "$dir/$name.csr.pem"
    echo "created: $dir/$name.cert.pem"
}

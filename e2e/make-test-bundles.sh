#!/usr/bin/env bash
# Build the standard e2e bundle set (signed, plus one untrusted) into the
# given output directory using the rauc-tools image.
#
#   e2e/make-test-bundles.sh <out-dir> [format]
#
# Set: 0.9.0 good (downgrade tests), 1.1.0 good (happy path),
#      1.2.0 --panic (boot-loop rollback), 1.3.0 --unhealthy (health-fail
#      rollback), 1.4.0 --bad-key (untrusted signature).
set -euo pipefail

OUT_DIR="${1:?usage: $0 <out-dir> [verity|plain]}"
FORMAT="${2:-verity}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$OUT_DIR"
OUT_ABS="$(cd "$OUT_DIR" && pwd)"

# Purge stale artifacts: the whole directory gets uploaded to signed/, and a
# key that is re-uploaded with different bytes AFTER registration poisons the
# registry (the DB keeps the old sha256 — signed/ keys are immutable by
# contract, see docs/operations.md).
rm -f "$OUT_ABS"/*.raucb "$OUT_ABS"/*.raucb.meta.json 2>/dev/null || true

[[ -f "$root/pki/keyring.pem" ]] || { echo "run 'make pki' first" >&2; exit 1; }

build() {
    docker run --rm \
        -v "$root/pki:/pki:ro" \
        -v "$OUT_ABS:/out" \
        atomic-updater/rauc-tools:dev \
        make-bundle.sh --pki /pki --out-dir /out --hardware x86_64-generic \
        --format "$FORMAT" "$@"
}

build --version 0.9.0
build --version 1.1.0
build --version 1.2.0 --panic
build --version 1.3.0 --unhealthy
build --version 1.4.0 --bad-key
# Big payload: gives TestResumeAfterNetworkDrop a wide window to interrupt.
build --version 1.5.0 --payload-mb 40
echo "test bundles ready in $OUT_ABS (format: $FORMAT)"

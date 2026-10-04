#!/usr/bin/env bash
# Build a miniature ext4 rootfs image UNPRIVILEGED (mke2fs -d). This stands in
# for the Yocto-built rootfs in environments without BitBake: it carries the
# version marker, a fake application and the health probes the atomic-health
# service will execute.
#
#   make-rootfs.sh --version 1.1.0 --out rootfs.img [--size-mb 64]
#                  [--payload-mb 8] [--panic] [--unhealthy]
#
# --payload-mb adds incompressible data (stand-in for the real OS payload).
# Without it the image squashes to ~4 KiB, which RAUC's verity format
# rejects — and download-resume tests need something worth interrupting.
#
# --panic     places /etc/BOOT_PANIC — the device simulator treats booting
#             this rootfs as an immediate crash before the health check
#             (kernel-panic analog, design doc §10.2).
# --unhealthy ships a health probe that always fails (§10.3).
set -euo pipefail

VERSION="" OUT="" SIZE_MB=64 PAYLOAD_MB=8 PANIC=0 UNHEALTHY=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)    VERSION="$2"; shift 2 ;;
        --out)        OUT="$2"; shift 2 ;;
        --size-mb)    SIZE_MB="$2"; shift 2 ;;
        --payload-mb) PAYLOAD_MB="$2"; shift 2 ;;
        --panic)      PANIC=1; shift ;;
        --unhealthy)  UNHEALTHY=1; shift ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done
[[ -n "$VERSION" && -n "$OUT" ]] || { echo "usage: $0 --version V --out FILE" >&2; exit 2; }

tree="$(mktemp -d)"
trap 'rm -rf "$tree"' EXIT

mkdir -p "$tree/etc" "$tree/usr/lib/atomic-updater/health.d" "$tree/app"
printf '%s\n' "$VERSION" > "$tree/etc/bundle-version"

cat > "$tree/app/run.sh" <<'EOF'
#!/bin/sh
echo "fake application $0 running"
EOF
chmod +x "$tree/app/run.sh"

cat > "$tree/usr/lib/atomic-updater/health.d/10-app.sh" <<EOF
#!/bin/sh
# Shipped-with-the-rootfs health probe. An unhealthy build fails here,
# which drives rauc mark-bad + reboot (design doc §7.4).
$( if [[ "$UNHEALTHY" == 1 ]]; then
       echo 'echo "probe: simulated application failure"; exit 1'
   else
       echo 'echo "probe: app healthy"; exit 0'
   fi )
EOF
chmod +x "$tree/usr/lib/atomic-updater/health.d/10-app.sh"

if [[ "$PANIC" == 1 ]]; then
    echo "this rootfs simulates a kernel panic at boot" > "$tree/etc/BOOT_PANIC"
fi

if [[ "$PAYLOAD_MB" -gt 0 ]]; then
    dd if=/dev/urandom of="$tree/app/payload.bin" bs=1M count="$PAYLOAD_MB" status=none
fi

rm -f "$OUT"
truncate -s "${SIZE_MB}M" "$OUT"
mke2fs -q -F -t ext4 -L rootfs -d "$tree" "$OUT"
echo "rootfs: $OUT (version $VERSION, panic=$PANIC, unhealthy=$UNHEALTHY)"

#!/bin/bash
# PID 1 of the device simulator: the "power supply". Provisions the factory
# state on first start, then power-cycles boot-sim forever. A boot ends when
# reboot-sim kills its process group (or it crashes); the next boot re-runs
# slot selection exactly like U-Boot after a watchdog reset.
set -u

log() { echo "[supervisor] $*"; }

mkdir -p /data/boot /data/slots /data/atomic-updater /run

if [[ ! -f /data/boot/uboot-env ]]; then
    printf 'BOOT_ORDER=A B\nBOOT_A_LEFT=3\nBOOT_B_LEFT=3\n' > /data/boot/uboot-env
    log "initialized U-Boot environment"
fi

if [[ ! -f /data/slots/rootfs-A.img ]]; then
    initial="${SIM_INITIAL_VERSION:-1.0.0}"
    log "factory-flashing slots with version $initial"
    make-rootfs.sh --version "$initial" --out /data/slots/rootfs-A.img >/dev/null
    cp /data/slots/rootfs-A.img /data/slots/rootfs-B.img
fi

boot=0
while true; do
    if [[ -e /data/RESCUE_HALT ]]; then
        log "RESCUE HALT latched — manual recovery required (docs/operations.md)"
        sleep infinity
    fi
    boot=$((boot + 1))
    log "power-on #$boot"
    setsid bash /usr/lib/atomic-updater/boot-sim.sh &
    pid=$!
    echo "$pid" > /run/boot.pgid
    wait "$pid"
    code=$?
    log "boot #$boot ended (exit $code); power-cycling"
    sleep 1
done

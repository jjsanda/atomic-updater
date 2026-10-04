#!/bin/bash
# One simulated boot. Reproduces the §7.2 U-Boot flow, then "userspace":
# atomic-health (real C binary) followed by atomic-agent (real Go binary).
#
#   1. pick the first slot in BOOT_ORDER with BOOT_x_LEFT > 0, decrement it
#   2. no bootable slot → rescue halt (the tests assert this NEVER happens)
#   3. extract the slot image (debugfs — no mount privileges needed to boot)
#   4. /etc/BOOT_PANIC in the rootfs → simulated kernel panic (exit before
#      health, counter stays decremented) — §10.2
#   5. run atomic-health: mark-good (counter reset) or mark-bad + reboot — §10.3
#   6. run atomic-agent in the foreground
set -uo pipefail
source /usr/lib/atomic-updater/sim-env.sh

log() { echo "[boot-sim] $*"; }

# --- U-Boot slot selection ---
chosen=""
attempts_left=""
(
    flock -x 9
    order="$(sed -n 's/^BOOT_ORDER=//p' "$SIM_ENV_FILE" | head -1)"
    for slot in $order; do
        left="$(sed -n "s/^BOOT_${slot}_LEFT=//p" "$SIM_ENV_FILE" | head -1)"
        if [[ "${left:-0}" -gt 0 ]]; then
            {
                grep -v "^BOOT_${slot}_LEFT=" "$SIM_ENV_FILE"
                printf 'BOOT_%s_LEFT=%s\n' "$slot" "$((left - 1))"
            } > "${SIM_ENV_FILE}.tmp"
            mv "${SIM_ENV_FILE}.tmp" "$SIM_ENV_FILE"
            printf '%s %s\n' "$slot" "$left" > /run/boot.choice
            exit 0
        fi
    done
    : > /run/boot.choice
) 9>"$SIM_ENV_LOCK"

read -r chosen attempts_left < /run/boot.choice || true
if [[ -z "${chosen:-}" ]]; then
    log "RESCUE: no bootable slot (all counters exhausted) — halting like U-Boot at the serial console"
    touch /data/RESCUE_HALT
    sleep infinity
fi
log "booting slot $chosen (attempts remaining was $attempts_left, now $((attempts_left - 1)))"

# --- "kernel + rootfs" ---
ROOTFS=/run/rootfs
rm -rf "$ROOTFS"
mkdir -p "$ROOTFS"
if ! debugfs -R "rdump / $ROOTFS" "/data/slots/rootfs-${chosen}.img" >/dev/null 2>&1; then
    log "PANIC: slot $chosen image unreadable (corrupt rootfs)"
    exit 41
fi
version="$(cat "$ROOTFS/etc/bundle-version" 2>/dev/null || echo unknown)"
log "slot $chosen carries version $version"

if [[ -e "$ROOTFS/etc/BOOT_PANIC" ]]; then
    log "KERNEL PANIC (simulated): $(cat "$ROOTFS/etc/BOOT_PANIC")"
    exit 42
fi

uuidgen > /run/boot_id

# --- atomic-health (real binary, per-boot env) ---
export ATOMIC_SLOT="$chosen"
export ATOMIC_APP_UNIT=""   # no systemd in the sim; app health comes from probes
export ATOMIC_HEALTH_DIR="$ROOTFS/usr/lib/atomic-updater/health.d"
export ATOMIC_PROBE_TIMEOUT="${ATOMIC_PROBE_TIMEOUT:-10}"
export ATOMIC_REBOOT_CMD="reboot-sim"
export ATOMIC_RAUC_EXTRA_ARGS="--override-boot-slot=${chosen}"

if ! atomic-health; then
    # mark-bad + reboot-sim were already invoked by atomic-health; if the
    # process group survived (shouldn't), end this boot explicitly.
    log "health check failed; ending boot"
    exit 43
fi
log "slot $chosen confirmed good"

# --- atomic-agent (real binary, env-driven config) ---
export ATOMIC_AGENT_SLOT="$chosen"
export ATOMIC_AGENT_VERSION_FILE="$ROOTFS/etc/bundle-version"
export ATOMIC_AGENT_BOOT_ID_FILE=/run/boot_id
export ATOMIC_AGENT_RAUC_EXTRA_ARGS="--override-boot-slot=${chosen}"
export ATOMIC_AGENT_REBOOT_CMD="reboot-sim"
export ATOMIC_AGENT_SERIAL="${ATOMIC_AGENT_SERIAL:-$(hostname)}"
log "starting atomic-agent"
exec atomic-agent -config ""

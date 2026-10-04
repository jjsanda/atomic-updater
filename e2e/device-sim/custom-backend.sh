#!/bin/bash
# RAUC custom bootloader backend (system.conf [handlers]
# bootloader-custom-backend). RAUC invokes it as:
#
#   custom-backend.sh get-primary              → print bootname, exit 0
#   custom-backend.sh set-primary <bootname>
#   custom-backend.sh get-state  <bootname>    → print "good"|"bad"
#   custom-backend.sh set-state  <bootname> <good|bad>
#
# Semantics map 1:1 onto design doc §7.2:
#   primary        = first slot in BOOT_ORDER
#   set-primary X  = BOOT_ORDER="X <other>", BOOT_X_LEFT=3   (install step 3c)
#   state good/bad = BOOT_x_LEFT 3 / 0                        (mark-good/bad)
set -euo pipefail
source /usr/lib/atomic-updater/sim-env.sh

other() { [[ "$1" == "A" ]] && echo B || echo A; }

valid_slot() {
    [[ "$1" == "A" || "$1" == "B" ]] || { echo "unknown bootname $1" >&2; exit 1; }
}

case "${1:-}" in
    get-primary)
        order="$(env_get BOOT_ORDER)"
        primary="${order%% *}"
        [[ -n "$primary" ]] || { echo "BOOT_ORDER unset" >&2; exit 1; }
        echo "$primary"
        ;;
    set-primary)
        valid_slot "${2:-}"
        env_set BOOT_ORDER "$2 $(other "$2")"
        env_set "BOOT_${2}_LEFT" 3
        ;;
    get-state)
        valid_slot "${2:-}"
        left="$(env_get "BOOT_${2}_LEFT")"
        if [[ "${left:-0}" -gt 0 ]]; then echo good; else echo bad; fi
        ;;
    set-state)
        valid_slot "${2:-}"
        case "${3:-}" in
            good) env_set "BOOT_${2}_LEFT" 3 ;;
            bad)  env_set "BOOT_${2}_LEFT" 0 ;;
            *) echo "unknown state ${3:-}" >&2; exit 1 ;;
        esac
        ;;
    *)
        echo "usage: $0 get-primary|set-primary <slot>|get-state <slot>|set-state <slot> <good|bad>" >&2
        exit 1
        ;;
esac

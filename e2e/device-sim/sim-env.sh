#!/bin/bash
# Shared helpers for the simulated U-Boot environment. The env lives in
# /data/boot/uboot-env as KEY=VALUE lines; all mutations run under flock so
# RAUC's custom backend, fw_setenv and the boot logic never race.

SIM_ENV_FILE="${SIM_ENV_FILE:-/data/boot/uboot-env}"
SIM_ENV_LOCK="${SIM_ENV_LOCK:-/data/boot/.uboot-env.lock}"

env_get() { # <key> — prints value ('' if unset)
    local key="$1"
    [[ -f "$SIM_ENV_FILE" ]] || return 0
    (
        flock -s 9
        sed -n "s/^${key}=//p" "$SIM_ENV_FILE" | head -1
    ) 9>"$SIM_ENV_LOCK"
}

env_set() { # <key> <value>
    local key="$1" value="$2"
    (
        flock -x 9
        touch "$SIM_ENV_FILE"
        {
            grep -v "^${key}=" "$SIM_ENV_FILE" || true
            printf '%s=%s\n' "$key" "$value"
        } > "${SIM_ENV_FILE}.tmp"
        mv "${SIM_ENV_FILE}.tmp" "$SIM_ENV_FILE"
    ) 9>"$SIM_ENV_LOCK"
}

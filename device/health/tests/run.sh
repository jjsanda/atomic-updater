#!/usr/bin/env bash
# atomic-health test suite — plain bash, no frameworks.
#
# Each case runs the binary in a fresh temp dir with tests/fakes/ prepended
# to PATH.  Fakes append "<name> <argv>" lines to $FAKE_LOG and behave per
# FAKE_* env knobs.  Cases run in a subshell so env never leaks between them.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$HERE")"
BIN="$ROOT/atomic-health"
FAKES="$HERE/fakes"
CASES_DIR="$HERE/cases"

make -C "$ROOT" atomic-health >/dev/null || {
    echo "FATAL: build failed" >&2
    exit 1
}
chmod +x "$FAKES"/* 2>/dev/null || true

# Make sure nothing from the calling environment leaks into the cases.
unset ATOMIC_SLOT ATOMIC_APP_UNIT ATOMIC_HEALTH_DIR ATOMIC_PROBE_TIMEOUT \
      ATOMIC_REBOOT_CMD ATOMIC_RAUC_EXTRA_ARGS \
      FAKE_SYSTEMCTL_ACTIVE FAKE_RAUC_EXIT FAKE_FW_SETENV_EXIT \
      FAKE_REBOOT_EXIT 2>/dev/null || true

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

setup() {
    TMP="$(mktemp -d)"
    export FAKE_LOG="$TMP/fake.log"
    : > "$FAKE_LOG"
    PROBES="$TMP/probes"
    mkdir -p "$PROBES"
}

teardown() {
    rm -rf "$TMP"
}

# Run the binary with fakes on PATH; extra env is exported by the case.
run_ah() {
    PATH="$FAKES:$PATH" ATOMIC_HEALTH_DIR="$PROBES" "$BIN" "$@" \
        > "$TMP/stdout" 2> "$TMP/stderr"
}

use_probe() { # <cases/ file> <installed name>
    cp "$CASES_DIR/$1" "$PROBES/$2"
}

assert_exit() { # <expected> <actual>
    if [ "$2" -ne "$1" ]; then
        echo "    expected exit $1, got $2" >&2
        return 1
    fi
}

assert_log_has() { # <extended regex>
    if ! grep -Eq "$1" "$FAKE_LOG"; then
        echo "    FAKE_LOG missing /$1/; log was:" >&2
        sed 's/^/      /' "$FAKE_LOG" >&2
        return 1
    fi
}

assert_log_not() { # <extended regex>
    if grep -Eq "$1" "$FAKE_LOG"; then
        echo "    FAKE_LOG unexpectedly matches /$1/; log was:" >&2
        sed 's/^/      /' "$FAKE_LOG" >&2
        return 1
    fi
}

assert_log_empty() {
    if [ -s "$FAKE_LOG" ]; then
        echo "    expected empty FAKE_LOG; log was:" >&2
        sed 's/^/      /' "$FAKE_LOG" >&2
        return 1
    fi
}

# Exact ordered sequence of action lines (rauc / fw_setenv / logger /
# fakereboot); the systemctl is-active check line is not an action.
assert_actions() { # <line>...
    local expected got
    expected="$(printf '%s\n' "$@")"
    got="$(grep -E '^(rauc|fw_setenv|logger|fakereboot)( |$)' "$FAKE_LOG" || true)"
    if [ "$got" != "$expected" ]; then
        {
            echo "    expected action sequence:"
            printf '      %s\n' "$@"
            echo "    got FAKE_LOG:"
            sed 's/^/      /' "$FAKE_LOG"
        } >&2
        return 1
    fi
}

# ---------------------------------------------------------------------------
# cases
# ---------------------------------------------------------------------------

case_all_pass() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1
    use_probe probe-ok.sh 10-ok.sh
    run_ah
    assert_exit 0 $? || return 1
    assert_actions "rauc status mark-good" "fw_setenv BOOT_A_LEFT 3"
}

case_app_inactive() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=0 ATOMIC_REBOOT_CMD=fakereboot
    use_probe probe-ok.sh 10-ok.sh
    run_ah
    assert_exit 1 $? || return 1
    assert_actions \
        "rauc status mark-bad" \
        "logger -t atomic-health slot A failed health: app unit app.service not active" \
        "fakereboot"
}

case_probe_fail() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1 ATOMIC_REBOOT_CMD=fakereboot
    use_probe probe-exit3.sh 20-fail.sh
    run_ah
    assert_exit 1 $? || return 1
    assert_log_has '^rauc status mark-bad$' || return 1
    assert_log_has '^logger -t atomic-health slot A failed health: probe 20-fail\.sh exit 3$' || return 1
    assert_log_has '^fakereboot$'
}

case_probe_timeout() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1 ATOMIC_REBOOT_CMD=fakereboot
    export ATOMIC_PROBE_TIMEOUT=1
    use_probe probe-sleep5.sh 30-slow.sh
    local start end elapsed rc
    start=$(date +%s)
    run_ah
    rc=$?
    end=$(date +%s)
    elapsed=$((end - start))
    assert_exit 1 "$rc" || return 1
    if [ "$elapsed" -gt 3 ]; then
        echo "    probe not killed in time: took ${elapsed}s (expected <=3)" >&2
        return 1
    fi
    assert_log_has '^rauc status mark-bad$' || return 1
    assert_log_has '^logger -t atomic-health slot A failed health: probe 30-slow\.sh timeout$'
}

case_empty_probe_dir() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1
    run_ah
    assert_exit 0 $? || return 1
    assert_actions "rauc status mark-good" "fw_setenv BOOT_A_LEFT 3"
}

case_no_slot() {
    unset ATOMIC_SLOT 2>/dev/null || true
    export FAKE_SYSTEMCTL_ACTIVE=1
    run_ah
    assert_exit 2 $? || return 1
    assert_log_empty
}

case_dry_run_failing() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1 ATOMIC_REBOOT_CMD=fakereboot
    use_probe probe-exit3.sh 20-fail.sh
    run_ah --dry-run
    assert_exit 1 $? || return 1
    assert_log_not '^(rauc|fw_setenv|fakereboot|logger)( |$)'
}

case_fw_setenv_fails() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1 FAKE_FW_SETENV_EXIT=1
    export ATOMIC_REBOOT_CMD=fakereboot
    run_ah
    assert_exit 1 $? || return 1
    # mark-good ran, fw_setenv was attempted, and nothing else followed
    assert_actions "rauc status mark-good" "fw_setenv BOOT_A_LEFT 3"
}

case_extra_args() {
    export ATOMIC_SLOT=B FAKE_SYSTEMCTL_ACTIVE=1
    export ATOMIC_RAUC_EXTRA_ARGS="--override-boot-slot=B"
    run_ah
    assert_exit 0 $? || return 1
    assert_actions "rauc status mark-good --override-boot-slot=B" \
                   "fw_setenv BOOT_B_LEFT 3"
}

case_slot_flag() {
    export FAKE_SYSTEMCTL_ACTIVE=1
    run_ah --slot=B
    assert_exit 0 $? || return 1
    assert_actions "rauc status mark-good" "fw_setenv BOOT_B_LEFT 3"
}

case_app_unit_disabled() {
    export ATOMIC_SLOT=A ATOMIC_APP_UNIT="" FAKE_SYSTEMCTL_ACTIVE=0
    run_ah
    assert_exit 0 $? || return 1
    assert_log_not '^systemctl is-active' || return 1
    assert_actions "rauc status mark-good" "fw_setenv BOOT_A_LEFT 3"
}

case_probe_order_first_failure_stops() {
    export ATOMIC_SLOT=A FAKE_SYSTEMCTL_ACTIVE=1 ATOMIC_REBOOT_CMD=fakereboot
    use_probe probe-exit3.sh 10-fail.sh
    use_probe probe-ok.sh 20-later.sh
    run_ah
    assert_exit 1 $? || return 1
    # lexicographically first probe fails; reason must name it
    assert_log_has '^logger -t atomic-health slot A failed health: probe 10-fail\.sh exit 3$'
}

# ---------------------------------------------------------------------------
# driver — table of cases
# ---------------------------------------------------------------------------

CASES="
all_pass
app_inactive
probe_fail
probe_timeout
empty_probe_dir
no_slot
dry_run_failing
fw_setenv_fails
extra_args
slot_flag
app_unit_disabled
probe_order_first_failure_stops
"

FAILED=0
for c in $CASES; do
    setup
    if ( "case_$c" ); then
        echo "PASS  $c"
    else
        echo "FAIL  $c"
        FAILED=1
    fi
    teardown
done

if [ "$FAILED" -ne 0 ]; then
    echo "test suite: FAILURES" >&2
    exit 1
fi
echo "test suite: all cases passed"

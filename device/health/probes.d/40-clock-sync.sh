#!/bin/sh
# 40-clock-sync.sh — pass when chrony reports a synchronized system clock.
# Skips (exit 0) on images without chrony so bare-bones builds stay healthy.
set -u

if ! command -v chronyc >/dev/null 2>&1; then
    echo "clock-sync: skipped (chronyc not installed)"
    exit 0
fi

out=$(chronyc tracking 2>&1) || {
    echo "clock-sync: 'chronyc tracking' failed: $out"
    exit 1
}

# A synchronized chronyd reports "Leap status : Normal"; an unsynchronized
# one reports "Not synchronised".
if printf '%s\n' "$out" | grep -qi '^leap status.*normal'; then
    echo "clock-sync: clock synchronized"
    exit 0
fi

echo "clock-sync: clock not synchronized"
exit 1

#!/bin/sh
# 50-http-selfcheck.sh — GET ${ATOMIC_SELFCHECK_URL} and require HTTP 200.
# Skips (exit 0) when the URL is not configured or curl is not installed.
set -u

url="${ATOMIC_SELFCHECK_URL:-}"
if [ -z "$url" ]; then
    echo "http-selfcheck: skipped (ATOMIC_SELFCHECK_URL not set)"
    exit 0
fi

if ! command -v curl >/dev/null 2>&1; then
    echo "http-selfcheck: skipped (curl not installed)"
    exit 0
fi

code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$url") || {
    echo "http-selfcheck: request to $url failed"
    exit 1
}

if [ "$code" = "200" ]; then
    echo "http-selfcheck: $url returned HTTP 200"
    exit 0
fi

echo "http-selfcheck: $url returned HTTP $code (expected 200)"
exit 1

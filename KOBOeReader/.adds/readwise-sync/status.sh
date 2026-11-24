#!/bin/sh
BASE=${READWISE_SYNC_DIR:-/mnt/onboard/.adds/readwise-sync}
FILE="$BASE/last-result.log"
if [ -f "$FILE" ]; then
    # Keep the dialog narrow without requiring the binary to start.
    awk '{ while (length($0) > 32) { print substr($0, 1, 32); $0 = substr($0, 33) } print }' "$FILE"
elif [ -d "$BASE/.lock" ]; then
    printf 'Syncing...\nCheck again shortly.\n'
else
    printf 'Not synced yet.\nChoose Sync to Readwise.\n'
fi

#!/bin/sh
# Explicitly requested backfill action; never run on ordinary highlight sync.
BASE=/mnt/onboard/.adds/readwise-sync
case "$(uname -m)" in
    aarch64*|arm64*) BIN="$BASE/readwise-sync-arm64" ;;
    *) BIN="$BASE/readwise-sync-arm" ;;
esac
chmod +x "$BIN" 2>/dev/null || :
"$BIN" -backfill-chapters >> "$BASE/backfill.log" 2>&1
exit $?

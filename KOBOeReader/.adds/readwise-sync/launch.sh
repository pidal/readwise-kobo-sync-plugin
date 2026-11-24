#!/bin/sh
BASE=${READWISE_SYNC_DIR:-/mnt/onboard/.adds/readwise-sync}

# NickelMenu commands must return within 10 seconds. Let quick syncs finish
# and show their result immediately; leave slow syncs running in background.
if [ "${1:-}" = '--menu' ]; then
    if [ ! -d "$BASE/.lock" ]; then
        /bin/sh "$0" </dev/null >/dev/null 2>&1 &
        SYNC_PID=$!
        CHECKS=0
        while kill -0 "$SYNC_PID" 2>/dev/null && [ "$CHECKS" -lt 5 ]; do
            sleep 1
            CHECKS=$((CHECKS + 1))
        done
    fi
    /bin/sh "$BASE/status.sh"
    exit 0
fi

case "$(uname -m)" in
    aarch64*|arm64*) BIN="$BASE/readwise-sync-arm64" ;;
    *) BIN="$BASE/readwise-sync-arm" ;;
esac
if [ ! -f "$BIN" ]; then
    printf 'Sync failed\nExecutable missing.\nReinstall the plugin.\n' > "$BASE/last-result.log"
    exit 1
fi
chmod +x "$BIN" 2>/dev/null || :
"$BIN" -dir "$BASE" >> "$BASE/run.log" 2>&1
RC=$?
if [ "$RC" -ne 0 ] && [ ! -d "$BASE/.lock" ]; then
    if ! grep -q '^Sync failed' "$BASE/last-result.log" 2>/dev/null; then
        printf 'Sync failed\nCould not finish (exit %s).\nDetails: run.log\n' "$RC" > "$BASE/last-result.log"
    fi
fi
exit "$RC"

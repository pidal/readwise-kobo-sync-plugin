#!/bin/sh
# NickelMenu action: start the prebuilt static binary for this Kobo CPU.
BASE=/mnt/onboard/.adds/readwise-sync
case "$(uname -m)" in
    aarch64*|arm64*) BIN="$BASE/readwise-sync-arm64" ;;
    arm*|*) BIN="$BASE/readwise-sync-arm" ;;
esac
if [ ! -f "$BIN" ]; then
    printf 'Sync could not start: executable missing. Reinstall the plugin.\n' > "$BASE/last-result.log"
    exit 1
fi
chmod +x "$BIN" 2>/dev/null || :
"$BIN" >> "$BASE/run.log" 2>&1
RC=$?
if [ "$RC" -ne 0 ]; then
    # The Go program normally writes a detailed failure status itself.
    # Also handle binary startup failures, such as incompatible firmware.
    if ! grep -q 'Sync failed:' "$BASE/last-result.log" 2>/dev/null; then
        printf 'Readwise sync failed to start/finish (exit %s). See run.log.\n' "$RC" > "$BASE/last-result.log"
    fi
fi
exit "$RC"

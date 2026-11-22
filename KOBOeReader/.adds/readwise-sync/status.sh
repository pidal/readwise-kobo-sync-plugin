#!/bin/sh
FILE=/mnt/onboard/.adds/readwise-sync/last-result.log
if [ -f "$FILE" ]; then
    cat "$FILE"
else
    echo 'Readwise sync has not run yet.'
fi

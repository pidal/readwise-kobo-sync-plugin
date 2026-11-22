# Kobo Readwise Sync (NickelMenu) — v1.0

Send highlights from Kobo's built-in Nickel reader to Readwise, including
sideloaded EPUBs and books that exist in the Kobo database.

**Status: locally tested beta.** The included Linux ARM and ARM64 binaries
are cross-compiled and the code has passed automated local database/API-mock
tests, but has NOT been physically tested on a Kobo device or with a real
Readwise account. Firmware differences may require adjustments.

## Install (no KOReader, Python or curl required on the Kobo)

1. You must already have [NickelMenu](https://pgaskin.net/NickelMenu/) installed.
2. Connect your Kobo by USB. Open this archive, then copy the **contents** of
   its `KOBOeReader` folder into the **root of the mounted KOBOeReader drive**.
   Alternatively, with Python 3 on your COMPUTER run:

       python3 install.py /media/YOU/KOBOeReader

   On Windows, an example is `py install.py E:\` (replace E: with your Kobo drive).
3. Obtain a private access token from <https://readwise.io/access_token>.
4. On the Kobo drive, create the following **plain UTF-8 text file**:

       .adds/readwise-sync/token.txt

   Paste your token as the **only** content (no quotes or `Token ` prefix).
5. Safely eject/unmount the Kobo and allow NickelMenu to load the new config
   (restart the Kobo if the menu items do not show immediately).
6. Enable **Wi-Fi** on the Kobo and connect to a network.
7. Open **NickelMenu > Sync to Readwise** from Home or the reading menu.
   Then use **Readwise sync status** to see how many highlights were sent.
   An initial library upload may take some time. Keep Wi-Fi on until completion.

**Never share the token file.** Kobo's USB-readable partition is not encrypted;
anyone with physical/USB access to your Kobo can read it. Readwise API tokens
provide access to your Readwise account. Keep backups of your highlights.

## Behavior

- Reads `.kobo/KoboReader.sqlite` *read-only* (does not modify it).
- Handles SQLite WAL commits that have not been checkpointed, ordinary/long
  SQLite records, and large tables. Does not require `sqlite3` on Kobo.
- Extracts highlight text, notes, title, author and date when available.
- Skips hidden/deleted highlights and annotation-only records with no highlighted text.
- Sends to `https://readwise.io/api/v2/highlights/` over TLS 1.2+ using a
  bundled CA certificate store and normal OS roots. No other network endpoints.
- Only sends new highlight IDs on subsequent runs. Tracks sent IDs in
  `.adds/readwise-sync/state.json`, which is saved after each accepted batch.
- Splits highlights longer than Readwise's 8,191-character API limit into
  8,000-character parts. Notes over 8,191 characters are truncated and labeled.
- Batches 25 highlights per request, retries transient network errors,
  server errors and rate limits up to 3 attempts.
- Writes a short result to `.adds/readwise-sync/last-result.txt`; detailed
  run messages, without highlight text or token, go to `run.log`.
- If Wi-Fi is off, it fails without advancing sync state. Retry once connected.

## Limitations and compatibility

- **Upload only**: changes/deletions to an already uploaded Kobo highlight or
  note are **not propagated** to Readwise. Readwise may also deduplicate identical
  text/title/author combinations; that is controlled by Readwise.
- It does not export KOReader highlights, handwriting, bookmarks without
  highlighted text, or annotation-only notes.
- No guarantee that Kobo Store's official Readwise sync and this integration
  will produce exactly the same book grouping; back up data before bulk import.
- Very old Kobo firmware, unusual CPU architectures, or future database schema
  changes might be incompatible. The ARM binary is compiled for ARMv5-compatible
  Linux userspace, and ARM64 is supported; both binaries are statically linked.
- Reading Kobo's live database is best effort: if Nickel writes while extraction
  is underway, the program can return an error. Retry. No data is deleted.
- The on-device clock must be approximately correct for HTTPS certificate checks.
- Only use Readwise API tokens you own. No credentials are included in this package.

## Troubleshooting

- **No menu item**: make sure `.adds/nm/readwise-sync` is present. Restart Kobo.
- **Missing token**: create `token.txt` in `.adds/readwise-sync/`.
- **HTTP 401/403**: invalid Readwise token; check account and token file.
- **TLS / certificate error**: verify Kobo date/time and Wi-Fi; see run.log.
- **Exec format error**: send me your Kobo model, firmware version and `uname -m`.
- **Still running**: read `.adds/readwise-sync/last-result.txt` after a moment.
- **Stuck lock**: if the previous process crashed and no sync is running,
  remove the **directory** `.adds/readwise-sync/.lock` (hidden).
- **Want to reupload everything**: save a backup of `state.json` first. Deleting
  it will cause a new full upload; Readwise can deduplicate, but duplicates are
  not guaranteed to be eliminated in all cases.

### Developer commands (on desktop Linux, macOS or Windows with Go)

    cd source
    go test ./...
    go build -o kobo-readwise-sync .
    ./kobo-readwise-sync -db /path/to/KoboReader.sqlite -dir /path/to/config -dry-run

Cross-compile for Kobo:

    GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o readwise-sync-arm .
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o readwise-sync-arm64 .

A separate integration test uses Python 3's built-in sqlite3 for fake databases:

    python3 tests/test_integration.py /path/to/kobo-readwise-sync

## Files installed on the Kobo

    .adds/nm/readwise-sync
    .adds/readwise-sync/launch.sh
    .adds/readwise-sync/status.sh
    .adds/readwise-sync/readwise-sync-arm
    .adds/readwise-sync/readwise-sync-arm64

Your `token.txt`, `state.json`, `last-result.txt`, and `run.log` are created
separately and intentionally not included in the release.

## Uninstall

Remove `.adds/nm/readwise-sync` and `.adds/readwise-sync/` from the Kobo
storage. Back up the latter folder if you want to preserve `state.json`.
This does **not** uninstall NickelMenu or modify Kobo's own database.

## References

- Readwise highlight CREATE endpoint: https://readwise.io/api_deets
- NickelMenu documentation: https://pgaskin.net/NickelMenu/
- Kobo database Bookmark schema reference: https://github.com/valeriangalliat/kobo-highlights-to-calibre

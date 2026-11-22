# Kobo Readwise Sync (NickelMenu) — v1.2

Send highlights from Kobo's built-in Nickel reader to Readwise, including
sideloaded EPUBs and books that exist in the Kobo database.

**Status: locally tested beta.** v1.0 highlight uploading has been reported to
work on a physical Kobo. Cover uploading (v1.1) and chapter metadata/backfill
(v1.2) passed simulated database/API tests but **have not yet been verified
against a live Readwise account/on-device**. Firmware and
book-format differences may require adjustments.

## Install (no KOReader, Python or curl required on the Kobo)

1. You must already have [NickelMenu](https://pgaskin.net/NickelMenu/) installed.
2. Connect your Kobo by USB. Open this archive, then copy the **contents** of
   its `KOBOeReader` folder into the **root of the mounted KOBOeReader drive**.
   Alternatively, with Python 3 on your COMPUTER run:

       python3 install.py /media/YOU/KOBOeReader

   On Windows, an example is `py install.py E:\` (replace E: with your Kobo drive).
   **Upgrading from v1.0/v1.1:** This is safe to copy over the old plugin. Do NOT
   delete `.adds/readwise-sync/token.txt` or `state.json`. Existing highlight
   sync history is retained, and already-uploaded books can gain covers.
3. On your first installation only, obtain a private access token from <https://readwise.io/access_token>.
4. On your first installation only, create this **plain UTF-8 text file**:

       .adds/readwise-sync/token.txt

   Paste your token as the **only** content (no quotes or `Token ` prefix).
5. Safely eject/unmount the Kobo and allow NickelMenu to load the new config
   (restart the Kobo if the menu items do not show immediately).
6. Enable **Wi-Fi** on the Kobo and connect to a network.
7. Open **NickelMenu > Sync to Readwise** from Home or the reading menu.
   Then use **Readwise sync status** to see how many highlights and covers
   were uploaded. No new highlights are required to backfill covers.
   An initial library upload may take some time. Keep Wi-Fi on until completion.

**Never share the token file.** Kobo's USB-readable partition is not encrypted;
anyone with physical/USB access to your Kobo can read it. Readwise API tokens
provide access to your Readwise account. Keep backups of your highlights.

## Chapter metadata and reading positions (new in v1.2)

- For new highlights, the plugin joins `Bookmark.ContentID` to the corresponding
  chapter record in Kobo's `content` table and prepends `Chapter: <title>` to
  the Readwise highlight **note**. The highlight text stays unchanged, and any
  Kobo note appears after the chapter on a new paragraph.
- If the database contains chapter order and start path for **all** highlights
  in a book, the plugin sends a book-order ordinal (`location_type: order`,
  `location: 1, 2, 3...`). It is not a real page number or a position compatible
  with Kindle. Adding new highlights earlier in a book can cause ordinal
  collisions with previously uploaded items; the position is best-effort.
- For existing Readwise highlights uploaded using v1.0/v1.1, select
  **NickelMenu > Backfill Readwise chapters** (Home menu). This is **opt-in**:
  it fetches Readwise's matching book and highlight list and only PATCHes the
  *note* of uniquely matched highlights known in the plugin's `state.json`.
  It preserves existing Readwise notes and skips duplicates/ambiguous books.
  It does **not** reset your sync history, reupload highlights, overwrite
  already marked `Chapter:` notes or change existing highlight locations.
- Actual fixed page numbers for standard EPUB/KEPUB highlights **cannot be
  recovered reliably** from the Kobo database: `ChapterProgress` depends on
  layout/font and often changes. The plugin deliberately does not fabricate
  page numbers or send `location_type: page`.
- Some sideloaded/DRM books do not expose meaningful chapter titles; such
  highlights will remain without a chapter label. Chapters embedded within
  an HTML content file may also need deeper EPUB navigation parsing.

## Behavior

- Reads `.kobo/KoboReader.sqlite` *read-only* (does not modify it).
- Handles SQLite WAL commits that have not been checkpointed, ordinary/long
  SQLite records, and large tables. Does not require `sqlite3` on Kobo.
- Extracts highlight text, notes, title, author, date, chapter title, and book-order position when available.
- Skips hidden/deleted highlights and annotation-only records with no highlighted text.
- Sends to `https://readwise.io/api/v2/highlights/` over TLS 1.2+ using a
  bundled CA certificate store and normal OS roots. No other network endpoints.
- Only sends new highlight IDs on subsequent runs. Tracks sent IDs in
  `.adds/readwise-sync/state.json`, which is saved after each accepted batch.
- Splits highlights longer than Readwise's 8,191-character API limit into
  8,000-character parts. Notes over 8,191 characters are truncated and labeled.
- Batches 25 highlights per request, retries transient network errors,
  server errors and rate limits up to 3 attempts.
- Automatically looks for images embedded in readable local `.epub`/`.kepub.epub`
  files using the EPUB OPF metadata (and reasonable cover-name fallback).
  For other books, tries cached images from `.kobo/images` and `.kobo-images`.
- Requests your existing Readwise Books list, matches exact title + author,
  and uploads a base64-encoded cover using the official Readwise Books PATCH
  endpoint **only if Readwise has its default book icon**. It does not
  overwrite real cover artwork. Ambiguous same-title/author matches are
  skipped instead of risking the wrong book.
- Covers are synced even when no new highlights need uploading. All cover
  bytes are sent only to the authenticated Readwise API over HTTPS.
- Writes a short result to `.adds/readwise-sync/last-result.txt`; detailed
  run messages, without highlight text or token, go to `run.log`.
- If Wi-Fi is off, it fails without advancing sync state. Retry once connected.

## Limitations and compatibility

- **Upload-first**: changes/deletions to an already uploaded Kobo highlight or
  note are **not propagated** to Readwise, except for the manual chapter-only backfill action. Readwise may also deduplicate identical
  text/title/author combinations; that is controlled by Readwise.
- It does not export KOReader highlights, handwriting, bookmarks without
  highlighted text, or annotation-only notes.
- Cover sync is best-effort: DRM-protected files, uncached covers, missing EPUB
  cover metadata, or nonmatching Readwise book titles may be skipped. The
  status screen shows how many covers were uploaded, already present, unmatched,
  or ambiguous. Image sizes are limited to 4 MB each.
- Covers present in Readwise, even if incorrect, are intentionally preserved.
  To correct one, use Readwise **Edit Metadata** manually.
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
- **Covers not uploaded**: verify that the Kobo EPUB has an embedded image or
  Kobo has cached a cover. Confirm that Readwise has the same book title and
  author. If Readwise already has a nondefault cover, it is preserved.
- **Backfill skipped my book**: Kobo may not have a chapter row, or Readwise has
  multiple books with the same title and author. This safety check avoids wrong-book edits.
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
    .adds/readwise-sync/backfill.sh
    .adds/readwise-sync/readwise-sync-arm
    .adds/readwise-sync/readwise-sync-arm64

Your `token.txt`, `state.json`, `last-result.txt`, and `run.log` are created
separately and intentionally not included in the release.

## Uninstall

Remove `.adds/nm/readwise-sync` and `.adds/readwise-sync/` from the Kobo
storage. Back up the latter folder if you want to preserve `state.json`.
This does **not** uninstall NickelMenu or modify Kobo's own database.

## References

- Readwise highlight CREATE, Book LIST, and Book UPDATE endpoints: https://readwise.io/api_deets
- NickelMenu documentation: https://pgaskin.net/NickelMenu/
- Kobo database Bookmark schema reference: https://github.com/valeriangalliat/kobo-highlights-to-calibre

## Git repository (included in this archive)

The ZIP includes a working `.git` directory at its **top level**, with the
`main` branch and version tags `v1.0`, `v1.1`, and `v1.2`. The three commits
are **reconstructed from the release ZIP snapshots**; they are not claimed
to be the project's original step-by-step development history.

After extracting the archive to a regular folder, run:

```sh
git log --oneline --decorate
git status
git diff v1.1..v1.2 -- source/
```

Do **not** copy the `.git` folder to your Kobo. Install only the contents of
`KOBOeReader/` as described above. The `.gitignore` excludes the private
Readwise token and sync state files from accidental `git add`.

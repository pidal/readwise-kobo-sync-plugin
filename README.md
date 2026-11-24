# Kobo Readwise Sync

Send new highlights and their personal notes from Kobo's built-in reader to Readwise over
Wi-Fi. Works with sideloaded EPUBs and books in Kobo's library. Requires
[NickelMenu](https://pgaskin.net/NickelMenu/).

## Install

1. Install NickelMenu on your Kobo.
2. Download and extract the release ZIP on your computer.
3. Connect Kobo by USB and run this command from the extracted folder:

   ```sh
   python3 install.py /path/to/KOBOeReader
   ```

   On macOS the drive is usually `/Volumes/KOBOeReader`. On Windows use
   `py install.py E:\`, replacing `E:` with the Kobo drive letter.
4. Paste your [Readwise access token](https://readwise.io/access_token) when
   prompted. Input is hidden.
5. Safely eject and restart Kobo, then connect to Wi-Fi.
6. Choose **NickelMenu → Sync to Readwise**.

The installer copies the program files, creates its data folder and excludes
`.adds` from Kobo's book scan. Existing scan rules are retained; the original
Kobo settings file is backed up before changing it. Repeating installation
preserves your token and sync history.

## Use

Highlight and annotate normally in Kobo's reader, then choose **Sync to Readwise**.
The action is available from Home, the reader and My Books.

Quick syncs show the result directly. Longer syncs continue in the background;
choose **Readwise status** to see a fresh progress snapshot. An open status dialog
does not refresh automatically. Keep Wi-Fi connected until syncing finishes.

```text
Sync complete
New: 12
Already synced: 85
Needs review: 0
Covers added: 2
Covers present: 8
Covers skipped: 0
```

## What syncs

- New highlights, personal notes, book title/author and highlight dates.
- Each highlight is uploaded once. Later changes to its text, personal note or
  location are not sent again. Existing Readwise highlights are never PATCHed.
- Initial reading order in Readwise's native location field when Kobo provides
  enough chapter/path data. These are highlight-order numbers, not printed
  pages or Kindle locations. Existing positions are not renumbered; later
  highlights can share an order number when earlier passages are highlighted.
- Missing book covers from readable EPUB/KEPUB files or Kobo's image cache.
  Covers are checked only for books receiving new highlights. Existing Readwise
  artwork is preserved; a cover error does not fail saved highlight uploads.

**Notes contain only your personal annotations.** No chapter names or Kobo
position labels are added to them. Nothing is imported from Readwise to Kobo,
and Kobo's database is never modified.

## Your data

All paths below are inside `.adds/readwise-sync/` on Kobo:

| File | Purpose |
| --- | --- |
| `data/token.conf` | Private Readwise token, stored as plain text |
| `data/sync-state.json` | Sync history, Readwise IDs and upload recovery data |
| `data/sync-review.log` | Items that could not be matched safely |
| `last-result.log` | Latest status |
| `run.log` | Detailed diagnostics |

Keep `data/` to retain credentials and sync history. The release never includes
personal files. State contains highlight text and notes; keep backups private. Confirmed IDs are skipped before any API matching; a run
with no new highlights makes no network requests.
Kobo's USB storage is not encrypted. All API requests use authenticated HTTPS.

## Limits

- Requires working Wi-Fi, a valid Readwise account/token, and a correct Kobo clock.
- KOReader highlights, handwriting and notes without highlighted text are not exported.
- Removed or edited Kobo highlights remain unchanged in Readwise.
- Identical passages and ambiguous matches are listed for review and skipped,
  without failing the whole run. Existing exact matches are recorded locally
  without changing them.
- Missing chapter/path data can prevent native reading-order locations. Genuine
  page/offset locations on existing Readwise highlights are preserved.
- Long highlights are split into 8,000-character parts; notes are bounded to the
  API limit. A changed book title/author does not move existing highlights to a
  different Readwise book.
- Reading the live Kobo database is best effort. Retry if Kobo was writing to it.
  Accepted uploads are tracked, and interrupted requests are reconciled on retry.

**Validation:** automated tests cover database extraction, new uploads, duplicate prevention,
recovery, clean installation and compact status. Physical-device and live-account
behavior can vary by firmware and book format.

## Troubleshooting

For a failure, follow the short status suggestion and inspect `run.log` if needed.
For items needing review, inspect `data/sync-review.log`. Do not delete sync history
to retry an upload. If a crash leaves a `.lock` directory, remove it only after
confirming no sync is running.

## Development

Requires Go and Python 3 on the computer; neither is needed on Kobo.

```sh
cd source
go test ./...
go build -o /tmp/kobo-readwise-sync .
cd ..
python3 tests/test_integration.py /tmp/kobo-readwise-sync
python3 -m unittest discover -s tests -p 'test_*.py'
```

`source/build.sh` tests and builds static Linux ARMv5 and ARM64 binaries into
`KOBOeReader/`. Use Go 1.23.12 for older Kobo kernels; set `GO` to select the
compiler. Newer Go toolchains may require newer Linux kernels.

After building, run `python3 source/package.py` from the repository root to
create `releases/kobo-readwise-sync.zip`. The ZIP contains only the installer,
instructions, license and device files. Development source and tests stay in
the repository.

## Uninstall

Back up `data/` if needed, then remove `.adds/nm/readwise-sync` and
`.adds/readwise-sync/`. This leaves your Kobo books and Readwise highlights intact.
The `.adds` scan exclusion remains because other applications may use it.

[Readwise API](https://readwise.io/api_deets) ·
[NickelMenu](https://pgaskin.net/NickelMenu/) ·
[License](LICENSE)

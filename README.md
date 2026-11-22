# Kobo Readwise Sync (NickelMenu) — v1.3

Sync highlights **one way, Kobo → Readwise**, directly over Wi-Fi, including
sideloaded EPUBs. Kobo's database is read-only. Requires NickelMenu.

**Status: locally tested beta.** Basic uploads were reported working on a
physical Kobo in v1.0. v1.3's ID migration, updates, locations and cover uploads
have automated local coverage but still need verification on a real Kobo and
Readwise account. No live credentials are bundled or needed for the tests.

## Install or upgrade

Connect Kobo by USB and run on your computer:

```sh
python3 install.py /path/to/KOBOeReader
```

On Windows: `py install.py E:\` (replace E: with your drive).

The installer:

- Copies only an explicit list of plugin executables, launch scripts and menu config.
- Backs up existing personal data to `.adds/readwise-sync-backups/upgrade-*/`.
- Moves old `token.txt` and `state.json` to the new names below, retaining history.
- Stops if old and new personal files conflict, rather than choosing a copy.
- Adds `.adds` to Kobo's `ExcludeSyncFolders` setting while retaining existing rules.
  The original `.kobo/Kobo/Kobo eReader.conf` is backed up next to it with
  `.readwise-sync.bak` appended. It does not edit Kobo's SQLite database.

For a **first installation**, obtain your private token at
<https://readwise.io/access_token> and save it in this plain UTF-8 file:

```text
.adds/readwise-sync/data/token.conf
```

Put only the token in it: no quotes or `Token ` prefix. `.conf` is the filename
extension; its contents are still plain text.

Safely eject, restart Kobo, connect Wi-Fi, then use **NickelMenu → Sync to Readwise**.
Use **Readwise sync status** to see results.

## Personal files: KEEP THESE WHEN UPGRADING

| Path on Kobo | Purpose |
| --- | --- |
| `.adds/readwise-sync/data/token.conf` | Private Readwise access token |
| `.adds/readwise-sync/data/sync-state.json` | Upload history, Readwise book/highlight IDs, content fingerprints and interrupted-upload recovery data |
| `.adds/readwise-sync/data/sync-state.json.v1.bak` | Original v1 history before its first schema upgrade, when applicable |
| `.adds/readwise-sync/data/sync-review.log` | Hashed local IDs needing review and the reason; no token/highlight text |
| `.adds/readwise-sync-backups/` | Installer's personal-data backups from previous upgrades |

**The entire `data/` folder is personal data.** It is excluded from Git and is
never included in the release payload or copied over by the installer. Back it
up before upgrades. Recovery state includes highlight text and notes; keep it
private along with your token. Kobo's USB storage is not encrypted.

The code lives alongside it in `.adds/readwise-sync/`: launch scripts and ARM
executables can be replaced. `last-result.log`, `run.log` and `backfill.log` are
runtime diagnostics, not sync history. Do not delete the history to fix a
network error: retries preserve progress.

Manual-copy installation is supported: copy only the **contents** of
`KOBOeReader/` onto the mounted drive, retaining `data/` and old personal files.
The next sync migrates legacy names automatically. For the book-scanning fix,
use the installer or merge the following into the existing `[FeatureSettings]`
section of `.kobo/Kobo/Kobo eReader.conf` (retain any existing exclusion rule):

```ini
[FeatureSettings]
ExcludeSyncFolders=\\.adds(?:/|$)
```

## Files showing up as books

Some Kobo firmware scans hidden folders, including `.adds`. A `.txt` file there
can therefore become a book. v1.3 uses `token.conf` and `last-result.log`, and the
installer excludes `.adds` from scanning. It preserves other exclusion rules.

Already imported library entries may remain after exclusion. First finish the
upgrade, confirm the token/history are in `data/`, and back up that folder.
Then remove stale plugin text-file entries through Kobo's library UI. Do not
remove real books or delete the plugin folder. The installer does not directly
clean Kobo's database. Kobo firmware upgrades may reset the exclusion setting;
rerunning the installer restores it.

## Locations

Each highlight includes the information Kobo actually exposes:

- A native Readwise numeric **order** location when all chapter indices and
  start paths for that book's current highlights are available.
- A **Kobo location** line in the first highlight part's note: chapter index
  (Kobo's raw index), start-container path/CFI/span, and character offset, when
  present. Chapter title and the personal note are also retained.

These are reading positions, **not printed page numbers or Kindle locations**.
The numeric order is the rank among the book's current highlights, not a fixed
coordinate in the whole book. Adding an earlier highlight can change ranks;
v1.3 PATCHes changed ranks on previously mapped highlights in the same sync.
After an interrupted run, retry to finish those updates.

For highlights adopted from an existing Readwise book, genuine page/offset
location types are preserved. Readwise documents changing the numeric location
but not its type with PATCH. If Kobo later lacks sufficient ordering data, the
previous native number is retained; available Kobo path details remain in the
note. The plugin does not invent missing page numbers. Books without chapter
rows or paths may have incomplete location metadata.

## One-way sync and recovery

- Uploads new highlights; records the returned Readwise highlight and book IDs.
  Each CREATE contains one highlight so an ID is never inferred from array order.
- Updates an already mapped highlight's text, note and available order when
  Kobo changes. Kobo is authoritative for those fields on update. Clearing a
  Kobo note clears its personal-note portion in Readwise. Readwise-only note
  edits can be overwritten by a later Kobo update; nothing is imported to Kobo.
- Keeps fingerprints to skip unchanged highlights. It does not continuously
  enforce Kobo content against edits made only in Readwise.
- Saves upload intent **before** sending, then saves IDs and fingerprints after
  acceptance. A subsequent run reconciles an interrupted upload before creating
  another item, including when Kobo's text changed in the meantime.
- Writes state using a flushed temporary file and atomic replacement. Corrupt
  or conflicting history fails closed instead of triggering a full upload.
- Reconciles v1.0–v1.2 history by unique title/author and unique highlight text.
  Changed legacy text cannot always be matched: those entries need review.
  Multiple matching Readwise books/highlights cause the run to stop without
  guessing which object to modify.
- Identical local passages in the same title/author grouping are reported for
  review because Readwise deduplicates them and cannot reliably preserve both
  independent Kobo notes. Unresolved entries are counted in status and listed
  in `data/sync-review.log` after a completed run.
- Removed/hidden Kobo highlights and obsolete long-text parts **are not deleted
  from Readwise**. They are retained and reported for review. Book title/author
  regrouping is not propagated to existing mapped Readwise objects.
- Retries transient network errors, rate limits and server failures, up to three
  attempts; paces API requests including list calls across different books.
  No arbitrary lock expiry: a long-running sync keeps its lock.

A first migration can take longer because it reads existing Readwise highlights.
No offline queue is needed for ordinary unsent highlights: they remain in Kobo's
DB and are picked up next time. This is not an exactly-once transaction across
Kobo storage and Readwise; recovery uses saved intent and conservative matching.

## Other behavior

- Reads `.kobo/KoboReader.sqlite`, including committed SQLite WAL frames,
  overflow records and large tables; validates WAL header/frame checksums.
- Detects database/WAL changes across extraction and refuses active rollback
  journals. It does not hold a SQLite snapshot lock: live reads remain best
  effort. Retry if Nickel was writing during extraction.
- Skips hidden/deleted and empty-text records. Does not export KOReader highlights,
  handwriting, bookmarks without selected text, or annotation-only records.
- Preserves highlight timestamps when available. Splits text into 8,000-character
  parts and bounds notes to Readwise's 8,191-character limit.
- Extracts embedded EPUB/KEPUB covers, with Kobo image-cache fallback. Uploads
  only missing/default Readwise covers; preserves existing artwork. Requires an
  unambiguous title/author match. Images are limited to 4 MB.
- HTTPS uses TLS 1.2+ and OS/bundled CA certificates. All network operations go
  to the authenticated Readwise API. Kobo's clock must be approximately correct.
- **Backfill Readwise chapters** remains an optional menu action that adds
  chapters to uniquely matched previously uploaded highlights while preserving
  existing notes. Normal v1.3 sync already updates mapped highlights' metadata.

## Troubleshooting

- **Missing token:** create `data/token.conf`, or retain legacy `token.txt` for migration.
- **HTTP 401/403:** check the Readwise token and account.
- **TLS error:** check Kobo's date/time, Wi-Fi and `run.log`.
- **Need review:** inspect `data/sync-review.log`; do not reset history.
- **Sync already running:** wait. After a crash/restart, and only when no sync is
  running, remove the empty `.adds/readwise-sync/.lock` directory.
- **Cover failure after uploads:** highlight progress is saved; retry.
- **Missing chapters:** the book may not expose chapter rows in Kobo's database;
  EPUB navigation parsing is not implemented.
- **Stale/missing Readwise ID:** the run stops if a mapped remote highlight was
  deleted. Restore/reconcile that item rather than deleting all sync history.

## Development and validation

```sh
cd source
go test ./...
go build -o /tmp/kobo-readwise-sync .
cd ..
python3 tests/test_integration.py /tmp/kobo-readwise-sync
python3 -m unittest discover -s tests -p 'test_install.py'
```

Cross-compile with `source/build.sh`; it tests first and creates both static
Linux ARMv5 and ARM64 binaries in the payload. For older Kobo kernels use
Go 1.23.12; newer Go toolchains can require newer Linux kernels. Set `GO` to a
specific Go executable if needed. Neither Go nor Python is needed on the Kobo.

The repository contains reconstructed v1.0/v1.1/v1.2 release commits and tags.
They were reconstructed from release ZIP snapshots, not original development
history. Keep `.git` on your computer; do not copy it to Kobo.

## Uninstall

Back up `data/` first. Remove `.adds/nm/readwise-sync` and
`.adds/readwise-sync/`. Personal backups in `.adds/readwise-sync-backups/` are
separate. This does not remove Readwise highlights or uninstall NickelMenu.
The scan exclusion is retained, since other `.adds` applications may need it.

## References

- [Readwise API](https://readwise.io/api_deets): CREATE IDs, deduplication, UPDATE and rate limits.
- [NickelMenu](https://pgaskin.net/NickelMenu/).
- [KFMon installer](https://github.com/NiLuJe/kfmon/blob/master/tools/install.sh): Kobo scan-exclusion setting and Qt INI escaping.
- [SQLite file format](https://www.sqlite.org/fileformat2.html): WAL checksum algorithm.

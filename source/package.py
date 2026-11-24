#!/usr/bin/env python3
"""Build the clean-install ZIP after running source/build.sh."""
from pathlib import Path
import sys
import zipfile

ROOT = Path(__file__).resolve().parent.parent
FILES = [
    'README.md', 'LICENSE', 'install.py',
    'KOBOeReader/.adds/nm/readwise-sync',
    'KOBOeReader/.adds/readwise-sync/launch.sh',
    'KOBOeReader/.adds/readwise-sync/status.sh',
    'KOBOeReader/.adds/readwise-sync/readwise-sync-arm',
    'KOBOeReader/.adds/readwise-sync/readwise-sync-arm64',
]


def package():
    for name in FILES:
        if not (ROOT / name).is_file():
            raise ValueError(f'Missing {name}; run source/build.sh first.')
    output = ROOT / 'releases' / 'kobo-readwise-sync.zip'
    output.parent.mkdir(exist_ok=True)
    with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name in FILES:
            archive.write(ROOT / name, name)
    with zipfile.ZipFile(output) as archive:
        if archive.testzip() is not None:
            raise ValueError('Release archive integrity check failed.')
    print(output)


if __name__ == '__main__':
    try:
        package()
    except (ValueError, OSError) as exc:
        print(f'ERROR: {exc}', file=sys.stderr)
        raise SystemExit(1)

#!/usr/bin/env python3
"""Install Kobo Readwise Sync onto a mounted Kobo, preserving user data.

Usage: python3 install.py /path/to/KOBOeReader
"""
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PAYLOAD = ROOT / 'KOBOeReader' / '.adds'


def main():
    if len(sys.argv) != 2 or sys.argv[1] in ('-h', '--help'):
        print('Usage: python3 install.py /path/to/KOBOeReader')
        print('Example (Windows): py install.py E:\\')
        return 0 if len(sys.argv) == 2 else 2
    device = Path(sys.argv[1]).expanduser().resolve()
    if not (device / '.kobo' / 'KoboReader.sqlite').is_file():
        print(f'ERROR: {device} does not look like a mounted Kobo drive.')
        return 1
    if not PAYLOAD.is_dir():
        print(f'ERROR: bundled files are missing at {PAYLOAD}')
        return 1
    for src in PAYLOAD.rglob('*'):
        if not src.is_file():
            continue
        relative = src.relative_to(PAYLOAD)
        dst = device / '.adds' / relative
        dst.parent.mkdir(parents=True, exist_ok=True)
        # Do not overwrite personal state/token files if later added.
        if src.name in ('token.txt', 'state.json', 'last-result.txt') and dst.exists():
            continue
        shutil.copy2(src, dst)
        print('Installed', dst.relative_to(device))
    print('\nInstalled or upgraded. Existing token.txt and state.json were preserved.')
    print('On first install only: create .adds/readwise-sync/token.txt.')
    print('Put ONLY your token from https://readwise.io/access_token in that file.')
    print('Safely eject the Kobo, turn on Wi-Fi, and use NickelMenu > Sync to Readwise.')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

#!/usr/bin/env python3
"""Install Kobo Readwise Sync onto a mounted Kobo."""
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PAYLOAD = ROOT / 'KOBOeReader' / '.adds'
# Install program files only; credentials and history are kept separately.
PLUGIN_FILES = ('launch.sh', 'status.sh',
                'readwise-sync-arm', 'readwise-sync-arm64')


def exclude_plugin(device):
    """Merge a targeted exclusion into Qt's INI format; preserve other rules."""
    config = device / '.kobo' / 'Kobo' / 'Kobo eReader.conf'
    config.parent.mkdir(parents=True, exist_ok=True)
    text = config.read_text(encoding='utf-8') if config.exists() else ''
    # Qt INI strings escape a backslash as two backslashes.
    rule = r'\\.adds(?:/|$)'
    section = None
    lines = text.splitlines(keepends=True)
    found_section = None
    matches = []
    for i, line in enumerate(lines):
        m = re.match(r'\s*\[([^]]+)\]', line)
        if m:
            section = m.group(1)
            if section == 'FeatureSettings':
                found_section = i
        if section == 'FeatureSettings' and re.match(r'\s*ExcludeSyncFolders\s*=', line):
            matches.append(i)
    if matches:
        # Qt uses the last key when duplicates exist.
        i = matches[-1]
        old = lines[i].split('=', 1)[1].strip().strip('"')
        if rule in old:
            return
        merged = f'(?:{old})|(?:{rule})' if old else rule
        lines[i] = f'ExcludeSyncFolders={merged}\n'
    elif found_section is not None:
        lines.insert(found_section + 1, f'ExcludeSyncFolders={rule}\n')
    else:
        if lines and not lines[-1].endswith('\n'):
            lines[-1] += '\n'
        lines.extend(['\n[FeatureSettings]\n', f'ExcludeSyncFolders={rule}\n'])
    if config.exists():
        backup = config.with_name(config.name + '.readwise-sync.bak')
        if not backup.exists():
            shutil.copy2(config, backup)
    tmp = config.with_name(config.name + '.readwise-sync.tmp')
    tmp.write_text(''.join(lines), encoding='utf-8')
    tmp.replace(config)


def install(device):
    if not (device / '.kobo' / 'KoboReader.sqlite').is_file():
        raise ValueError(f'{device} does not look like a mounted Kobo drive.')
    sources = [(PAYLOAD / 'nm' / 'readwise-sync', Path('nm/readwise-sync'))]
    sources += [(PAYLOAD / 'readwise-sync' / name, Path('readwise-sync') / name)
                for name in PLUGIN_FILES]
    for src, _ in sources:
        if not src.is_file():
            raise ValueError(f'Bundled file missing: {src}. Build release binaries first.')
    plugin = device / '.adds' / 'readwise-sync'
    (plugin / 'data').mkdir(parents=True, exist_ok=True)
    exclude_plugin(device)
    for src, relative in sources:
        dst = device / '.adds' / relative
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
        print('Installed', dst.relative_to(device))
    print('Installed Kobo Readwise Sync.')
    return plugin / 'data' / 'token.conf'


def main():
    if len(sys.argv) != 2 or sys.argv[1] in ('-h', '--help'):
        print('Usage: python3 install.py /path/to/KOBOeReader')
        return 0 if len(sys.argv) == 2 else 2
    try:
        token_path = install(Path(sys.argv[1]).expanduser().resolve())
        if not token_path.exists():
            from getpass import getpass
            print('Get your token from https://readwise.io/access_token')
            token = getpass('Readwise token (input hidden): ').strip()
            if not token or any(c.isspace() for c in token) or 'paste' in token.lower():
                raise ValueError('Enter only your Readwise token, without spaces.')
            # Exclusive creation also protects an existing token on repeated installs.
            with token_path.open('x', encoding='utf-8') as out:
                out.write(token + '\n')
            token_path.chmod(0o600)
        print('Safely eject and restart Kobo, then connect Wi-Fi.')
        print('Choose NickelMenu > Sync to Readwise.')
    except (ValueError, OSError, EOFError, KeyboardInterrupt) as exc:
        print(f'ERROR: {exc}')
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

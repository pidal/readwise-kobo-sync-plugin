#!/usr/bin/env python3
"""Install/upgrade a mounted Kobo, preserving personal data and settings."""
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PAYLOAD = ROOT / 'KOBOeReader' / '.adds'
# Explicit allowlist: personal data can never accidentally enter an upgrade.
PLUGIN_FILES = ('launch.sh', 'status.sh', 'backfill.sh',
                'readwise-sync-arm', 'readwise-sync-arm64')


def migrate_data(plugin):
    data = plugin / 'data'
    data.mkdir(parents=True, exist_ok=True)
    pairs = [('token.txt', 'token.conf'), ('state.json', 'sync-state.json')]
    # Validate all conflicts before moving anything.
    for old_name, new_name in pairs:
        old, new = plugin / old_name, data / new_name
        if old.exists() and new.exists() and old.read_bytes() != new.read_bytes():
            raise ValueError(f'{old} and {new} differ. Back up and resolve before upgrading.')
    for old_name, new_name in pairs:
        old, new = plugin / old_name, data / new_name
        if old.exists():
            if new.exists():
                old.unlink()  # identical duplicate only
            else:
                old.rename(new)
    old_status = plugin / 'last-result.txt'
    if old_status.exists():
        new_status = plugin / 'last-result.log'
        if not new_status.exists():
            old_status.rename(new_status)
        else:
            old_status.rename(data / 'legacy-last-result.log')


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
    # Back up all personal files before migration; backups are outside payload.
    if plugin.exists():
        backup_root = device / '.adds' / 'readwise-sync-backups'
        backup_root.mkdir(parents=True, exist_ok=True)
        import tempfile
        backup = Path(tempfile.mkdtemp(prefix='upgrade-', dir=backup_root))
        for name in ('data', 'token.txt', 'state.json', 'last-result.txt'):
            src = plugin / name
            if src.is_dir():
                shutil.copytree(src, backup / name)
            elif src.is_file():
                shutil.copy2(src, backup / name)
        # Old token backups must not become books either.
        if (backup / 'token.txt').exists():
            (backup / 'token.txt').rename(backup / 'token.conf')
        if (backup / 'last-result.txt').exists():
            (backup / 'last-result.txt').rename(backup / 'last-result.log')
        print('Personal data backed up:', backup.relative_to(device))
    migrate_data(plugin)
    exclude_plugin(device)
    for src, relative in sources:
        dst = device / '.adds' / relative
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
        print('Installed', dst.relative_to(device))
    print('\nInstalled. KEEP .adds/readwise-sync/data/ when upgrading:')
    print('  token.conf       = private Readwise token (plain UTF-8, token only)')
    print('  sync-state.json  = sync history, Readwise IDs and recovery data')
    print('The data/ folder is never shipped or overwritten by the installer.')
    print('Plugin folder excluded from Kobo book scanning; original settings backed up.')
    print('Safely eject, restart Kobo, enable Wi-Fi, then use Sync to Readwise.')


def main():
    if len(sys.argv) != 2 or sys.argv[1] in ('-h', '--help'):
        print('Usage: python3 install.py /path/to/KOBOeReader')
        return 0 if len(sys.argv) == 2 else 2
    try:
        install(Path(sys.argv[1]).expanduser().resolve())
    except (ValueError, OSError) as exc:
        print(f'ERROR: {exc}')
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

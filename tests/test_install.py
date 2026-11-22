import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('installer', Path(__file__).resolve().parents[1] / 'install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallTests(unittest.TestCase):
    def test_migration_and_conflict(self):
        with tempfile.TemporaryDirectory() as tmp:
            plugin = Path(tmp)
            (plugin / 'token.txt').write_text('private-token')
            (plugin / 'state.json').write_text('{"version":1,"uploaded":{"x":true}}')
            installer.migrate_data(plugin)
            installer.migrate_data(plugin)
            self.assertFalse((plugin / 'token.txt').exists())
            self.assertEqual((plugin / 'data/token.conf').read_text(), 'private-token')
            self.assertIn('"x":true', (plugin / 'data/sync-state.json').read_text())
            (plugin / 'token.txt').write_text('other-token')
            with self.assertRaises(ValueError):
                installer.migrate_data(plugin)
            self.assertEqual((plugin / 'token.txt').read_text(), 'other-token')
            self.assertEqual((plugin / 'data/token.conf').read_text(), 'private-token')

    def test_config_preserves_rules_and_is_idempotent(self):
        for original in ('[Other]\nKeep=yes\n',
                         '[FeatureSettings]\nExcludeSyncFolders=old-rule\n[Other]\nKeep=yes\n',
                         '[FeatureSettings]\nKey=value\n[Other]\nKeep=yes\n'):
            with self.subTest(original=original), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                config = root / '.kobo/Kobo/Kobo eReader.conf'
                config.parent.mkdir(parents=True)
                config.write_text(original)
                installer.exclude_plugin(root)
                updated = config.read_text()
                installer.exclude_plugin(root)
                self.assertEqual(config.read_text(), updated)
                self.assertIn('Keep=yes', updated)
                self.assertIn(r'\\.adds(?:/|$)', updated)
                if 'old-rule' in original:
                    self.assertIn('old-rule', updated)
                self.assertEqual(config.with_name(config.name + '.readwise-sync.bak').read_text(), original)

    def test_upgrade_never_copies_personal_payload(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            device = root / 'device'
            (device / '.kobo').mkdir(parents=True)
            (device / '.kobo/KoboReader.sqlite').touch()
            payload = root / 'payload'
            (payload / 'nm').mkdir(parents=True)
            (payload / 'nm/readwise-sync').write_text('menu')
            source = payload / 'readwise-sync'
            source.mkdir()
            for name in installer.PLUGIN_FILES:
                (source / name).write_text('new-code')
            (source / 'data').mkdir()
            (source / 'data/token.conf').write_text('DO-NOT-COPY')
            plugin = device / '.adds/readwise-sync'
            plugin.mkdir(parents=True)
            (plugin / 'token.txt').write_text('my-token')
            (plugin / 'state.json').write_text('my-history')
            old_payload = installer.PAYLOAD
            try:
                installer.PAYLOAD = payload
                installer.install(device)
                installer.install(device)
            finally:
                installer.PAYLOAD = old_payload
            self.assertEqual((plugin / 'data/token.conf').read_text(), 'my-token')
            self.assertEqual((plugin / 'data/sync-state.json').read_text(), 'my-history')
            self.assertTrue(list((device / '.adds/readwise-sync-backups').glob('upgrade-*/state.json')))


if __name__ == '__main__':
    unittest.main()

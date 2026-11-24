import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from contextlib import redirect_stdout
from io import StringIO

spec = importlib.util.spec_from_file_location('installer', Path(__file__).resolve().parents[1] / 'install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallTests(unittest.TestCase):
    def test_setup_prompts_once_without_printing_token(self):
        with tempfile.TemporaryDirectory() as tmp:
            token_path = Path(tmp) / 'token.conf'
            output = StringIO()
            with patch.object(installer, 'install', return_value=token_path), \
                 patch.object(installer.sys, 'argv', ['install.py', tmp]), \
                 patch('getpass.getpass', return_value='private-test-token') as prompt, \
                 redirect_stdout(output):
                self.assertEqual(installer.main(), 0)
                self.assertEqual(installer.main(), 0)
                prompt.assert_called_once()
            self.assertEqual(token_path.read_text().strip(), 'private-test-token')
            self.assertNotIn('private-test-token', output.getvalue())

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

    def test_clean_install_and_repeat_preserve_personal_data(self):
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

            old_payload = installer.PAYLOAD
            try:
                installer.PAYLOAD = payload
                token_path = installer.install(device)
                self.assertTrue(token_path.parent.is_dir())
                self.assertFalse(token_path.exists())
                token_path.write_text('my-token')
                (token_path.parent / 'sync-state.json').write_text('my-history')
                installer.install(device)
            finally:
                installer.PAYLOAD = old_payload
            self.assertEqual((plugin / 'data/token.conf').read_text(), 'my-token')
            self.assertEqual((plugin / 'data/sync-state.json').read_text(), 'my-history')
            self.assertFalse((device / '.adds/readwise-sync-backups').exists())


if __name__ == '__main__':
    unittest.main()

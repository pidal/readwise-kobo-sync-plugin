from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parents[1] / 'KOBOeReader/.adds/readwise-sync'

class StatusTests(unittest.TestCase):
    def setup_plugin(self, root):
        for name in ('launch.sh', 'status.sh'):
            shutil.copy2(SCRIPTS / name, root / name)
        for arch in ('arm', 'arm64'):
            binary = root / f'readwise-sync-{arch}'
            binary.write_text('#!/bin/sh\nprintf "Sync complete\\nNew: 2  Updated: 1\\n" > "$READWISE_SYNC_DIR/last-result.log"\n')
            binary.chmod(0o755)
        return {**os.environ, 'READWISE_SYNC_DIR': str(root)}

    def test_quick_sync_shows_result(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            env = self.setup_plugin(root)
            result = subprocess.run(['sh', str(root / 'launch.sh'), '--menu'], env=env,
                                    capture_output=True, text=True, timeout=8)
            self.assertEqual(result.returncode, 0)
            self.assertIn('Sync complete', result.stdout)
            self.assertIn('New: 2', result.stdout)

    def test_running_sync_not_started_twice_and_text_wraps(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            env = self.setup_plugin(root)
            (root / '.lock').mkdir()
            (root / 'last-result.log').write_text('Syncing...\n' + 'x' * 120)
            result = subprocess.run(['sh', str(root / 'launch.sh'), '--menu'], env=env,
                                    capture_output=True, text=True, timeout=3)
            self.assertIn('Syncing...', result.stdout)
            self.assertNotIn('Sync complete', result.stdout)
            self.assertTrue(all(len(line) <= 32 for line in result.stdout.splitlines()))

if __name__ == '__main__':
    unittest.main()

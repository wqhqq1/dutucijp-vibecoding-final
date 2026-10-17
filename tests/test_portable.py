"""Lightweight portable startup boundaries; no official data or accounts created."""
import contextlib
import hashlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'portable'))
import launcher


class PortableTests(unittest.TestCase):
    def test_open_account_uses_page_without_reading_links_or_renewing(self):
        with patch.object(launcher, 'run_logged') as run, patch.object(launcher.webbrowser, 'open') as opened:
            launcher.open_account(Path('nonexistent-state'), 'warehouse', 18080, True)
            opened.assert_called_once_with('http://localhost:18080')
            run.assert_not_called()
            opened.reset_mock()
            launcher.open_account(Path('nonexistent-state'), 'admin', 18080, False)
            opened.assert_not_called()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='portable-中文-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = self.root / '独立状态'
        self.state.mkdir()

    def test_first_preparation_requires_eight_gib_before_any_download(self):
        with patch.object(launcher.shutil, 'disk_usage', return_value=SimpleNamespace(free=8*1024**3-1)), patch.object(launcher, 'run_logged') as run:
            with self.assertRaisesRegex(RuntimeError, '8 GiB'):
                launcher.prepare(self.state, 'all')
            run.assert_not_called()

    def test_partial_database_is_retained_and_rejected(self):
        db = launcher.paths(self.state)[2]
        db.parent.mkdir(parents=True)
        db.write_bytes(b'incomplete fixture, not a business database')
        with self.assertRaisesRegex(RuntimeError, 'Incomplete prior preparation'):
            launcher.prepare(self.state, 'all')
        self.assertEqual(db.read_bytes(), b'incomplete fixture, not a business database')

    def test_repeat_preparation_retains_completed_state(self):
        db = launcher.paths(self.state)[2]
        db.parent.mkdir(parents=True)
        db.write_bytes(b'existing independent state')
        launcher.atomic_json(self.state/'ready.json', {'scope': 'all'})
        with patch.object(launcher, 'run_logged') as run:
            launcher.prepare(self.state, 'all')
            run.assert_not_called()
        self.assertEqual(db.read_bytes(), b'existing independent state')

    def test_manifest_verification_detects_changed_executable(self):
        app = self.root/'app/recon.exe'
        app.parent.mkdir()
        app.write_bytes(b'fixture executable')
        manifest = {'files': {'app/recon.exe': {'sha256': hashlib.sha256(app.read_bytes()).hexdigest()}}}
        launcher.atomic_json(self.root/'portable-manifest.json', manifest)
        with patch.object(launcher, 'HOME', self.root):
            launcher.check_files()
            app.write_bytes(b'changed fixture')
            with self.assertRaisesRegex(RuntimeError, 'missing or changed'):
                launcher.check_files()

    def test_check_only_does_not_create_user_state(self):
        target = self.root/'尚未创建'
        with patch.object(sys, 'argv', ['launcher.py', 'check', '--state', str(target)]), patch.object(launcher, 'check_files'), contextlib.redirect_stdout(io.StringIO()):
            launcher.main()
        self.assertFalse(target.exists())

    def test_child_environment_uses_isolated_state(self):
        with patch.dict(launcher.os.environ, {'LOCALAPPDATA': 'author-state', 'PYTHONPATH': 'author-modules', 'PYTHONHOME': 'author-python'}):
            env = launcher.env_for(self.state)
        self.assertEqual(env['LOCALAPPDATA'], str(self.state/'local'))
        self.assertNotIn('PYTHONPATH', env)
        self.assertNotIn('PYTHONHOME', env)

    def test_busy_port_rejects_before_preparing_or_stopping(self):
        class BusySocket:
            def __enter__(self):return self
            def __exit__(self, *args):pass
            def connect_ex(self, address):return 0
        with patch.object(sys, 'argv', ['launcher.py', 'start', '--state', str(self.state), '--no-browser']), patch.object(launcher, 'check_files'), patch.object(launcher, 'health', return_value=None), patch.object(launcher.socket, 'socket', return_value=BusySocket()), patch.object(launcher, 'prepare') as prepare, patch.object(launcher.os, 'kill') as kill:
            with self.assertRaisesRegex(RuntimeError, 'another application'):
                launcher.main()
            prepare.assert_not_called()
            kill.assert_not_called()

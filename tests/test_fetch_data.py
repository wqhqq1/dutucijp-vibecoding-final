"""Isolated provenance/immutability tests; fixture bytes are not official data."""
import contextlib
import hashlib
import io
import json
import stat
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tools'))
import fetch_data


class SourceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'raw').mkdir()
        self.path = self.root / 'raw/online_retail_II.xlsx'
        self.path.write_bytes(b'isolated test bytes, not official workbook')
        self.expected = hashlib.sha256(self.path.read_bytes()).hexdigest()
        self.receipt = self.root / 'raw/source.json'
        self.receipt.write_text(json.dumps(dict(sha256=self.expected, source_url=fetch_data.SOURCE,
            download_url=fetch_data.DOWNLOAD, license='CC BY 4.0', acquired_at='test-only-original-acquisition')), encoding='utf-8')
        self.addCleanup(lambda: self.path.chmod(stat.S_IREAD | stat.S_IWRITE))

    def fetch(self):
        with patch.object(fetch_data, 'EXPECTED', self.expected), patch.object(sys, 'argv', ['fetch_data.py', '--root', str(self.root)]), contextlib.redirect_stdout(io.StringIO()):
            fetch_data.main()

    def test_TC01_existing_source_becomes_read_only_without_rewriting_receipt(self):
        before = self.receipt.read_bytes()
        self.fetch()
        self.assertEqual(self.expected, fetch_data.sha256(self.path))
        self.assertFalse(self.path.stat().st_mode & stat.S_IWRITE)
        self.assertEqual(before, self.receipt.read_bytes())

    def test_TC01_tampered_original_rejected_without_overwrite(self):
        self.path.write_bytes(b'changed fixture')
        with self.assertRaisesRegex(ValueError, 'never overwrite'):
            self.fetch()
        self.assertEqual(self.path.read_bytes(), b'changed fixture')

    def test_TC01_inconsistent_provenance_rejected(self):
        receipt = json.loads(self.receipt.read_text())
        receipt['license'] = 'incorrect fixture license'
        self.receipt.write_text(json.dumps(receipt))
        with self.assertRaisesRegex(ValueError, 'source receipt'):
            self.fetch()

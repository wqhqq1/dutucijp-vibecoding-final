"""Download the official workbook without overwriting an existing original."""
import argparse
import hashlib
import json
import os
from datetime import datetime, timezone
from pathlib import Path
from urllib.request import urlopen
from zipfile import ZipFile

SOURCE = 'https://archive.ics.uci.edu/dataset/502/online+retail+ii'
DOWNLOAD = 'https://archive.ics.uci.edu/static/public/502/online%2Bretail%2Bii.zip'
EXPECTED = 'bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980'


def sha256(path):
    with open(path, 'rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--root', type=Path, default=Path(os.environ['LOCALAPPDATA'])/'dutucijp-vibecoding-final')
    args = parser.parse_args()
    raw = args.root/'raw'
    raw.mkdir(parents=True, exist_ok=True)
    workbook = raw/'online_retail_II.xlsx'
    if workbook.exists():
        if sha256(workbook) != EXPECTED:
            raise ValueError('Existing raw workbook differs from the reviewed official version; never overwrite it')
        if not (raw/'source.json').exists():
            raise ValueError('Existing workbook lacks source receipt; investigate provenance before using it')
        print(json.dumps({'reused': True, 'path': str(workbook), 'sha256': EXPECTED}))
        return
    archive = raw/'online_retail_II.zip'
    if not archive.exists():
        partial = raw/'download.part'
        with urlopen(DOWNLOAD, timeout=120) as response, open(partial, 'wb') as target:
            while chunk := response.read(1024*1024):
                target.write(chunk)
        partial.rename(archive)
    with ZipFile(archive) as zipped:
        names = [n for n in zipped.namelist() if n.endswith('online_retail_II.xlsx')]
        if len(names) != 1:
            raise ValueError('Expected exactly one official workbook')
        content = zipped.read(names[0])
    if hashlib.sha256(content).hexdigest() != EXPECTED:
        raise ValueError('Official workbook changed; review source before accepting a new version')
    with workbook.open('xb') as stream:
        stream.write(content)
    receipt = dict(source_url=SOURCE, download_url=DOWNLOAD, license='CC BY 4.0',
                   citation='Chen, D. (2012). Online Retail II. DOI 10.24432/C5CG6D',
                   acquired_at=datetime.now(timezone.utc).isoformat(), sha256=EXPECTED,
                   zip_sha256=sha256(archive), bytes=workbook.stat().st_size)
    (raw/'source.json').write_text(json.dumps(receipt, indent=2)+'\n', encoding='utf-8')
    print(json.dumps(receipt))


if __name__ == '__main__':
    main()

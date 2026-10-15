"""Read-only copy of an existing official run into a new private demo runtime."""
import argparse
from datetime import datetime,timezone,timedelta
import hashlib,json,os
from pathlib import Path
import sqlite3

def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream,'sha256').hexdigest()

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--source',required=True);parser.add_argument('--destination',required=True);args=parser.parse_args()
    now=datetime.now(timezone(timedelta(hours=8)))
    if now.strftime('%m-%d') in ('10-22','11-17'):raise SystemExit('Reserved day: no new demo initialization')
    source=Path(args.source).resolve(strict=True);destination=Path(args.destination).resolve()
    allowed=Path(os.environ['LOCALAPPDATA'])/'dutucijp-vibecoding-final/demos'
    if not destination.parent.parent.samefile(allowed) or not destination.parent.name.startswith('demo-') or destination.name!='business.sqlite' or destination.exists():
        raise SystemExit('Expected a new demos/demo-*/business.sqlite destination')
    before=digest(source)
    with sqlite3.connect(source.as_uri()+'?mode=ro',uri=True) as original:
        row=original.execute("SELECT r.id,r.dataset_id,d.cleaning_version,r.rule_version,d.source_hash FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.status='published' AND d.status='published' AND d.scope='year1' AND d.cleaning_version='cr-1.0.1' AND d.source_hash=? LIMIT 1",('bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980',)).fetchone()
        if row is None:raise SystemExit('Existing official cr-1.0.1 year1 run required')
        with sqlite3.connect(destination) as copy:original.backup(copy)
    after=digest(source)
    if before!=after:raise SystemExit('Original changed; demo not accepted')
    receipt={'actual_time':now.isoformat(),'database':str(destination),'source_database':str(source),'source_db_sha256':before,'source_database_unchanged':True,'run_id':row[0],'dataset_id':row[1],'cleaning_version':row[2],'rule_version':row[3],'source_hash':row[4],'notice':'Official transactions; new course runtime. Real passwords and factors must be configured by the user.'}
    (destination.parent/'demo.json').write_text(json.dumps(receipt,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    print(json.dumps(receipt,ensure_ascii=False,indent=2))

if __name__=='__main__':main()

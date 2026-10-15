"""Copy only official published INPUT tables into a new private NFR03 template.

No run, decision, allocation, balance, case, audit, authentication or session
table is read from the source. Template preparation is outside the Run timer.
"""
import argparse
import hashlib
import json
import sqlite3
from datetime import datetime, timedelta, timezone
from pathlib import Path
from time import perf_counter


def digest(path):
    hasher = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            hasher.update(chunk)
    return hasher.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-db', required=True, type=Path)
    parser.add_argument('--output-db', required=True, type=Path)
    args = parser.parse_args()
    if datetime.now(timezone(timedelta(hours=8))).strftime('%m-%d') in ('10-22', '11-17'):
        raise ValueError('Reserved day in Asia/Shanghai: no preparation work')
    source = args.source_db.resolve(strict=True)
    output = args.output_db.resolve()
    project = Path(__file__).resolve().parents[1]
    if output == project or project in output.parents or output.exists():
        raise ValueError('Requires a new output database outside the repository')
    for suffix in ('-wal', '-shm'):
        if Path(str(source) + suffix).exists():
            raise ValueError('Source must be closed with no SQLite companions')
    started = perf_counter()
    source_hash_before = digest(source)
    output.parent.mkdir(parents=True, exist_ok=True)
    connection = sqlite3.connect(output, uri=True)
    try:
        connection.execute('PRAGMA foreign_keys=ON')
        connection.execute('PRAGMA cache_size=-65536')
        connection.executescript((project / 'backend/internal/store/schema.sql').read_text(encoding='utf-8'))
        connection.execute('ATTACH DATABASE ? AS input_source', (source.as_uri() + '?mode=ro',))
        # The source queries are deliberately restricted to these input tables.
        with connection:
            for table in ('datasets', 'source_rows', 'normalized_rows'):
                connection.execute(f'INSERT INTO main.{table} SELECT * FROM input_source.{table}')
        connection.executescript('''
            CREATE INDEX IF NOT EXISTS cases_owner ON cases(run_id,assignee,cancel_id);
            CREATE INDEX IF NOT EXISTS row_invoice ON normalized_rows(dataset_id,invoice);
            CREATE INDEX IF NOT EXISTS row_source_time ON normalized_rows(dataset_id,source_time,id);
            CREATE INDEX IF NOT EXISTS row_stock ON normalized_rows(dataset_id,stock,id);
        ''')
        counts = {table: connection.execute(f'SELECT count(*) FROM main.{table}').fetchone()[0]
                  for table in ('datasets', 'source_rows', 'normalized_rows', 'runs', 'decisions',
                                'allocations', 'sale_balances', 'cases', 'candidate_evidence', 'audit_events')}
        datasets = connection.execute('SELECT id,scope,source_hash,cleaning_version,records_hash,status FROM datasets').fetchall()
        if len(datasets) != 1 or datasets[0][1] != 'all' or datasets[0][5] != 'published':
            raise ValueError('Expected one official published all-scope input dataset')
        if counts['source_rows'] != 1067371 or counts['normalized_rows'] != 1067371:
            raise ValueError('Template is not the complete official input')
        if any(counts[table] for table in ('runs', 'decisions', 'allocations', 'sale_balances', 'cases', 'candidate_evidence', 'audit_events')):
            raise ValueError('Template contains old computed or human output')
        connection.execute('DETACH DATABASE input_source')
        connection.execute('PRAGMA wal_checkpoint(TRUNCATE)')
    finally:
        connection.close()
    source_hash_after = digest(source)
    if source_hash_before != source_hash_after:
        raise ValueError('Source changed during read-only template preparation')
    result = dict(actual_time=datetime.now(timezone(timedelta(hours=8))).isoformat(),
                  source_db=str(source), output_db=str(output), seconds=perf_counter() - started,
                  source_sha256_before=source_hash_before, source_sha256_after=source_hash_after,
                  template_sha256=digest(output), input_counts=counts, dataset=datasets[0],
                  source_tables_read=['datasets', 'source_rows', 'normalized_rows'],
                  note='Only input tables copied with read-only ATTACH; every computed/human output table is empty. Template preparation is not NFR03 computation.')
    receipt = Path(str(output) + '.input-receipt.json')
    receipt.write_text(json.dumps(result, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()

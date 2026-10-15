"""Read-only online backup of an existing official run for isolated workflow verification.

No prepared transactions or associations are invented. Workflow actors/actions in
the separate Go verifier are explicitly simulated and never become real users.
"""
import argparse
from datetime import datetime, timezone, timedelta
import hashlib
import json
import os
from pathlib import Path
import sqlite3

OFFICIAL_HASH = "bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980"


def file_hash(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def fingerprint(connection):
    result = {}
    for table in ("cases", "audit_events", "allocations", "sale_balances"):
        digest = hashlib.sha256()
        count = 0
        for row in connection.execute(f"SELECT * FROM {table} ORDER BY rowid"):
            digest.update(json.dumps(row, ensure_ascii=False, separators=(",", ":")).encode())
            digest.update(b"\n")
            count += 1
        result[table] = {"rows": count, "sha256": digest.hexdigest()}
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True)
    parser.add_argument("--destination", required=True)
    args = parser.parse_args()
    now = datetime.now(timezone(timedelta(hours=8)))
    if now.strftime("%m-%d") in ("10-22", "11-17"):
        raise SystemExit("Reserved day: no workflow backup or verification")
    source = Path(args.source).resolve(strict=True)
    destination = Path(args.destination).resolve()
    allowed = (Path(os.environ["LOCALAPPDATA"]) / "dutucijp-vibecoding-final/verification").resolve()
    # Windows app containers can report a physical LocalCache path for the child
    # while keeping LOCALAPPDATA's logical root name. Compare directory identity,
    # not differently virtualized path strings. No deletion/move is performed.
    if (not destination.parent.parent.samefile(allowed)
            or not destination.parent.name.startswith("workflow-")
            or destination.name != "workflow.sqlite" or destination.exists() or source == destination):
        raise SystemExit("Destination must be a new verification/workflow-*/workflow.sqlite")
    before_hash = file_hash(source)
    with sqlite3.connect(source.as_uri() + "?mode=ro", uri=True) as original:
        original.execute("PRAGMA query_only=ON")
        row = original.execute("""SELECT r.id,r.dataset_id,d.scope,d.cleaning_version,r.rule_version,d.source_hash
            FROM runs r JOIN datasets d ON d.id=r.dataset_id
            WHERE r.status='published' AND d.status='published' AND d.cleaning_version='cr-1.0.1'
            AND d.source_hash=? AND d.scope='year1' AND r.rule_version='conservative-1.0.0'
            ORDER BY r.id LIMIT 1""", (OFFICIAL_HASH,)).fetchone()
        if not row:
            raise SystemExit("Source needs the existing published official cr-1.0.1 year1 run")
        run_id, dataset_id, scope, cleaning, rule, source_hash = row
        if original.execute("SELECT count(*) FROM cases WHERE run_id=? AND status='pending' AND coalesce(assignee,'')=''", (run_id,)).fetchone()[0] < 2:
            raise SystemExit("Source needs at least two unassigned differences")
        before = fingerprint(original)
        with sqlite3.connect(destination) as copy:
            original.backup(copy)
            if fingerprint(copy) != before:
                raise SystemExit("Backup fingerprints differ")
        after = fingerprint(original)
    after_hash = file_hash(source)
    if before != after or before_hash != after_hash:
        raise SystemExit("Source changed during backup; retained copy is not marked for verification")
    receipt = {
        "actual_time": now.isoformat(), "purpose": "isolated-workflow", "database": "workflow.sqlite",
        "source_database": str(source), "copy_database": str(destination), "run_id": run_id,
        "dataset_id": dataset_id, "scope": scope, "cleaning_version": cleaning,
        "rule_version": rule, "source_hash": source_hash,
        "source_db_sha256_before": before_hash, "source_db_sha256_after": after_hash,
        "source_workflow_fingerprints_before": before, "source_workflow_fingerprints_after": after,
        "notice": "Official transactions copied read-only; subsequent actors/actions/conclusions are isolated course simulations, not human business operations or FR-14 authentication.",
    }
    marker = destination.parent / "workflow-verification.json"
    with marker.open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(receipt, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    print(json.dumps(receipt, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()

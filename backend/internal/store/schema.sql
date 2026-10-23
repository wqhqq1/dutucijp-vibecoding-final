PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS datasets (
 id TEXT PRIMARY KEY, scope TEXT NOT NULL, source_hash TEXT NOT NULL,
 cleaning_version TEXT NOT NULL, records_hash TEXT NOT NULL,
 manifest_json TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('staging','published','failed'))
);
CREATE TABLE IF NOT EXISTS source_rows (
 id TEXT PRIMARY KEY, source_hash TEXT NOT NULL, sheet TEXT NOT NULL,
 sheet_index INTEGER NOT NULL, source_row INTEGER NOT NULL, raw_json TEXT NOT NULL,
 UNIQUE(source_hash,sheet,source_row)
);
CREATE TABLE IF NOT EXISTS normalized_rows (
 dataset_id TEXT NOT NULL REFERENCES datasets(id), id TEXT NOT NULL,
 source_id TEXT NOT NULL REFERENCES source_rows(id), invoice TEXT NOT NULL,
 stock TEXT NOT NULL, description TEXT NOT NULL, quantity INTEGER NOT NULL,
 source_time TEXT NOT NULL, price TEXT NOT NULL, customer TEXT NOT NULL,
 country TEXT NOT NULL, kind TEXT NOT NULL, excluded TEXT NOT NULL,
 flags TEXT NOT NULL, duplicate_of TEXT NOT NULL, PRIMARY KEY(dataset_id,id)
);
CREATE TABLE IF NOT EXISTS runs (
 id TEXT PRIMARY KEY, dataset_id TEXT NOT NULL REFERENCES datasets(id), rule_version TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('staging','published','failed')), started_at TEXT NOT NULL,
 completed_at TEXT, summary_json TEXT, error TEXT, UNIQUE(dataset_id,rule_version)
);
CREATE TABLE IF NOT EXISTS decisions (
 run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE, cancel_id TEXT NOT NULL,
 quantity INTEGER NOT NULL CHECK(quantity>0), classification TEXT NOT NULL
 CHECK(classification IN ('matched','suspected','no_original')), reason TEXT NOT NULL,
 candidate_lower_bound INTEGER NOT NULL, PRIMARY KEY(run_id,cancel_id)
);
CREATE TABLE IF NOT EXISTS sale_balances (
 run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE, sale_id TEXT NOT NULL,
 original INTEGER NOT NULL CHECK(original>0), allocated INTEGER NOT NULL CHECK(allocated>=0),
 remaining INTEGER NOT NULL CHECK(remaining>=0), CHECK(original-allocated=remaining), PRIMARY KEY(run_id,sale_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS allocations (
 run_id TEXT NOT NULL, cancel_id TEXT NOT NULL, sale_id TEXT NOT NULL,
 quantity INTEGER NOT NULL CHECK(quantity>0), amount TEXT NOT NULL, method TEXT NOT NULL,
 actor TEXT, PRIMARY KEY(run_id,cancel_id),
 FOREIGN KEY(run_id,cancel_id) REFERENCES decisions(run_id,cancel_id) ON DELETE CASCADE,
 FOREIGN KEY(run_id,sale_id) REFERENCES sale_balances(run_id,sale_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS cases (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL, cancel_id TEXT NOT NULL, reason TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','in_progress','closed')),
 assignee TEXT, version INTEGER NOT NULL DEFAULT 1, conclusion TEXT NOT NULL DEFAULT '',
 assigned_at TEXT NOT NULL DEFAULT '', assigned_by TEXT NOT NULL DEFAULT '',
 generation INTEGER NOT NULL DEFAULT 1,is_current INTEGER NOT NULL DEFAULT 1,
 previous_case_id TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT '',
 waiting INTEGER NOT NULL DEFAULT 0,missing_materials TEXT NOT NULL DEFAULT '',next_step TEXT NOT NULL DEFAULT '',
 UNIQUE(run_id,cancel_id,generation), FOREIGN KEY(run_id,cancel_id) REFERENCES decisions(run_id,cancel_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS allocations_sale ON allocations(run_id,sale_id);
CREATE TABLE IF NOT EXISTS candidate_evidence (
 run_id TEXT NOT NULL, cancel_id TEXT NOT NULL, position INTEGER NOT NULL,
 sale_id TEXT NOT NULL, remaining INTEGER NOT NULL, eligible INTEGER NOT NULL,
 reason TEXT NOT NULL, rule_version TEXT NOT NULL, PRIMARY KEY(run_id,cancel_id,position),
 FOREIGN KEY(run_id,cancel_id) REFERENCES decisions(run_id,cancel_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS audit_events (
 id INTEGER PRIMARY KEY, case_id TEXT NOT NULL REFERENCES cases(id), actor TEXT NOT NULL,
 action TEXT NOT NULL, occurred_at TEXT NOT NULL, before_json TEXT NOT NULL, after_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS cases_workflow ON cases(run_id,status,assignee);
CREATE INDEX IF NOT EXISTS audit_case ON audit_events(case_id,id);
CREATE TABLE IF NOT EXISTS review_sets (
 id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(id),actor TEXT NOT NULL,
 policy_version TEXT NOT NULL,created_at TEXT NOT NULL,body_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS review_sets_actor ON review_sets(run_id,actor,created_at);
CREATE TABLE IF NOT EXISTS review_batches (
 id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES runs(id),actor TEXT NOT NULL,
 created_at TEXT NOT NULL,status TEXT NOT NULL,body_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS review_batches_actor ON review_batches(actor,created_at);
CREATE TABLE IF NOT EXISTS review_batch_groups (
 batch_id TEXT NOT NULL REFERENCES review_batches(id),group_id INTEGER NOT NULL,
 status TEXT NOT NULL,body_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',
 PRIMARY KEY(batch_id,group_id)
);
CREATE TABLE IF NOT EXISTS duplicate_sale_groups (
 run_id TEXT NOT NULL REFERENCES runs(id),id TEXT NOT NULL,canonical_id TEXT NOT NULL,
 capacity INTEGER NOT NULL CHECK(capacity>0),actor TEXT NOT NULL,batch_id TEXT NOT NULL,
 PRIMARY KEY(run_id,id)
);
CREATE TABLE IF NOT EXISTS duplicate_sale_members (
 run_id TEXT NOT NULL,sale_id TEXT NOT NULL,group_id TEXT NOT NULL,
 PRIMARY KEY(run_id,sale_id),FOREIGN KEY(run_id,group_id) REFERENCES duplicate_sale_groups(run_id,id)
);
CREATE TABLE IF NOT EXISTS review_confirmations (
 run_id TEXT NOT NULL,cancel_id TEXT NOT NULL,batch_id TEXT NOT NULL,
 recommendation_kind TEXT NOT NULL,policy_version TEXT NOT NULL,evidence_json TEXT NOT NULL,
 PRIMARY KEY(run_id,cancel_id),FOREIGN KEY(run_id,cancel_id) REFERENCES allocations(run_id,cancel_id)
);
CREATE TRIGGER IF NOT EXISTS audit_no_update BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT,'audit events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS audit_no_delete BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT,'audit events are append-only'); END;
CREATE TABLE IF NOT EXISTS remediation_runs(run_id TEXT PRIMARY KEY REFERENCES runs(id),revision INTEGER NOT NULL DEFAULT 0,initialized INTEGER NOT NULL DEFAULT 0,sparse INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS business_rows AS SELECT '' AS run_id,n.* FROM normalized_rows n WHERE 0;
CREATE UNIQUE INDEX IF NOT EXISTS business_row_key ON business_rows(run_id,id);
CREATE TABLE IF NOT EXISTS supplemental_sources(
 run_id TEXT NOT NULL REFERENCES runs(id),id TEXT PRIMARY KEY,source_hash TEXT NOT NULL,
 sheet TEXT NOT NULL,sheet_index INTEGER NOT NULL,source_row INTEGER NOT NULL,raw_json TEXT NOT NULL,
 document_ref TEXT NOT NULL,line_ref TEXT NOT NULL,UNIQUE(run_id,document_ref,line_ref)
);
CREATE TABLE IF NOT EXISTS current_assessments(
 run_id TEXT NOT NULL,cancel_id TEXT NOT NULL,quantity INTEGER NOT NULL,classification TEXT NOT NULL,
 reason TEXT NOT NULL,candidate_lower_bound INTEGER NOT NULL,evidence_json TEXT NOT NULL,
 PRIMARY KEY(run_id,cancel_id),FOREIGN KEY(run_id,cancel_id) REFERENCES decisions(run_id,cancel_id)
);
CREATE TABLE IF NOT EXISTS case_snapshots(case_id TEXT PRIMARY KEY REFERENCES cases(id),row_json TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS case_decision_snapshots(case_id TEXT PRIMARY KEY REFERENCES cases(id),decision_json TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remediation_events(
 id TEXT PRIMARY KEY,run_id TEXT NOT NULL,case_id TEXT NOT NULL REFERENCES cases(id),actor TEXT NOT NULL,
 request_id TEXT NOT NULL,request_json TEXT NOT NULL,result_json TEXT NOT NULL,
 action TEXT NOT NULL,evidence TEXT NOT NULL,before_json TEXT NOT NULL,after_json TEXT NOT NULL,occurred_at TEXT NOT NULL,
 UNIQUE(actor,request_id)
);
CREATE TRIGGER IF NOT EXISTS remediation_no_update BEFORE UPDATE ON remediation_events BEGIN SELECT RAISE(ABORT,'remediation events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS remediation_no_delete BEFORE DELETE ON remediation_events BEGIN SELECT RAISE(ABORT,'remediation events are append-only'); END;
CREATE TABLE IF NOT EXISTS duplicate_verifications(
 run_id TEXT NOT NULL,row_id TEXT NOT NULL,disposition TEXT NOT NULL,canonical_id TEXT NOT NULL,
 event_id TEXT NOT NULL,PRIMARY KEY(run_id,row_id)
);

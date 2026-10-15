import sqlite3, pathlib, json, datetime, hashlib, os
now=datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8)))
assert now.strftime('%Y-%m-%d')=='2026-10-15', 'Only authorized for this actual date'
root=pathlib.Path(os.environ['LOCALAPPDATA'])/'dutucijp-vibecoding-final'
p=root/'demos/demo-20261015-170120-d36101c6/business.sqlite'
assert p.is_file() and p.parent.name=='demo-20261015-170120-d36101c6'
c=sqlite3.connect(p, timeout=30, isolation_level=None)
c.execute('PRAGMA foreign_keys=ON')
c.execute('PRAGMA synchronous=FULL')
assert c.execute('select role,state,version from auth_users where id=?',('warehouse',)).fetchone()==('warehouse','complete',4), 'Unexpected account state; do not repeat reset'
private=p.parent/'private-auth'
assert private.is_dir()
backup=private/('warehouse-before-reset-'+now.strftime('%Y%m%d-%H%M%S')+'.sqlite')
assert not backup.exists()
b=sqlite3.connect(backup)
c.backup(b)
b.close()
tables=['auth_users','auth_sessions','auth_credentials','auth_recovery','auth_bootstrap','auth_challenges']
def others():
 return {t:c.execute('select * from '+t+' where '+('id' if t=='auth_users' else 'user_id')+'<>? order by 1',('warehouse',)).fetchall() for t in tables}
try:
 c.execute('BEGIN IMMEDIATE')
 before_others=others()
 before_audit=c.execute('select * from auth_audit order by id').fetchall()
 assert c.execute('select role,state,version from auth_users where id=?',('warehouse',)).fetchone()==('warehouse','complete',4)
 deleted={}
 for t in tables[1:]:
  deleted[t]=c.execute('delete from '+t+' where user_id=?',('warehouse',)).rowcount
 assert c.execute("update auth_users set password_hash='',state='new',version=version+1,totp_secret=NULL,pending_totp=NULL,last_step=-1 where id='warehouse' and role='warehouse' and state='complete' and version=4").rowcount==1
 limit_key='account:'+hashlib.sha256(b'warehouse').hexdigest()
 deleted['account_rate_limit']=c.execute('delete from auth_limits where key=?',(limit_key,)).rowcount
 audit_id=c.execute('insert into auth_audit(actor,action,method,result,occurred_at) values(?,?,?,?,?)',('codex_local_maintenance','demo_account_reset','explicit_user_authorized_demo_only','warehouse:complete/v4->new/v5; requires strong password and verified factor',now.isoformat())).lastrowid
 assert others()==before_others, 'Other accounts changed'
 assert c.execute('select * from auth_audit where id<>? order by id',(audit_id,)).fetchall()==before_audit, 'Old audits changed'
 assert c.execute("select state,version,length(password_hash),totp_secret is null,pending_totp is null from auth_users where id='warehouse'").fetchone()==('new',5,0,1,1)
 c.commit()
 result={'occurred_at':now.isoformat(),'scope':'one-time explicitly authorized local demo maintenance, not a product recovery flow or acceptance pass','account':'warehouse','state_before':'complete','version_before':4,'state_after':'new','version_after':5,'revoked_counts':deleted,'other_accounts_unchanged_in_transaction':True,'existing_auth_audit_unchanged':True,'new_auth_audit_id':audit_id,'business_sql_writes':0,'private_backup_created':True,'private_backup_runtime_relative':str(backup.relative_to(root)),'strong_password_and_verified_factor_required':True}
 print(json.dumps(result,ensure_ascii=False,indent=2))
except:
 c.rollback()
 raise
finally:
 c.close()
"""Explicit synthetic HTTP acceptance against a new, isolated portable runtime.

Uses the normal account-selection/password/TOTP/recovery-saving routes. Never modifies
the author's runtime. Secret responses stay in memory, not evidence files.
"""
from pathlib import Path
import argparse,base64,csv,hashlib,hmac,http.cookiejar,io,json,os,secrets,sqlite3,struct,subprocess,time,urllib.request,urllib.error
from urllib.parse import quote

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--state',type=Path,required=True);ap.add_argument('--bundle',type=Path,required=True);ap.add_argument('--port',type=int,required=True);ap.add_argument('--output',type=Path,required=True);ap.add_argument('--allow-synthetic-test',action='store_true');args=ap.parse_args()
    assert args.allow_synthetic_test and args.port!=8080
    state=args.state.resolve();bundle=args.bundle.resolve();db=state/'local/dutucijp-vibecoding-final/state/business.sqlite';base=f'http://localhost:{args.port}'
    with sqlite3.connect(db.as_uri()+'?mode=ro',uri=True) as c:
        assert c.execute("SELECT count(*) FROM auth_users WHERE state!='new' OR password_hash<>''").fetchone()[0]==0,'Only a fresh, untouched QA runtime is allowed'
        assert c.execute('SELECT count(*) FROM audit_events').fetchone()[0]==0
    events=[]
    class Client:
        def __init__(self):self.jar=http.cookiejar.CookieJar();self.opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar));self.csrf=''
        def call(self,method,path,body=None,expected=200,raw=False):
            req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Origin':base,'Content-Type':'application/json','X-CSRF-Token':self.csrf})
            try:
                with self.opener.open(req,timeout=30) as r:status=r.status;data=r.read()
            except urllib.error.HTTPError as e:status=e.code;data=e.read()
            if status!=expected:raise RuntimeError(f'{method} {path.split("?")[0]} status {status}; expected {expected}; response withheld')
            result=data if raw else json.loads(data)
            if not raw:
                self.csrf=result.get('session',{}).get('csrf',result.get('csrf',self.csrf)) if isinstance(result,dict) else self.csrf
            return result
    anonymous=Client();h=anonymous.call('GET','/api/health');assert h['runtime_id']==json.loads((state/'instance.json').read_text())['id']
    anonymous.call('GET','/api/runs',expected=401);events.append('Unauthenticated business rejected')
    def totp(secret):
        key=base64.b32decode(secret+'='*((-len(secret))%8));d=hmac.new(key,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest();o=d[-1]&15;return str((struct.unpack('>I',d[o:o+4])[0]&0x7fffffff)%1000000).zfill(6)
    def setup(account):
        client=Client()
        assert account in [a['id'] for a in client.call('GET','/api/auth/setup/accounts')]
        client.call('POST','/api/auth/setup/start',{'id':account})
        client.call('GET','/api/runs',expected=403)
        password='Synthetic QA '+secrets.token_urlsafe(24)+' 73!'
        client.call('POST','/api/auth/setup/password',{'password':password})
        client.call('GET','/api/runs',expected=403)
        en=client.call('POST','/api/auth/totp/enroll',{})
        grant=client.call('POST','/api/auth/totp/confirm',{'code':totp(en['secret'])})
        assert len(grant.get('recovery_codes',[]))>0
        client.call('GET','/api/runs',expected=403)
        client.call('POST','/api/auth/setup/complete',{'saved':True})
        assert all(c.name=='recon_session_'+str(args.port) for c in client.jar)
        return client,password,en['secret']
    finance,password,secret=setup('finance');warehouse,_,_=setup('warehouse')
    events.append('New synthetic finance/warehouse required strong password, verified TOTP and recovery-code saving; no credential bypass')
    runs=finance.call('GET','/api/runs');assert isinstance(runs,list);all_run=next(x for x in runs if x['scope']=='all')['run_id']
    page=finance.call('GET','/api/cases?run_id='+all_run+'&status=pending&page_size=50');case=page['items'][0];path='/api/cases/'+quote(case['id'],safe='')
    assigned=finance.call('POST',path+'/assign',{'assignee':'warehouse'});assert assigned['version']==2
    finance.call('POST',path+'/assign',{'assignee':'warehouse'},expected=409)
    started=warehouse.call('POST',path+'/start',{'version':2});assert started['version']==3
    warehouse.call('POST',path+'/close',{'version':3,'conclusion':''},expected=422)
    closed=warehouse.call('POST',path+'/close',{'version':3,'conclusion':'隔离交付自动验收：当前资料无法证明真实原订单，带说明结案，不新增配对。'});assert closed['status']=='closed' and closed['version']==4
    audit=warehouse.call('GET',path+'/audit');assert len(audit)==3
    warehouse.call('GET','/api/exports?run_id='+all_run,expected=403)
    events.append('Real official-data case assigned finance to warehouse, duplicate rejected, start/empty conclusion/close checked, three audits and warehouse export denial verified')
    job=finance.call('POST','/api/jobs',{'scope':'year1'});deadline=time.monotonic()+240
    while time.monotonic()<deadline:
        current=finance.call('GET','/api/jobs/'+job['id'])
        if current['status']=='succeeded':break
        if current['status']=='failed':raise RuntimeError('Bundled-Python background data job failed; private diagnostics retained')
        time.sleep(1)
    else:raise RuntimeError('Background job timed out')
    events.append('Background year1 preparation/reconciliation succeeded using only bundled Python and newly downloaded official data')
    run=current['run_id'];data=finance.call('GET','/api/transactions?run_id='+run+'&customer=13526&page_size=50');csv_data=finance.call('GET','/api/exports?run_id='+run+'&customer=13526',raw=True)
    assert len(list(csv.DictReader(io.StringIO(csv_data.decode('utf-8-sig')))))==data['total']==54
    events.append('CSV exports all 54 filtered rows, not only the first page')
    launcher=[str(bundle/'python/python.exe'),str(bundle/'portable/launcher.py')]
    common=['--state',str(state),'--port',str(args.port),'--no-browser']
    subprocess.run(launcher+['stop']+common,check=True,capture_output=True)
    for _ in range(50):
        try:urllib.request.urlopen(base+'/api/health',timeout=.5)
        except Exception:break
        time.sleep(.1)
    logs=state/'logs';restart_out=(logs/'qa-restart.txt').open('ab')
    process=subprocess.Popen(launcher+['start']+common,stdout=restart_out,stderr=restart_out,cwd=bundle)
    try:
        for _ in range(100):
            try:
                if finance.call('GET',path)['status']=='closed':break
            except Exception:time.sleep(.2)
        else:raise RuntimeError('Restart/state/session preservation failed')
        finance.call('POST','/api/auth/logout',{})
        finance.call('POST','/api/auth/login',{'id':'finance','password':password})
        finance.call('GET','/api/runs',expected=403)
        # Enrollment consumed this step; use the next real step, without changing clocks.
        time.sleep(30-(time.time()%30)+.15)
        finance.call('POST','/api/auth/totp/login',{'code':totp(secret)})
        assert finance.call('GET',path)['status']=='closed'
        events.append('Restart retained account, session and closed case; subsequent password plus fresh real-time TOTP login succeeded')
    finally:
        subprocess.run(launcher+['stop']+common,check=True,capture_output=True);process.wait(timeout=15);restart_out.close()
    with sqlite3.connect(db.as_uri()+'?mode=ro',uri=True) as c:
        counts={k:c.execute('SELECT count(*) FROM '+k).fetchone()[0] for k in ['source_rows','allocations','cases','audit_events']}
    result={'status':'passed','actual_time':__import__('datetime').datetime.now(__import__('datetime').timezone(__import__('datetime').timedelta(hours=8))).isoformat(),'test_kind':'same-host clean profile, explicit bundled Python, fresh official download, synthetic isolated account credentials','external_runtime_path_removed':False,'scope':'all and year1','port':args.port,'checks':events,'counts':counts,'private_state_included_in_release':False,'different_physical_machine_tested':False,'real_human_factor_setup_claimed':False}
    args.output.parent.mkdir(parents=True,exist_ok=True);args.output.write_bytes((json.dumps(result,ensure_ascii=False,indent=2)+'\n').encode());print(json.dumps(result,ensure_ascii=False))
if __name__=='__main__':main()

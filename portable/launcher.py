"""Independent Windows reviewer launcher. No original runtime or account is read."""
from pathlib import Path
import argparse,hashlib,json,os,secrets,shutil,socket,subprocess,sys,time,urllib.request,webbrowser

APP=Path(__file__).resolve().parent
HOME=APP.parent
PYTHON=HOME/'python/python.exe'
BINARY=HOME/'app/recon.exe'
PROJECT=HOME/'project'

def atomic_json(path,data):
    path.parent.mkdir(parents=True,exist_ok=True)
    tmp=path.with_suffix('.tmp');tmp.write_bytes((json.dumps(data,ensure_ascii=False,indent=2)+'\n').encode());tmp.replace(path)

def paths(state):
    base=state/'local';runtime=base/'dutucijp-vibecoding-final'
    return base,runtime,runtime/'state/business.sqlite'

def env_for(state):
    env=dict(os.environ);env['LOCALAPPDATA']=str(paths(state)[0]);env.pop('PYTHONPATH',None);env.pop('PYTHONHOME',None)
    return env

def health(port):
    try:
        with urllib.request.urlopen(f'http://localhost:{port}/api/health',timeout=1) as r:return json.load(r)
    except Exception:return None

def check_files():
    manifest=json.loads((HOME/'portable-manifest.json').read_text(encoding='utf-8'))
    for relative,item in manifest['files'].items():
        p=HOME/relative
        if not p.is_file():
            raise RuntimeError('Package file missing or changed: '+relative)
        with p.open('rb') as stream:
            if hashlib.file_digest(stream,'sha256').hexdigest()!=item['sha256']:
                raise RuntimeError('Package file missing or changed: '+relative)

def run_logged(args,state,name):
    print('Starting: '+name,flush=True)
    logs=state/'logs';logs.mkdir(parents=True,exist_ok=True)
    with (logs/(name+'.txt')).open('ab') as out:
        p=subprocess.run(list(map(str,args)),cwd=PROJECT,env=env_for(state),stdout=out,stderr=subprocess.STDOUT)
    if p.returncode:raise RuntimeError('Step failed: '+name+'; see '+str(logs/(name+'.txt')))
    print('Completed: '+name,flush=True)

def prepare(state,scope):
    base,runtime,db=paths(state);runtime.mkdir(parents=True,exist_ok=True)
    if db.exists():
        # Never treat a partial database as a completed setup or overwrite it.
        if not (state/'ready.json').is_file():raise RuntimeError('Incomplete prior preparation retained. Use a new state directory; see logs.')
        return
    if shutil.disk_usage(state).free < 8*1024**3:raise RuntimeError('First full-data setup requires at least 8 GiB free disk space.')
    print('First start: downloading the official UCI workbook and generating your own database. Internet and disk space are required.',flush=True)
    run_logged([PYTHON,PROJECT/'tools/fetch_data.py'],state,'download')
    run_logged([PYTHON,PROJECT/'tools/prepare.py','--scope',scope,'--output',runtime/'prepared'],state,'prepare')
    with (state/'logs/prepare.txt').open(encoding='utf-8') as f:
        entries=[json.loads(line) for line in f if line.lstrip().startswith('{')]
    prepared=next(x['path'] for x in reversed(entries) if 'path' in x)
    db.parent.mkdir(parents=True,exist_ok=True)
    run_logged([BINARY,'run','--prepared',prepared,'--db',db],state,'reconcile')
    atomic_json(state/'ready.json',{'scope':scope,'created_at':time.time(),'notice':'New official-data runtime; not copied from the author'})

def open_account(state,account,port,enabled):
    # The app lists untouched accounts directly. Do not read private links or
    # renew tokens merely to open a browser, including on repeated launches.
    if enabled:webbrowser.open(f'http://localhost:{port}')


def main():
    ap=argparse.ArgumentParser();ap.add_argument('action',choices=['start','prepare','stop','new','check'],nargs='?',default='start');ap.add_argument('--state',type=Path);ap.add_argument('--port',type=int,default=8080);ap.add_argument('--scope',choices=['year1','all'],default='all');ap.add_argument('--account',choices=['finance','warehouse','admin'],default='finance');ap.add_argument('--no-browser',action='store_true');args=ap.parse_args()
    if not 1024<=args.port<=65535:raise RuntimeError('Port must be between 1024 and 65535')
    check_files()
    if args.action=='check':print('Portable files verified. First preparation requires official-source internet access; no runtime was created.');return
    if args.action=='new':
        with socket.socket() as probe:
            if probe.connect_ex(('127.0.0.1',args.port))==0:raise RuntimeError('Stop the known Demo before creating a new one. Existing state selection was not changed.')
    default=Path(os.environ['LOCALAPPDATA'])/'dutucijp-teacher-demo'
    selection=default/'selected.json'
    if args.state:state=args.state.resolve()
    elif args.action=='new' or not selection.exists():
        state=default/('instance-'+secrets.token_hex(8));atomic_json(selection,{'state':str(state)})
    else:state=Path(json.loads(selection.read_text())['state'])
    state.mkdir(parents=True,exist_ok=True)
    identity=state/'instance.json'
    if not identity.exists():atomic_json(identity,{'id':secrets.token_hex(16)})
    instance=json.loads(identity.read_text())['id']
    status=health(args.port)
    if args.action=='stop':
        pidfile=state/'server.json'
        if not pidfile.exists() or not status or status.get('runtime_id')!=instance:raise RuntimeError('No matching instance; no process stopped')
        pid=int(json.loads(pidfile.read_text())['pid'])
        import ctypes,signal
        kernel=ctypes.windll.kernel32
        kernel.OpenProcess.restype=ctypes.c_void_p
        h=kernel.OpenProcess(0x1000,False,pid)
        if not h:raise RuntimeError('Saved process no longer exists')
        try:
            size=ctypes.c_ulong(32768);buf=ctypes.create_unicode_buffer(size.value)
            if not kernel.QueryFullProcessImageNameW(ctypes.c_void_p(h),0,buf,ctypes.byref(size)) or Path(buf.value).resolve()!=BINARY.resolve():raise RuntimeError('Process differs; not stopped')
        finally:kernel.CloseHandle(ctypes.c_void_p(h))
        atomic_json(state/'stop-request.json',{'pid':pid,'at':time.time()})
        os.kill(pid,signal.SIGTERM);print('Matching teacher Demo stopped. Data retained.');return
    if status and status.get('runtime_id')==instance:
        open_account(state,args.account,args.port,not args.no_browser)
        print('This teacher Demo is already running.');return
    with socket.socket() as probe:
        if probe.connect_ex(('127.0.0.1',args.port))==0:raise RuntimeError(f'Port {args.port} belongs to another application. No process stopped. Close the known application or choose --port.')
    prepare(state,args.scope)
    if args.action=='prepare':print('Independent database prepared.');return
    base,runtime,db=paths(state)
    run_logged([BINARY,'init-auth','--db',db,'--project',PROJECT,'--port',args.port],state,'account-initialization')
    logs=state/'logs';out=(logs/'server.stdout.txt').open('ab');err=(logs/'server.stderr.txt').open('ab')
    process=subprocess.Popen([str(BINARY),'serve','--db',str(db),'--project',str(PROJECT),'--python',str(PYTHON),'--port',str(args.port),'--runtime-id',instance],cwd=PROJECT,env=env_for(state),stdout=out,stderr=err,creationflags=subprocess.CREATE_NEW_PROCESS_GROUP)
    try:
        for _ in range(100):
            if process.poll() is not None:raise RuntimeError('Server exited; inspect server.stderr.txt')
            value=health(args.port)
            if value and value.get('runtime_id')==instance:break
            time.sleep(.2)
        else:process.terminate();raise RuntimeError('Server did not become healthy')
        atomic_json(state/'server.json',{'pid':process.pid,'port':args.port,'binary':str(BINARY),'runtime_id':instance})
        open_account(state,args.account,args.port,not args.no_browser)
        print(f'Demo ready at http://localhost:{args.port}. First use requires your own strong password and verified Passkey or TOTP. Keep this window open; Ctrl+C stops only this instance.',flush=True)
        rc=process.wait()
        stop=state/'stop-request.json'
        requested=stop.exists() and json.loads(stop.read_text()).get('pid')==process.pid
        if rc and not requested:raise RuntimeError('Server ended unexpectedly; inspect server.stderr.txt. Data retained.')
    except KeyboardInterrupt:
        process.send_signal(__import__('signal').CTRL_BREAK_EVENT)
        try:process.wait(timeout=10)
        except subprocess.TimeoutExpired:process.terminate();process.wait(timeout=10)
    finally:out.close();err.close()

if __name__=='__main__':
    try:main()
    except Exception as e:print('Demo could not start: '+str(e),file=sys.stderr);sys.exit(1)

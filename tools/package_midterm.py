"""Package pinned, committed midterm files without rewriting presentations.

Keeps original relative paths, checks the local web assets, creates a full history
bundle, restores it, and checks every ZIP member after extraction. No teacher
scripts, runtime databases, credentials, dependencies or untracked drafts enter.
"""
from pathlib import Path, PurePosixPath
from datetime import datetime,timezone,timedelta
import argparse,hashlib,json,re,subprocess,zipfile
from urllib.parse import unquote,urlsplit

def sha(b):return hashlib.sha256(b).hexdigest()
def run(*args,cwd):return subprocess.check_output(args,cwd=cwd,stderr=subprocess.STDOUT)
def main():
    ap=argparse.ArgumentParser();ap.add_argument('--output-root',type=Path,required=True);args=ap.parse_args()
    root=Path(__file__).resolve().parents[1]
    now=datetime.now(timezone(timedelta(hours=8)))
    if now.strftime('%m-%d') in ('10-22','11-17'):raise SystemExit('Reserved presentation day; packaging not allowed')
    out=args.output_root.absolute()
    if out.exists():raise SystemExit('Use a new output directory')
    parent=out.parent.resolve()
    if parent==root or root in parent.parents:raise SystemExit('Output must stay outside the project')
    def git(*a,cwd=root):return run('git',*a,cwd=cwd)
    if git('diff','HEAD','--name-only'):raise SystemExit('Commit tracked changes before packaging')
    head=git('rev-parse','HEAD').decode().strip()
    rows=[]
    for row in git('ls-tree','-rz',head).split(b'\0'):
        if row:
            meta,path=row.split(b'\t',1);mode,kind,s=meta.decode().split();assert kind=='blob'
            rows.append((path.decode(),s))
    required=['大作业报告.docx','提交索引.md','要做什么.md','怎么做.md','怎么算做对.md','测试记录.md','运行说明.md','实际运行结果.txt','选题自查表.md','决定记录.md','AGENTS.md','材料清单.md','启动Demo.cmd','测试Demo.cmd','中期汇报.pptx','中期汇报.pdf','中期汇报讲稿.md','中期汇报网页版/index.html','backend/go.mod','frontend/package.json','tests/test_prepare.py']
    paths={p for p,s in rows}
    for p in required:
        if p not in paths:raise SystemExit('Required committed midterm material missing: '+p)
    def allowed(p):
        q=PurePosixPath(p)
        if q.is_absolute() or '..' in q.parts:return False
        if q.parts[0]=='资料' and len(q.parts)>1 and q.parts[1].startswith('中期汇报基线-'):return False
        if any(x in q.parts for x in ['node_modules','dist','.backup','.working','private-auth','data','artifacts','.runtime','.state']):return False
        if q.name.startswith(('.env','initialization-','.hook-trust')):return False
        if p=='.codex/config.toml':return False
        return q.suffix.lower() in {'.go','.sql','.py','.ps1','.cmd','.json','.jsonl','.toml','.md','.ts','.tsx','.js','.mjs','.cjs','.yaml','.yml','.css','.html','.txt','.docx','.pptx','.pdf','.jpg','.jpeg','.png'} or q.name in ['go.mod','go.sum','.gitignore','.gitattributes']
    selected=[(p,s) for p,s in rows if allowed(p)]
    unique=list(dict.fromkeys(s for p,s in selected))
    data= subprocess.run(['git','cat-file','--batch'],input=('\n'.join(unique)+'\n').encode(),stdout=subprocess.PIPE,cwd=root,check=True).stdout
    blobs={};pos=0
    for s in unique:
        end=data.index(b'\n',pos);h,k,n=data[pos:end].split();n=int(n);pos=end+1
        b=data[pos:pos+n];pos+=n+1
        assert h.decode()==s and hashlib.sha1(b'blob '+str(n).encode()+b'\0'+b).hexdigest()==s
        blobs[s]=b
    assert pos==len(data)
    out.mkdir(parents=True);stage=out/'中期材料';stage.mkdir()
    inventory={}
    for p,s in selected:
        b=blobs[s]
        if Path(p).suffix.lower() not in {'.jpg','.png','.jpeg','.pptx','.pdf','.docx'}:
            if re.search(rb'gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9]{20,}|-----BEGIN (?:RSA |EC )?PRIVATE KEY-----',b):raise SystemExit('Credential pattern found in '+p)
        dest=stage/p;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(b)
        inventory[p]={'bytes':len(b),'sha256':sha(b),'git_blob_sha':s}
    # Verify all submitted presentation bytes against both pinned Git and the live originals.
    presentation_paths=[p for p,s in selected if p in ['中期汇报.pptx','中期汇报.pdf','中期汇报讲稿.md'] or p.startswith('中期汇报网页版/')]
    for p in presentation_paths:
        assert (stage/p).read_bytes()==(root/p).read_bytes(),p
    html=(stage/'中期汇报网页版/index.html').read_text(encoding='utf-8')
    web_checks=[]
    for attr,ref in re.findall(r'(src|data-src|data-orig)=["\']([^"\']+)["\']',html):
        if ref.startswith(('http:','https:','data:','#')):continue
        rel=unquote(urlsplit(ref).path)
        # The deck treats data-orig as repository-root paths; src/data-src are deck-relative.
        target=(stage/rel) if attr=='data-orig' else (stage/'中期汇报网页版'/rel)
        if not target.is_file():raise SystemExit(f'Missing local web reference {attr}={ref}')
        web_checks.append({'attribute':attr,'reference':ref,'sha256':sha(target.read_bytes())})
    for day in sorted((root/'每日工作记录').iterdir()):
        if not day.is_dir() or not re.fullmatch(r'\d{4}-\d{2}-\d{2}',day.name):continue
        start=datetime.fromisoformat(day.name).replace(tzinfo=timezone(timedelta(hours=8)))
        b=git('log','--since='+start.isoformat(),'--until='+(start+timedelta(days=1)).isoformat(),'--format=%h %cI %s')
        p='每日工作记录/'+day.name+'/提交记录.txt';(stage/p).write_bytes(b);inventory[p]={'bytes':len(b),'sha256':sha(b),'derived_from':head}
    bundle=stage/'项目存档.bundle'
    subprocess.run(['git','bundle','create',str(bundle),'--all'],cwd=root,check=True,capture_output=True)
    verify=subprocess.run(['git','bundle','verify',str(bundle)],cwd=root,check=True,capture_output=True,text=True)
    restored=out/'历史还原核验'
    subprocess.run(['git','clone','--quiet',str(bundle),str(restored)],check=True,capture_output=True)
    assert git('rev-parse','HEAD',cwd=restored).decode().strip()==head
    subprocess.run(['git','fsck','--full'],cwd=restored,check=True,capture_output=True)
    native_count=int(git('rev-list','--count','HEAD'))
    assert int(git('rev-list','--count','HEAD',cwd=restored))==native_count
    inventory['项目存档.bundle']={'bytes':bundle.stat().st_size,'sha256':sha(bundle.read_bytes()),'native_commits':native_count}
    manifest={'kind':'midterm_materials_v1.2','actual_time':now.isoformat(),'source_head':head,'native_commit_count':native_count,'presentation_content_changed':False,'presentation_files':presentation_paths,'web_local_references_verified':web_checks,'files':inventory,'excluded_tracked_paths':[p for p,s in rows if not allowed(p)],'untracked_files_not_used':[p for p in git('ls-files','--others','--exclude-standard','-z').decode().split('\0') if p],'excludes':['teacher originals','official raw workbook and private CSV exports','runtime databases and authentication material','dependencies and caches','historical presentation baselines (available in Git bundle)','unadopted presentation drafts'],'note':'Current midterm materials, not a final acceptance or a claim of personal rehearsal. No invented test or submission status. Manifest excludes its own bytes; ZIP hash is in the external receipt.'}
    (stage/'中期打包清单.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    archive=out/('软件编程-中期-20251101030-王麒皓-'+now.strftime('%Y%m%d-%H%M%S')+'.zip')
    with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
        for p in sorted(stage.rglob('*')):
            if p.is_file():z.write(p,'中期材料/'+p.relative_to(stage).as_posix())
    unpack=out/'解包核验';unpack.mkdir()
    with zipfile.ZipFile(archive) as z:
        assert z.testzip() is None
        for p in z.namelist():
            q=PurePosixPath(p);assert not q.is_absolute() and '..' not in q.parts
        z.extractall(unpack)
    for p,e in inventory.items():assert sha((unpack/'中期材料'/p).read_bytes())==e['sha256'],p
    assert (unpack/'中期材料/中期打包清单.json').read_bytes()==(stage/'中期打包清单.json').read_bytes()
    for p in presentation_paths:assert sha((root/p).read_bytes())==inventory[p]['sha256']
    receipt={'actual_time':datetime.now(timezone(timedelta(hours=8))).isoformat(),'kind':manifest['kind'],'source_head':head,'native_commits_restored':native_count,'zip':str(archive),'zip_bytes':archive.stat().st_size,'zip_sha256':sha(archive.read_bytes()),'bundle':str(bundle),'bundle_bytes':bundle.stat().st_size,'bundle_sha256':sha(bundle.read_bytes()),'bundle_verify_exit':verify.returncode,'bundle_restore_and_fsck':'passed','archive_integrity_and_all_file_hashes':'passed','presentation_all_bytes_unchanged':True,'presentation_hashes':{p:inventory[p]['sha256'] for p in presentation_paths},'web_reference_count':len(web_checks),'file_count':len(inventory)+1,'course_universal_20MiB_limit':False,'local_submission_or_email_sent':False}
    (out/'中期打包回执.json').write_text(json.dumps(receipt,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    receipt_copy=root/'.backup-receipts'/('midterm-'+now.strftime('%Y%m%d-%H%M%S')+'.json');receipt_copy.parent.mkdir(exist_ok=True);receipt_copy.write_text(json.dumps(receipt,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    print(json.dumps(receipt,ensure_ascii=False,indent=2))
if __name__=='__main__':main()

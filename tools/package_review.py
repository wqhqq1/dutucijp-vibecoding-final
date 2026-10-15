"""Create a pinned, allowlisted nine-category review package; never a fake freeze.

No teacher scripts are executed. Reads committed Git blobs only. Runtime secrets,
source data, dependencies, untracked changes and previous bundles cannot enter it.
"""
from __future__ import annotations
import argparse, datetime as dt, hashlib, json, os, subprocess, zipfile
from pathlib import Path, PurePosixPath

def digest(p):
    h=hashlib.sha256()
    with p.open('rb') as f:
        for b in iter(lambda:f.read(1024*1024),b''):h.update(b)
    return h.hexdigest()

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--output-root',type=Path,required=True); args=ap.parse_args()
    now=dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))
    if now.strftime('%m-%d') in ('10-22','11-17'):raise SystemExit('Reserved day: do not create or repair packages')
    project=Path(__file__).resolve().parents[1]
    output=args.output_root.absolute()
    if output.exists():raise SystemExit('Output must be a new directory')
    # Resolve the existing parent before creating anything, including redirected Windows runtime paths.
    parent=output.parent.resolve()
    if parent==project or project in parent.parents:raise SystemExit('Package must stay outside project')
    output.mkdir(parents=True); stage=output/'review';stage.mkdir()
    def git(*command,where=project):return subprocess.check_output(['git',*command],cwd=where,stderr=subprocess.STDOUT)
    head=git('rev-parse','HEAD').decode().strip()
    files=[n for n in git('ls-tree','-rz','--name-only',head).decode('utf-8').split('\0') if n]
    allowed_suffixes={'.go','.sql','.py','.ps1','.json','.toml','.md','.ts','.tsx','.css','.html','.txt','.docx','.pptx','.jpg','.jpeg','.png'}
    rejected=[]; mapping={}
    def safe(name):
        path=PurePosixPath(name)
        return not path.is_absolute() and '..' not in path.parts and not any(x in path.parts for x in ('node_modules','dist','.backup','.working','private-auth','data','artifacts')) and path.name not in ('config.toml','master.key') and not path.name.startswith(('.env','initialization-','.hook-trust')) and (path.suffix.lower() in allowed_suffixes or path.name in ('go.mod','go.sum','.gitignore','.gitattributes'))
    for name in files:
        if not safe(name):rejected.append(name);continue
        p=PurePosixPath(name)
        if name=='大作业报告.docx': target='01-报告/'+p.name
        elif name=='中期汇报.pptx': target='02-PPT/'+p.name
        elif p.parts[0]=='每日工作记录':target='04-每日记录/'+name
        elif name in ('要做什么.md','怎么做.md','怎么算做对.md'):target='05-三份规格/'+p.name
        elif name=='选题自查表.md':target='06-自查/'+p.name
        elif name=='决定记录.md':target='07-决定记录/'+p.name
        elif name=='AGENTS.md' or p.parts[0] in ('.agents','.codex'):target='08-项目规矩/'+name
        elif name in ('材料清单.md','提交索引.md'):target='09-材料清单/'+p.name
        elif p.parts[0] in ('backend','frontend','tools','tests') or len(p.parts)==1:target='09-材料清单/交付源码/'+name
        else:rejected.append(name);continue
        data=git('show',head+':'+name)
        dest=stage/target;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(data);mapping[target]=name
    for expected in ('01-报告/大作业报告.docx','02-PPT/中期汇报.pptx','09-材料清单/材料清单.md'):
        if expected not in mapping:raise SystemExit('Required committed material missing: '+expected)
    bundle=stage/'03-Git历史/项目存档.bundle';bundle.parent.mkdir()
    subprocess.run(['git','bundle','create',str(bundle),'--all'],cwd=project,check=True,capture_output=True)
    verify=subprocess.run(['git','bundle','verify',str(bundle)],cwd=project,check=True,capture_output=True,text=True)
    (output/'bundle-verify.txt').write_text(verify.stdout+verify.stderr,encoding='utf-8')
    restore=output/'restored-history'
    clone=subprocess.run(['git','clone','--no-hardlinks',str(bundle),str(restore)],check=True,capture_output=True,text=True)
    restored=git('rev-parse','HEAD',where=restore).decode().strip()
    if restored!=head:raise SystemExit('Restored HEAD differs from pinned source')
    fsck=subprocess.run(['git','fsck','--full'],cwd=restore,check=True,capture_output=True,text=True)
    (output/'restore-check.txt').write_text(clone.stdout+clone.stderr+fsck.stdout+fsck.stderr+f'\nsource={head}\nrestored={restored}\n',encoding='utf-8')
    parts=[]
    if bundle.stat().st_size>8*1024*1024:
        with bundle.open('rb') as f:
            for i,b in enumerate(iter(lambda:f.read(8*1024*1024),b''),1):
                part=bundle.with_name(bundle.name+f'.part{i:03}');part.write_bytes(b);parts.append({'name':str(part.relative_to(stage)).replace('\\','/'),'sha256':digest(part)})
        # Full bundle remains in the archive for direct local restoration; parts support plugin upload limits.
    daily=sorted({PurePosixPath(n).parts[1] for n in files if n.startswith('每日工作记录/') and len(PurePosixPath(n).parts)>2})
    pending=['本人三角色首次强密码及真机Passkey/TOTP绑定、登录与恢复码保存','本人亲跑测试、重启技能、可信Hook实际deny、上下文与完整演练','八个真实日期记录：当前只有'+','.join(daily),'NFR02未达标；NFR04以逐请求实际结果为准','本人邮件发送和辅导截图/插件备份，另一台机器验证另列最终待做']
    manifest={'actual_time':now.isoformat(),'kind':'review_only_not_frozen','source_head':head,'nine_categories':[p.name for p in sorted(stage.iterdir()) if p.is_dir()],'pending':pending,'excluded_tracked_paths':rejected,'mapping':mapping,'files':{str(p.relative_to(stage)).replace('\\','/'):{'bytes':p.stat().st_size,'sha256':digest(p)} for p in sorted(stage.rglob('*')) if p.is_file()},'bundle_parts':parts,'note':'Manifest excludes itself. ZIP hash and restore/unpack receipt are external, with no circular self-hashes. No teacher originals or runtime files are included.'}
    (stage/'09-材料清单/manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf-8')
    archive=output/('软件编程-中期审阅-20251101030-王麒皓-'+now.strftime('%Y%m%d-%H%M%S')+'.zip')
    with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
        for p in sorted(stage.rglob('*')):
            if p.is_file():z.write(p,str(p.relative_to(stage)).replace('\\','/'))
    unpack=output/'unpacked';unpack.mkdir()
    with zipfile.ZipFile(archive) as z:
        assert z.testzip() is None
        for name in z.namelist():
            target=PurePosixPath(name)
            if target.is_absolute() or '..' in target.parts:raise SystemExit('Unsafe archive member')
        z.extractall(unpack)
    for name,entry in manifest['files'].items():
        if digest(unpack/name)!=entry['sha256']:raise SystemExit('Unpack hash mismatch: '+name)
    receipt={'actual_time':now.isoformat(),'source_head':head,'restored_head':restored,'bundle_sha256':digest(bundle),'bundle_verify_exit':verify.returncode,'fsck_exit':fsck.returncode,'unpack_all_hashes_equal':True,'zip':str(archive),'zip_bytes':archive.stat().st_size,'zip_sha256':digest(archive),'under_20MiB':archive.stat().st_size<=20*1024*1024,'file_count':len(manifest['files'])+1,'kind':manifest['kind'],'pending':pending}
    (output/'receipt.json').write_text(json.dumps(receipt,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps(receipt,ensure_ascii=False,indent=2))
    if not receipt['under_20MiB']:raise SystemExit('Archive exceeds course 20MiB check; do not send as one attachment')

if __name__=='__main__':main()

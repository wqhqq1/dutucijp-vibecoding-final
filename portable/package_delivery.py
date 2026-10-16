"""Add only the verified portable distribution to a pinned midterm source package."""
from pathlib import Path
import argparse,hashlib,json,shutil,subprocess,sys,zipfile
from datetime import datetime,timezone,timedelta

def digest(p):
    with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--portable',type=Path,required=True);ap.add_argument('--output',type=Path,required=True);args=ap.parse_args()
    project=Path(__file__).resolve().parents[1];out=args.output.resolve();app=args.portable.resolve()
    assert not out.exists() and not out.is_relative_to(project)
    p=subprocess.run([sys.executable,str(project/'tools/package_midterm.py'),'--output-root',str(out)],cwd=project,check=True,capture_output=True)
    base=json.loads((out/'中期打包回执.json').read_text(encoding='utf-8'));stage=out/'中期材料'
    app_manifest=json.loads((app/'portable-manifest.json').read_text(encoding='utf-8'))
    for name,item in app_manifest['files'].items():
        relative=Path(name)
        assert not relative.is_absolute() and '..' not in relative.parts and not any(v in relative.parts for v in ['private-auth','__pycache__','logs'])
        assert relative.suffix.lower() not in ['.sqlite','.db','.xlsx','.key']
        source=app/name;assert digest(source)==item['sha256'],name
        target=stage/'teacher-demo'/name;target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(source,target)
    shutil.copyfile(app/'portable-manifest.json',stage/'teacher-demo/portable-manifest.json')
    guide='''# 请先阅读 教师独立运行版

解压整个“中期材料”文件夹到可写目录，双击根目录“启动Demo.cmd”。它直接使用teacher-demo中的程序和Python，不要求安装Codex、Go、Node或Python，也不执行PowerShell策略绕过。首次必须联网从UCI官方取得完整数据，至少保留8GiB空闲磁盘，等待下载、准备、对账完成后在浏览器配置全新finance账户。强密码、Passkey或TOTP实际验证、保存恢复码均不能跳过。其他角色入口位于teacher-demo文件夹。详见teacher-demo/教师启动说明.md。

本包不含作者的数据库、账户和认证秘密，不复制作者Passkey。默认localhost:8080，端口冲突报错，不停止其他软件。个人预留日限制不再阻止老师使用产品。另机、真人设备或离线的验证范围以本次交付验收记录为准；首次联网要求不能隐去。

报告、PPT、演示PDF、网页版、讲稿和原图保持既有版本。网页版实机按钮仍指向localhost:8080；PPT是静态播放备份。早期报告和工作记录按当时版本保留，独立启动入口以后续本说明为准，未改写既往结果。源码与完整历史仍在包内，教师不必先编译源码。

本包是隔离分支交付，未合并到原main，未修改开发会话的工作目录。提交索引和中期打包清单给出固定版本与文件散列；新生成的账号、原件与数据库位于老师自己的应用数据目录，不属于提交物。打包不等于已发课程邮件。
'''
    (stage/'00-教师独立运行说明.md').write_bytes(guide.encode())
    manifest=json.loads((stage/'中期打包清单.json').read_text(encoding='utf-8'))
    inventory={p.relative_to(stage).as_posix():{'bytes':p.stat().st_size,'sha256':digest(p)} for p in sorted(stage.rglob('*')) if p.is_file() and p.name!='中期打包清单.json'}
    manifest.update({'kind':'midterm-with-teacher-portable-demo','files':inventory,'portable':'teacher-demo','first_start_requires_internet':True,'private_runtime_included':False,'initial_runtime_source':'download official UCI workbook, validate hash, prepare all, reconcile into a new database','support':'Windows x64; same-host clean-profile tests; no different physical computer claim'})
    (stage/'中期打包清单.json').write_bytes((json.dumps(manifest,ensure_ascii=False,indent=2)+'\n').encode())
    now=datetime.now(timezone(timedelta(hours=8)))
    archive=out/('软件编程-中期-教师独立运行-20251101030-王麒皓-'+now.strftime('%Y%m%d-%H%M%S')+'.zip')
    with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
        for p in sorted(stage.rglob('*')):
            if p.is_file():z.write(p,'中期材料/'+p.relative_to(stage).as_posix())
    unpack=out/'教师版解包核验';unpack.mkdir()
    with zipfile.ZipFile(archive) as z:
        assert z.testzip() is None;z.extractall(unpack)
    for name,item in inventory.items():assert digest(unpack/'中期材料'/name)==item['sha256'],name
    for name,expected in base['presentation_hashes'].items():assert digest(unpack/'中期材料'/name)==expected,name
    # Check launcher/manifest through the actual relocated package, without creating an account or database.
    check=subprocess.run([str(unpack/'中期材料/teacher-demo/python/python.exe'),str(unpack/'中期材料/teacher-demo/portable/launcher.py'),'check'],check=True,capture_output=True)
    receipt={**base,'kind':manifest['kind'],'zip':str(archive),'zip_bytes':archive.stat().st_size,'zip_sha256':digest(archive),'file_count':len(inventory)+1,'portable_manifest_sha256':digest(stage/'teacher-demo/portable-manifest.json'),'relocated_package_check_exit':check.returncode,'first_start_requires_internet':True,'private_runtime_included':False,'teacher_package_contains_executable_and_python':True,'source_only_intermediate_zip':base['zip'],'actual_time':now.isoformat()}
    (out/'教师独立交付回执.json').write_bytes((json.dumps(receipt,ensure_ascii=False,indent=2)+'\n').encode())
    print(json.dumps(receipt,ensure_ascii=False,indent=2))
if __name__=='__main__':main()

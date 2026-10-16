"""Build a Windows x64 portable delivery, excluding all original user state."""
from pathlib import Path
import argparse,hashlib,json,shutil,subprocess,urllib.request,zipfile
from datetime import datetime,timezone,timedelta

ROOT=Path(__file__).resolve().parents[1]
URL='https://www.python.org/ftp/python/3.12.10/python-3.12.10-embed-amd64.zip'
FILES=[('openpyxl-3.1.5.whl','https://files.pythonhosted.org/packages/c0/da/977ded879c29cbd04de313843e76868e6e13408a94ed6b987245dc7c8506/openpyxl-3.1.5-py2.py3-none-any.whl','5282c12b107bffeef825f4617dc029afaf41d0ea60823bbb665ef3079dc79de2'),('et_xmlfile-2.0.0.whl','https://files.pythonhosted.org/packages/c1/8b/5fe2cc11fee489817272089c4203e679c63b570a5aaeb18d852ae3cbba6a/et_xmlfile-2.0.0-py3-none-any.whl','7a91720bc756843502c3b7504c77b8fe44217c85c537d85037f0f536151b2caa')]
def digest(p):
    with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def fetch(url,p):
    if not p.exists():
        with urllib.request.urlopen(url,timeout=120) as src,p.open('xb') as dst:shutil.copyfileobj(src,dst)

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--output',type=Path,required=True);ap.add_argument('--binary',type=Path,required=True);args=ap.parse_args()
    out=args.output.resolve();assert not out.exists() and not out.is_relative_to(ROOT)
    cache=ROOT/'.working/portable-dependencies';cache.mkdir(parents=True,exist_ok=True)
    py=cache/'python-3.12.10-embed-amd64.zip';fetch(URL,py)
    # Official release-page checksum plus pinned HTTPS source; SHA-256 is recorded for redistribution.
    assert hashlib.md5(py.read_bytes()).hexdigest()=='fe8ef205f2e9c3ba44d0cf9954e1abd3'
    out.mkdir(parents=True)
    with zipfile.ZipFile(py) as z:z.extractall(out/'python')
    for name,url,sha in FILES:
        p=cache/name;fetch(url,p);assert digest(p)==sha
        with zipfile.ZipFile(p) as z:z.extractall(out/'python/Lib/site-packages')
    (out/'python/python312._pth').write_bytes(b'python312.zip\n.\nLib/site-packages\n../project/tools\nimport site\n')
    for folder in ['app','portable','project/tools']:(out/folder).mkdir(parents=True,exist_ok=True)
    shutil.copyfile(args.binary,out/'app/recon.exe')
    shutil.copyfile(ROOT/'portable/launcher.py',out/'portable/launcher.py')
    for name in ['fetch_data.py','prepare.py']:shutil.copyfile(ROOT/'tools'/name,out/'project/tools'/name)
    for label,action in [('启动Demo','start'),('停止Demo','stop'),('新建独立Demo','new'),('配置仓库账号','start --account warehouse'),('配置管理员账号','start --account admin')]:
        content='@echo off\r\nsetlocal\r\n"%~dp0python\\python.exe" "%~dp0portable\\launcher.py" '+action+' %*\r\nif errorlevel 1 pause\r\nendlocal\r\n'
        (out/(label+'.cmd')).write_bytes(content.encode('ascii'))
    notice='''# 教师独立Demo Windows x64

解压到本机可写目录，双击“启动Demo.cmd”。不需要安装Codex、Go、Node或Python，不读取作者的数据库或账号。首次必须联网：从UCI官方下载完整工作簿，核对已锁定SHA-256，再准备两个年度全部数据、配对并创建新数据库。视网络和电脑性能需要数分钟，至少预留8GiB磁盘空间（验收中全量加首年度运行状态已超过3GB）；失败时保留诊断，不把失败库当完成。首次默认all，并非一万行缩小演示。

首次浏览器进入finance配置页面，由使用者设置强密码，实际绑定Passkey或TOTP，并保存恢复码后才开放业务。双击“配置仓库账号.cmd”或“配置管理员账号.cmd”分别打开另两角色的配置入口，已完成配置则进入正常登录，不重置账号。初始化链接保存在本机新运行目录的private-auth文件夹，只用于本人配置，不能上传或转发。未触碰账号的过期链接会在再次启动时更新。其他角色不能沿用作者设备的Passkey。新建独立Demo.cmd创建全新状态，保留旧状态，不清空旧库；服务运行中须先停止，不能切换丢失旧实例。

数据默认保存在当前Windows用户LOCALAPPDATA/dutucijp-teacher-demo，全部属于这次独立运行。程序默认localhost:8080，端口被其他程序占用则明确报错，不结束别人进程。可在终端运行启动Demo.cmd --port 18080进行隔离验证，此时实机网页的固定8080链接不适用；正式主讲仍用默认8080。停止Demo.cmd只结束已核实的本次实例，保留数据。窗口内Ctrl+C也可结束服务。

首次准备成功后，查询、核查、派单、结案、CSV和重新计算可使用本机缓存，Python及其依赖已经随包提供；删除运行目录或选择新建时需重新从官方获取数据。此包不含作者原数据库、密码、会话、Passkey私钥、TOTP密钥或恢复码。首次配置依赖使用者的实际验证设备，自动化合成凭据测试不等于真人设备操作。

只声明Windows x64支持；macOS/Linux未验证。无管理员权限和无外部依赖的同机隔离验证见交付核对记录，另一台物理机器仍需实际验证，不能凭打包声称通过。学生的预留日工作安排不限制老师的合法首次配置和运行。PPT、PDF、网页和讲稿按原件保留，不由此工具重建。

数据：UCI Online Retail II，DOI 10.24432/C5CG6D，CC BY 4.0。完整原件首次从官方URL获取，不由本包重新发布。Python 3.12.10来自python.org的官方Windows embeddable包；保留其LICENSE.txt。openpyxl和et_xmlfile来自已锁定PyPI wheel，其许可证及元数据随包保留。软件源代码和实际交付基线见交付材料与Git bundle。
'''
    (out/'教师启动说明.md').write_bytes(notice.encode('utf-8'))
    files={p.relative_to(out).as_posix():{'bytes':p.stat().st_size,'sha256':digest(p)} for p in sorted(out.rglob('*')) if p.is_file()}
    manifest={'kind':'teacher-portable-windows-x64','actual_time':datetime.now(timezone(timedelta(hours=8))).isoformat(),'first_start_requires_internet':True,'official_data_sha256':'bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980','python_distribution':{'url':URL,'sha256':digest(py),'official_release_page':'https://www.python.org/downloads/release/python-31210/'},'files':files,'private_runtime_included':False}
    (out/'portable-manifest.json').write_bytes((json.dumps(manifest,ensure_ascii=False,indent=2)+'\n').encode())
    subprocess.run([str(out/'python/python.exe'),'-c','import openpyxl,sqlite3,ssl; print("Bundled runtime ready",openpyxl.__version__)'],check=True)
    print(json.dumps({'portable':str(out),'files':len(files),'python_sha256':digest(py)},ensure_ascii=False))
if __name__=='__main__':main()

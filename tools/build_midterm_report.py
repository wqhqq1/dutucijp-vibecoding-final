"""Fill the retained course template; preserve every package part except body XML.

Run with the bundled authoring Python. Render with the documents skill afterwards.
The template is a local teacher original and must not be added to Git.
"""
from __future__ import annotations
import argparse, copy, datetime as dt, hashlib, json, subprocess, zipfile
from pathlib import Path
from lxml import etree as E

W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
N = {'w': W}
def q(s): return '{'+W+'}'+s
def sha(b): return hashlib.sha256(b).hexdigest()

def main():
    a=argparse.ArgumentParser(); a.add_argument('--template',type=Path,required=True); a.add_argument('--output',type=Path,required=True); a.add_argument('--work',type=Path,required=True); args=a.parse_args()
    now=dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))
    if now.strftime('%m-%d') in ('10-22','11-17'): raise SystemExit('Reserved day: no material creation')
    root=Path(__file__).resolve().parents[1]
    original=args.template.read_bytes(); args.work.mkdir(parents=True,exist_ok=True)
    (args.work/'retained-reference.docx').write_bytes(original)
    with zipfile.ZipFile(args.template) as z:
        parts={x.filename:z.read(x.filename) for x in z.infolist()}; info=z.infolist()
    xml=E.fromstring(parts['word/document.xml']); body=xml.find('w:body',N)
    paras=body.findall('w:p',N); tables=body.findall('w:tbl',N); section=copy.deepcopy(body.find('w:sectPr',N))
    (args.work/'reference-parts.json').write_text(json.dumps({n:{'bytes':len(b),'sha256':sha(b),'editable':n=='word/document.xml'} for n,b in parts.items()},indent=2),encoding='utf-8')
    def settext(p,text):
        keep=p.find('w:pPr',N)
        rp=p.find('w:r/w:rPr',N)
        for e in list(p):
            if e is not keep:p.remove(e)
        r=E.SubElement(p,q('r'))
        if rp is not None:r.append(copy.deepcopy(rp))
        t=E.SubElement(r,q('t')); t.set('{http://www.w3.org/XML/1998/namespace}space','preserve'); t.text=str(text)
    def paragraph(text,role=8,newpage=False):
        p=copy.deepcopy(paras[role]); settext(p,text)
        if newpage:
            pp=p.find('w:pPr',N)
            if pp is None:pp=E.SubElement(p,q('pPr'))
            E.SubElement(pp,q('pageBreakBefore'))
        body.append(p)
    def table(rows,pattern=0):
        t=copy.deepcopy(tables[pattern]); source=t.findall('w:tr',N)
        for r in source:t.remove(r)
        cols=len(rows[0]); grid=t.find('w:tblGrid',N)
        widths=[int(x.get(q('w'))) for x in grid] if grid is not None else []
        if len(widths)!=cols:
            if grid is not None:t.remove(grid)
            grid=E.SubElement(t,q('tblGrid'))
            for _ in range(cols):E.SubElement(grid,q('gridCol')).set(q('w'),str(8730//cols))
        for i,row in enumerate(rows):
            r=copy.deepcopy(source[0 if i==0 else min(1,len(source)-1)])
            cells=r.findall('w:tc',N)
            while len(cells)<cols:r.append(copy.deepcopy(cells[-1])); cells=r.findall('w:tc',N)
            for extra in cells[cols:]:r.remove(extra)
            for c,text in zip(cells,row):
                ps=c.findall('w:p',N)
                for extra in ps[1:]:c.remove(extra)
                settext(ps[0],text)
            # Never split an evidence/status row across pages.
            pr=r.find('w:trPr',N)
            if pr is None:pr=E.SubElement(r,q('trPr'))
            E.SubElement(pr,q('cantSplit'))
            t.append(r)
        body.append(t)
    for e in list(body):body.remove(e)
    paragraph('退货对账与差异处理系统中期报告',0)
    paragraph('软件编程　机国际2502　20251101030　王麒皓　'+now.strftime('%Y-%m-%d'),1)
    paragraph('一、选题与背景',5)
    paragraph('财务需要把取消明细与在先销售核对，仓库需要接收差异并留下结论。系统替代逐行筛选和口头派单，保留歧义与原工作表行号。公开交易来自UCI Online Retail II（Daqing Chen，2009—2011，CC BY 4.0）。配对是规则推断，账户、派单及结论是课程模拟。')
    paragraph('二、需求',16)
    short=['来源、许可及散列','benchmark/year1/all与真实进度','六条清洗规则及源行','接纳、排除和守恒','保守候选与歧义说明','唯一候选及累计余量','筛选、分页、证据','同版本运行幂等','财务或管理员派单','重复及并发派单拒绝','顺序流转及关闭后只读','责任人人工确认并重验','非空结论或资料不足结案','首次强密码及Passkey/TOTP、角色权限','前后状态及源证据审计','财务/管理员筛选CSV','四项性能底线','复现、恢复与历史材料']
    table([['编号','系统应当支持的行为','类型']]+[[f'FR-{i:02}',s,'权限' if i in (14,16) else ('异常' if i in (5,6,8,10,11,12,13) else '常规')] for i,s in enumerate(short,1)])
    paragraph('NR-01至04：不做真实退款支付、税务记账、物流联动或统计推荐。完整不可重排需求见《要做什么.md》。')
    paragraph('三、设计与技术选型',21,True)
    paragraph('五模块依次为数据准备、自动对账、差异工作流、认证授权、查询与导出。Python只读Excel并生成带清单CSV，Go使用Gin及SQLite，React/TypeScript/Vite负责界面。原始字段、规则证据与业务操作分别保存，界面不能提交可信角色。')
    table([['表组','关键字段','用途'],['datasets / source_rows / normalized_rows','source_hash、sheet、row、scope','版本与清洗追溯'],['runs / decisions / allocations / balances','run、cancel、sale、remaining','保守匹配与数量守恒'],['cases / audit_events','owner、status、version、before/after','派单、确认及关闭事务'],['auth_users / credentials / sessions / auth_audit','hash、ciphertext、scope、version','认证因素、受限恢复和撤销']],2)
    paragraph('决定：2026-10-14沿用Go＋SQLite以便本机复现，未选远程服务；10-15使用保守候选而非最近时间猜原单；10-15将完整认证与业务写事务二次校验绑定，避免恢复撤销后旧请求继续提交。详见《决定记录.md》。')
    paragraph('四、实现情况',32)
    table([['FR','实际状态（截至本报告）','仍需验收'],['01—06、08','CLI/官方year1及all功能实跑通过','不主张真实原订单准确率'],['07','服务端50/最多100分页、候选与证据已测','本人界面筛选'],['09—13','派单、状态、人工确认、结论与并发回滚通过','本人完整流程'],['14','三角色密码、双方式、恢复及可信会话隔离测试通过','真机Passkey/认证器'],['15—16','业务/认证审计及筛选CSV隔离测试通过','本人导出核对'],['17','NFR01/03有三次实测，NFR02未通过','NFR04见最新性能记录'],['18','启动、隔离演示、技能/Hook定义及分支试验已实现','重启技能、可信Hook、另机']],3)
    paragraph('运行：./tools/bootstrap.ps1；./build.ps1；./prepare-demo.ps1 -SourceDatabase <已发布库>；./start.ps1 -Database <新演示库> -Binary <已构建程序>。私有初始化链接只在运行目录，首次配置由本人操作；完整命令见《运行说明.md》。')
    paragraph('五、测试与验收',38,True)
    paragraph('自动化使用真实临时SQLite及隔离凭据，虚拟Passkey经过WebAuthn库验证真实签名。集成回归71条Go顶层通过，0失败/0跳过；Python13条通过。输入、预期、实际、命令与每项TC状态见当天《FR12-18执行记录.md》和《测试记录.md》。')
    table([['用例','实际输入及预期','结果'],['TC-16/17','124行100+24分页，123候选100+23；源证据','通过后端'],['TC-19/20/21/24','重复派单、12并发、跳级/逆向/空白结论','通过'],['TC-22/23','两差异各退4争原量6；审计故障注入','1成功，余2；故障全回滚'],['TC-25/26/27','伪造身份、他人证据、仓库CSV、筛选导出','越权拒绝，授权通过'],['TC-33—48','密码边界、QR门禁、挑战、UV、重放、恢复、限流','自动覆盖通过；设备项待本人']],4)
    paragraph('本人待验收：三类账户先强密码，再实际验证因素并保存恢复码；两种方式分别绑定/登录；亲跑不少于10条测试；安全演示正常和异常流程。未做部分不计通过。二维码、OTP和恢复码不截图。')
    paragraph('官方all：1,067,371原始＝1,038,818接纳＋28,553排除；19,164取消＝9,714自动配对＋6,275疑似＋3,175无合格原单。清洗cr-1.0.1、规则conservative-1.0.0，源xlsx SHA前缀bcbe73b35f5b。相同数量或匹配率不能证明原订单真值。')
    table([['性能指标','底线','本轮实际'],['NFR-01 空状态一万行三次','每次≤5秒','2.001661 / 1.711716 / 1.830999秒，通过'],['NFR-02 空状态官方all','每次≤90秒，需三次','198.841174秒，未通过；不补造其余轮'],['NFR-03 输入已有强制重算三次','每次≤15秒','14.492502 / 14.864326 / 14.281526秒，通过'],['NFR-04 重任务十路≥300 HTTP','零失败，P95≤500ms','逐请求实测见性能记录，不以未测标通过']],5)
    paragraph('六、开发过程',47,True)
    head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
    count=subprocess.check_output(['git','rev-list','--count','HEAD'],cwd=root,text=True).strip()
    paragraph(f'生成报告时主线快照{head[:7]}，{count}次提交。真实开发日期仅10-14、10-15。典型提交：e950765来源环境；ed2db45文本编号清洗；b6f7331事务工作流。新代码以FR开头提交，最终数量读实际Git。')
    paragraph('AGENTS约束来源、日期与秘密；run-app技能固定入口；已授权子代理独立测认证、数量及性能。危险命令Hook定义及隔离策略检查已准备，重启调用和本人信任后的真实deny尚待。真实可丢弃分支79ff9b2已提交并比较，因仅证明canary而不合入业务，保留历史。')
    paragraph('本执行对话发生自动上下文压缩，前后占用数字未提供；本人手动上下文管理仍待做。代拟任务和AI代码、报告制作均如实标来源，代理运行不写成本人亲手完成。')
    paragraph('七、问题与解决',57)
    paragraph('第一例：Windows运行环境层ACL初始化实际失败（P1）。认证测试检出SID格式错误，随后改为原生受保护DACL并清除显式其他主体授权；加入Everyone授权再验证，最终只保留当前用户访问。失败与复验JSONL均保留。')
    paragraph('第二例：恢复逻辑层允许另一密码客户端借recovering状态重新绑定（P1）。独立审查发现，限制只有原有效恢复会话可重新绑定，并在同事务二次检查写权限；并发撤销后的派单/确认均拒绝。')
    paragraph('第三例：前端构建环境层TypeScript7拒绝CSS副作用导入（P2）。实际build输出TS2882，补Vite类型声明后重建通过。另检出首页301循环，修为直接返回嵌入HTML，回归及实际HTTP200通过。')
    paragraph('八、成本统计',65)
    table([['项目','数值与来源'],['token / 费用','本轮未获得可归属账单，待本人从实际用量界面核对'],['设备 / 软件','本机Windows；Go1.27.2，Python3.12.10，SQLite3.53.4；版本回执在原始输出'],['性能优化预算','实际两轮：缓存；批量SQL及有限LRU。停止第三轮，不隐藏全量未达标']],6)
    paragraph('九、遗留问题与后续计划',67,True)
    table([['事项','实际状态','计划'],['认证与演示','本人真机、恢复码保存、三角色流程未做','10-21前本人完成'],['工程动作','技能重启、可信Hook实际deny、亲跑测试及上下文待做','10-21前本人留证'],['性能','all完整管线198.84秒未达90秒；本阶段停止优化','按10-23起最终阶段排查'],['材料与冻结','仅两个真实日期，缺八日记录；本人演练/发送未做','不造日期，不宣布冻结'],['另机与费用','另一台机器、真实费用来源待核','最终11-16前核验']],7)
    paragraph('10-20初稿、10-21中期收口及11-16最终冻结的计划保留。当前提前于真实10-15制作审阅材料，不能据此声称未来工作已发生。10-22和11-17仅使用事先准备版本，不执行修补、安装或改包。邮件由本人发送。')
    paragraph('十、自查',69)
    # Copy the current seven-row self-check exactly; this stays traceable to a real source file.
    rows=[]
    for line in (root/'选题自查表.md').read_text(encoding='utf-8-sig').splitlines():
        if line.startswith('|') and not line.startswith('| ---'):
            row=[x.strip() for x in line.strip('|').split('|')]
            if len(row)==4:rows.append(row)
    table(rows,8)
    paragraph('证据索引：《提交索引.md》《材料清单.md》及每日工作记录/2026-10-15/原始输出。最新状态以对应原始输出及执行交接为准。提交包仅供审阅，尚未满足本人动作和真实日期的冻结要求。')
    body.append(section)
    parts['word/document.xml']=E.tostring(xml,xml_declaration=True,encoding='UTF-8',standalone=True)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    if args.output.exists():raise SystemExit('Use a new output path; do not overwrite a reviewed artifact')
    with zipfile.ZipFile(args.output,'w',zipfile.ZIP_DEFLATED) as z:
        for item in info:z.writestr(item,parts[item.filename])
    with zipfile.ZipFile(args.output) as z:
        preserved=[n for n,b in parts.items() if n!='word/document.xml' and z.read(n)==b]
        assert len(preserved)==len(parts)-1
    contract=f'''# Retained report template contract
Reference: {args.template.absolute()}
SHA-256: {sha(original)}
Retained copy: {args.work/'retained-reference.docx'}
Page system: one A4 section; exact sectPr copied unchanged. Top/bottom 2.54cm, left 3.000375cm, right 2.599972cm.
Typography/components: clone original title paragraph0, metadata1, section headings5/16/21/32/38/47/57/65/67/69, body8 and nine original tables. Preserve each source pPr/rPr and table/cell properties. No generic style pack.
Slot map: word/document.xml/body paragraphs and table rows are editable; template instructions and empty slots removed. Rows may be cloned. Ten sections and original order preserved. Five page starts intentionally specified for requested 3–5-page midterm. Long lists use explicit external evidence cross-references.
Preserve-only: ALL package parts other than word/document.xml byte-identical, including styles, theme, numbering, core properties, relationships, headers/footers and opaque data. sectPr byte-structure unchanged. Baseline inventory reference-parts.json.
Fidelity gates: render retained template and final with skill render_docx.py; visually inspect every page; verify 3–5 pages and no clipping. Capture actual reference page count in render receipt. Unsupported personal cost/actions remain pending. Original teacher file hash must remain {sha(original)}.
'''
    (args.work/'artifact.md').write_text(contract,encoding='utf-8')
    print(json.dumps({'output':str(args.output.absolute()),'template_sha256':sha(original),'output_sha256':sha(args.output.read_bytes()),'preserved_package_parts':len(preserved),'total_parts':len(parts),'render_status':'required'},ensure_ascii=False))

if __name__=='__main__':main()

"""Refresh adopted midterm documents from saved evidence; never edit slides or package."""
from pathlib import Path
from datetime import datetime, timezone, timedelta
import hashlib, json, re, shutil, subprocess

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.working/midterm-refresh-20261018'
DAY = ROOT / '每日工作记录/2026-10-18/中期材料刷新'
OUT.mkdir(parents=True, exist_ok=True)
DAY.mkdir(parents=True, exist_ok=True)
NOW = datetime.now(timezone(timedelta(hours=8)))
HEAD = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip()
COUNT = int(subprocess.check_output(['git', 'rev-list', '--count', 'HEAD'], cwd=ROOT))
def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()
def load(name):
    return json.loads((ROOT / name).read_text(encoding='utf-8-sig'))
def write(name, content):
    path = ROOT / name
    path.parent.mkdir(parents=True, exist_ok=True)
    previous = OUT / 'before' / name
    if path.exists() and not previous.exists():
        previous.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, previous)
    path.write_text(content.rstrip() + '\n', encoding='utf-8', newline='\n')

protected = [ROOT / n for n in ['中期汇报.pptx', '中期汇报.pdf', '中期汇报讲稿.md']]
protected += [p for p in (ROOT / '中期汇报网页版').rglob('*') if p.is_file()]
before_slides = {p.relative_to(ROOT).as_posix(): digest(p) for p in protected}
before_sources = subprocess.check_output(['git', 'diff', 'HEAD', '--name-only', '--', 'backend', 'frontend', 'portable'], cwd=ROOT)
assert before_sources == b'', 'Software changed before document refresh'
reg = load('每日工作记录/2026-10-18/批量复核实现/regression-summary.json')
batch = load('每日工作记录/2026-10-18/批量复核实现/TC55官方all隔离HTTP-通过.json')
build = load('每日工作记录/2026-10-18/批量处理优化/自动刷新建议构建.json')
assert (reg['go_top_level_passed'], int(reg['python_tests_ran']), int(reg['frontend_passed'])) == (130, 25, 6)
assert batch['source_unchanged'] and batch['after_counts']['allocations'] == 14951
assert build['tests_run'] is False

report = '''# 退货对账与差异处理中期报告

软件编程大作业  VC-R-03  课程要求 v1.2

王麒皓  20251101030  机国际2502  2026-10-18

## 一 选题与背景

本项目帮助财务和仓库核查无法直接关联原销售的取消记录。当前已实现两年度全量准备、首轮保守配对、单条和批量复核、权限审计及网页查询。本次中期材料更新到10月18日的软件成果；软件本人验收、最新设备操作和最终课程交付仍未完成。

数据来自UCI Online Retail II，DOI 10.24432/C5CG6D，CC BY 4.0。两个年度工作表中的Invoice、StockCode、Quantity、InvoiceDate、Price、Customer ID、Country等是原始事实；配对、推荐、角色、派单、结论和审计是本系统建立的记录。系统不执行真实退款，不接入支付或物流。

## 二 需求

沿用18项FR编号，最新条文及范围见《要做什么.md》。表中表示已实现的行为，自动测试、模拟页面和真实设备验收的边界另列，不将实现等同全部验收。

| 编号 | 当前系统行为 |
| --- | --- |
| FR-01 | 核验官方原件出处、许可和散列 |
| FR-02 | 支持benchmark、year1、all及真实阶段进度 |
| FR-03 | 清洗并保留源行、类型和排除原因 |
| FR-04 | 展示接纳、排除、分类及数量守恒 |
| FR-05 | 首轮多候选不自动选；另提供待人工核查的建议 |
| FR-06 | 首轮唯一合格候选才自动配对，不跨销售分摊 |
| FR-07 | 条件检索、稳定排序、分页、中文原因及批量工作台 |
| FR-08 | 相同输入幂等，保留决策证据和处理历史 |
| FR-09 | 单条或跨页批量派单，只使用服务端可信身份 |
| FR-10 | 拒绝重复派单，冲突或他人负责项明确跳过 |
| FR-11 | 待处理、处理中、已关闭顺序流转，闭单只读 |
| FR-12 | 人工确认或批量复核重验版本与共享余量 |
| FR-13 | 非空结论后关闭，可预览共同结论的批量处理 |
| FR-14 | 首次强密码加实际因素；选择Passkey后识别账户 |
| FR-15 | 成功业务与审计同事务，保存复核策略和批次 |
| FR-16 | 财务或管理员导出全部筛选CSV，保持精度和安全 |
| FR-17 | 保留全量与并发底线，新增功能诊断单列 |
| FR-18 | 提供从零运行、失败记录、源码与原生开发历史 |

## 三 设计与技术选型

Python读取官方工作簿并准备可追溯记录；Go/Gin负责规则、认证、事务和HTTP；SQLite保存来源、余额、核查与审计；React/TypeScript只展示服务端授权后的分页结果。选用现有Go方案，FastAPI、Rust、C#和Java曾作为备选，未做同条件后端横评，不声称固定速度倍数。

| 对象 | 职责和限制 |
| --- | --- |
| 来源与准备 | 文件散列、工作表、源行、清洗及排除原因，原件只读 |
| 运行与分配 | 首轮分类、精确数量/金额、唯一取消分配与销售余量 |
| 推荐与批次 | review-1.0.0快照、预览和共享资源组件，人工确认后生效 |
| 工单与账户 | 归属、状态、版本、审计、服务端会话及受限恢复 |

首轮conservative-1.0.0要求客户、商品、精确单价、国家/地区相同，销售严格在先且余量足够；多候选和缺身份不自动确认。一条取消不跨销售分摊。买10退4可成立，累计退量不能超过可退容量。

新增review-1.0.0与首轮分开：可提出当前唯一、八字段完全重复销售归组、整单关联和最近一笔建议；最近时间并列不预选。经人工复核确认的重复组以最早来源为代表，只计一次容量且保留全部来源。整单或共享余量组件原子提交，容量冲突整组跳过，其他组件继续；不能用先提交者抢余量或重写初始自动率。

## 四 实现情况与实际结果

清洗版本cr-1.0.1。C前缀识别取消，排除有主要原因；缺客户保留核查，同表疑似重复保留，跨表副本按出现次数对应。精确价格参与判定，显示舍入不改变规则。

| 范围 | 原始行 | 接纳行 | 排除行 | 首轮配对取消行 |
| --- | --- | --- | --- | --- |
| benchmark | 10,000 | 9,972 | 28 | 只作独立一万行基准 |
| year1 | 525,461 | 521,770 | 3,691 | 5,214 / 10,205 |
| all | 1,067,371 | 1,038,818 | 28,553 | 9,714 / 19,164 |

all接纳取消19,164 = 首轮配对9,714 + 疑似6,275 + 当前规则下无合格原单3,175；初始差异9,450，覆盖率50.69%。原始可识别取消19,494 = 接纳19,164 + 排除330；其他无法识别单号的排除不猜作取消。year1覆盖率51.09%。该统计是首轮结果，数据没有真实原订单关联标签，覆盖率不是准确率。

官方all隔离副本中的合成财务复核批次选择5,451条，5,237成功、214跳过、0事务失败。成功包括唯一8、重复归组56、整单1,601、最近3,572；不同建议覆盖可重叠，不能相加当准确率。当前分配从9,714增至14,951，初始分类不变；这些是隔离模拟审核成果，不是本人或真实企业审核。

网页已补七种原因中文、原因筛选、候选对比和确认结果持久展示；批量工作台支持跨页选择、预览分类、跳过原因、结果恢复、隐藏已关闭项。15:12构建新增打开工作台与提交后自动刷新建议，该末次修改只构建、未重新测试。

## 五 测试与验收

10月18日12:44一次统一回归为130项Go、25项Python、6项前端，共161项，0失败/0跳过。后续已关闭隐藏修订有131项Go和6项前端通过的保存记录，另复跑3项相关Go测试；Python25项复用。去重覆盖可计162项，但不是一次162项统一回归，也不能称15:12末次版本已全测。

| 场景 | 输入与预期 | 实际结果与证据 |
| --- | --- | --- |
| 主流程和异常 | year1 0:182派单、开始、非空关闭；重复派单和空结论拒绝 | 旧实录版本1→2→3→4，3条审计，无配对；10-16 v12中期复核图05/06/08/10 |
| 首轮歧义与数量 | 多候选不自动挑；买10退4余6；超量不写分配 | 原规则回归通过，engine_test.go及10-15实测 |
| 原因筛选和CSV | 七原因白名单；非法原因拒绝；分页不截断完整导出 | 10-17第二轮HTTP补充通过，原始输出见前端设计优化目录 |
| 重复组共享容量 | 单条与批量并发不能把同一笔重复容量多次使用 | TC49—54事务、回滚、闭单只读及权限专项通过 |
| 跨页批量与重放 | 冻结选择/方案，冲突组跳过，成功项四步审计；重试不重复 | 官方all隔离5,237成功/214跳过/0失败；增加20,948条审计，重放无新增 |
| Passkey选择账户 | 不预填账户，签名和handle确定实际账户；错挑战/签名/来源拒绝 | 三角色合成ES256协议与HTTP边界通过；真实设备选择流程待本人验证 |

单条旧实录保留原时点，不代表新实例当前状态。10月18日按本人授权曾另建未设置的独立all实例，旧实例留存；旧Passkey证据不能代替新实例配置。新实例的真实强密码、Passkey/TOTP、恢复码保存和软件认可仍由本人完成，不采集秘密。

| 性能项目 | 最近已有证据 | 结论和边界 |
| --- | --- | --- |
| NFR01一万行 | 10-17单次1.7444502秒 | ≤5秒底线；不是10-18新测 |
| NFR02全量首次 | 10-17单次67.3385582秒 | ≤90秒；45秒优选未达 |
| NFR03强制重算 | 同核心候选3单次5.5834541秒 | ≤15秒；5秒优选未达 |
| NFR04后台查询 | 10路300次0失败，整体P95 149.0449ms | ≤500ms；UK类别304.451ms，非每类≤200ms |
| TC55新增批量 | 推荐2.1699672秒、预览1.0450447秒、提交13.0402186秒 | all隔离一次；4路300次查询P95 52.322ms，非正式NFR04 |

原10月16日三轮正式底线记录继续保留；10月17日单次、同核心复用和10月18日局部诊断分开引用。TC55查询用独立合成认证库，不能代表同库写会话或页面可见耗时。最新原因筛选及末次自动刷新未重测全量性能。

## 六 开发过程与AI使用

截至本次资料刷新，源码基线为__HEAD__，main共__COUNT__个原生提交。关键成果包括6ead220性能与首次引导、b9d8d8d原因筛选、bc97763推荐批量复核、eb499f9工作台修订及ac298f6自动刷新；完整原生历史在最终选定版本打包时更新。

Codex用于代码、测试、排错和材料核对，Claude Code参与前端与PPT。本人提出需求和方案选择，历史三角色Passkey由本人操作；代理的协议、模拟页面和隔离审核不计作本人审核。当前PPT继续交Claude，报告、运行与证据先刷新，PPT完成后再统一打包。

## 七 问题与解决

全量首次曾198.84秒，并发300次有228次失败；通过实际诊断调整批次、索引和读写路径，后续达到底线，但45秒/5秒优选差距继续披露。交易日期筛选单次页面从1210ms至309ms，包含自动化与观察开销，不能当稳定P95。

新增推荐查询首次CPU超过122秒未完成；改为取消键驱动及明确身份索引后，all隔离生成约2.17秒。只读诊断还曾因普通路径mode=ro未被驱动采用而在关闭时checkpoint，源物理SHA变化，该轮记失败。改为file URI并检查驱动只读，最终复验源SHA前后一致；十张业务表逻辑散列一致不能把旧物理失败改写成通过。

## 八 成本统计

项目完整token及货币费用没有可核对的全量记录，记未提供。已有单轮用量只代表该轮，不外推总成本。环境沿用Windows x64、Go 1.27.2、Python 3.12.10、Node 24.18.0及锁定依赖；测试、截图或提交数量均不代替项目质量。

## 九 遗留问题与后续计划

先验收当前软件和新实例设备登录，必要时只补实际受影响检查；不重做已充分通过的测试或继续无边界追优。PPT由Claude继续更新，完成后核对演示与这些中期材料的版本，再生成包含软件和材料的完整中期包。当前尚未打包、冻结或发送，10月22日不安排补工。

软件本人认可后再整理最终报告、测试与运行说明、最终汇报演示和完整最终包，最迟11月16日冻结，11月17日只用准备好的版本。恢复、物理另机、真实候选确认与实际预演没有新结果时如实保留，不伪填完成。

## 十 自查与材料入口

本中期报告与同源Markdown已刷新，规格、设计、规则和用例沿用最新文件。提交索引指向report/rules/tests/test_source/run/source，以及实际结果和待Claude更新的汇报材料。完整测试命令与证据在《测试记录.md》和真实工作记录中；启动方式见《运行说明.md》。本文是中期更新，不是最终报告或最终验收结论。
'''.replace('__HEAD__', HEAD[:7]).replace('__COUNT__', str(COUNT))
write('中期项目报告.md', report)

# Preserve the established report's A4 typography, tables and heading ladder.
builder = (ROOT / 'tools/build_midterm_current_report.py').read_text(encoding='utf-8')
prefix = builder.split("para('退货对账与差异处理中期报告','Title')", 1)[0]
scope = {'__file__': str(ROOT / 'tools/build_midterm_current_report.py'), '__name__': '__midterm_layout__'}
exec(compile(prefix, 'existing_midterm_layout', 'exec'), scope)
doc = scope['doc']
para, heading, table = scope['para'], scope['heading'], scope['table']
from docx.shared import Pt
doc.styles['Normal'].font.size = Pt(11)
doc.styles['Normal'].paragraph_format.line_spacing = 1.15
lines = report.strip().splitlines()
i = 0
while i < len(lines):
    line = lines[i].strip()
    if not line:
        i += 1
        continue
    if line.startswith('# '):
        para(line[2:], 'Title')
    elif line.startswith('## '):
        title = line[3:]
        if title.startswith(('三 ', '五 ', '六 ')):
            scope['nextpage'] = True
        heading(title)
    elif line.startswith('|'):
        block = []
        while i < len(lines) and lines[i].strip().startswith('|'):
            block.append([c.strip() for c in lines[i].strip().strip('|').split('|')])
            i += 1
        headers, rows = block[0], block[2:]
        widths = {2: [2.0, 15.2], 3: [3.0, 6.2, 8.0], 5: [2.3, 3.0, 3.0, 3.0, 5.9]}[len(headers)]
        table(headers, rows, widths, 9.5)
        continue
    else:
        para(line, 'Subtitle' if line.startswith('软件编程大作业') else None)
    i += 1
original_doc = OUT / 'before/大作业报告.docx'
if not original_doc.exists():
    shutil.copyfile(ROOT / '大作业报告.docx', original_doc)
doc.save(ROOT / '大作业报告.docx')

actual = f'''VC-R-03 中期实际运行结果
刷新日期：2026-10-18，Asia/Shanghai；源码基线{HEAD}，不是本次新跑结果。
最新构建：{build['source_commit']}，SHA-256 {build['sha256']}。
末次自动刷新建议只构建、未测试；正式服务按原交接保持停止。

一 官方数据与首轮分类
UCI Online Retail II，DOI 10.24432/C5CG6D，CC BY 4.0。
原件SHA-256 bcbe73b35f5b7babf197fb0cb983a11f5d9ff929078d4aa53d171b1f2df2e980。
清洗cr-1.0.1，首轮规则conservative-1.0.0，人工推荐review-1.0.0。
all：1,067,371原始 = 1,038,818接纳 + 28,553排除。
接纳取消19,164 = 9,714首轮配对 + 6,275疑似 + 3,175当前规则下无合格原单。
覆盖率9,714/19,164约50.69%，不是准确率；初始差异9,450。
year1：525,461 = 521,770 + 3,691；取消10,205 = 5,214 + 2,699 + 2,292。
原始可识别取消19,494 = 接纳19,164 + 排除330；无法识别单号的排除不猜作取消。
上述为首轮固定口径，人工复核不重写初始分类；桥接原证据为每日工作记录/2026-10-17/优选目标续作/收尾NFR02/NFR02-1/trial.json。

二 官方all隔离批量复核
5,451条选择，5,237成功、214跳过、0事务失败。
跳过：容量不足139、未明确选择74、整单方案未完整一致1。
成功：唯一8、重复归组56、整单1,601、最近3,572。
分配9,714→14,951，关闭1→5,238，审计3→20,951，新增5,237×4条审计。
初始分类与summary散列不变，重放无新增；91个重复组、187个来源成员保留。
生成2.1699672秒、预览1.0450447秒、提交13.0402186秒。
4路300次HTTP均成功、均在提交窗口开始，P95 52.322毫秒。
边界：官方数据隔离副本、明确合成财务复核；查询使用独立合成认证库，不代表本人审核、同库写会话、真实原单准确率或正式NFR04。
证据：每日工作记录/2026-10-18/批量复核实现/TC55官方all隔离HTTP-通过.json及TC55官方全量隔离验证说明.md。

三 自动测试及末次版本边界
10-18 12:44统一130 Go+25 Python+6前端=161，0失败/0跳过。
后续修订Go131及前端6通过，另复验3项相关Go；Python25复用。
去重覆盖162项来自不同轮次，不写一次统一162项；15:12自动刷新只构建、未测试。
模拟浏览器结果不替代真实设备；新实例的实际Passkey选择、恢复及软件本人认可待验证。

四 既有性能结果
10-17单次NFR01 1.7444502秒、NFR02 67.3385582秒；同核心候选3 NFR03 5.5834541秒。
NFR04十路300次0失败，整体P95 149.0449毫秒，UK类别304.451毫秒。
底线5/90/15秒和500毫秒有证据；45秒首次、5秒重算优选未达，部分分类未达200毫秒。
原10-16三轮正式结果保留，10-18新增批量和末次自动刷新不冒称重新通过整套NFR。
证据：每日工作记录/2026-10-17/优选目标续作/收尾与首次引导.md及对应原始输出。

五 运行与交付状态
完整运行目录中双击启动Demo.cmd；源码检出需先构建并准备teacher-demo，不能当现成安装包。
首次使用者需强密码、实际Passkey/TOTP及恢复码保存；设备和恢复秘密不随材料分发。
10-18已按本人授权另建独立未设置实例，旧账户历史留在旧实例；不以旧Passkey证据代替新实例配置。
PPT继续由Claude更新，本次只刷新其他中期材料，尚未打包、冻结或发送。
'''
write('实际运行结果.txt', actual)

test_note = '''## 2026-10-18中期材料采用的最新测试摘要

仅整理已有证据，本次没有重跑测试。12:44一次统一回归130 Go＋25 Python＋6前端=161，0失败/0跳过，见[原始统计](每日工作记录/2026-10-18/批量复核实现/regression-summary.json)。后续工作台修订保存131 Go和6前端通过记录，并有3项相关Go复验；Python25项复用，去重覆盖162项但不是同轮162项统一回归。15:12自动刷新建议仅构建，未测试；详见[最新构建](每日工作记录/2026-10-18/批量处理优化/自动刷新建议构建.json)。

| 需求或规则 | 输入与预期 | 实际结果 | 证据及测试源 |
| --- | --- | --- | --- |
| TC49—54 推荐和重复归组 | 精确八字段/类型、最近并列、整单、共享容量；未确认不占量 | 隔离专项通过，并列不预选、冲突组回滚、其他组继续 | [实现交接](每日工作记录/2026-10-18/批量复核实现/实现与交接.md)，backend/internal/store/review_batch_test.go |
| TC55 官方all端到端 | 冻结5,451条，预览→提交→四步审计→重放 | 5,237成功、214跳过、0事务失败；初始分类不改、重放不新增 | [原始聚合](每日工作记录/2026-10-18/批量复核实现/TC55官方all隔离HTTP-通过.json)，backend/cmd/reviewverify/main.go |
| FR-11 已关闭只读 | 新快照/分组不列闭单，全部闭单预览拒绝 | 相关store和HTTP检查通过，非法状态422 | [修订与边界](每日工作记录/2026-10-18/批量处理优化/批量工作台修订-已关闭隐藏与动画.md)，backend/internal/web/batch_view_test.go |
| FR-14 可发现Passkey | 不先填账户，核验实际credential归属；错签名/handle/Origin/重放拒绝 | 三角色合成ES256协议和HTTP边界通过，真实设备待本人 | [验证记录](每日工作记录/2026-10-18/Passkey选择登录/修复与Demo重置.md)，backend/internal/auth及web测试 |
| FR-07/16 原因筛选导出 | 未知原因拒绝，分页不截断完整CSV | 已配置合成身份四入口422，三条取消完整导出 | [10-17集成](每日工作记录/2026-10-17/前端设计优化/Codex第二轮集成结果.md)，backend/internal/web/transaction_query_test.go |
| 主流程和边界旧实录 | 0:182派单→开始→非空关闭；重复派单、空结论拒绝 | 原时点v1→v4、3条审计、无新配对；不冒称当前新实例已亲跑 | 每日工作记录/2026-10-16/截图/v12中期复核/ |

TC55的4路300次查询P95 52.322ms属于局部诊断且使用独立合成认证库，不能替代正式NFR04同口径结果。旧NFR和真实页面证据保持原时点，新增功能与末次自动刷新没有整套性能复测。原始失败、修复和验证方法见对应记录；所有模拟审核不记作本人审核。

## 以下为此前测试记录

'''
old_tests = (ROOT / '测试记录.md').read_text(encoding='utf-8')
if '## 以下为此前测试记录\n' in old_tests:
    old_tests = '# 测试记录\n\n' + old_tests.split('## 以下为此前测试记录\n', 1)[1].lstrip()
write('测试记录.md', '# 测试记录\n\n' + test_note + old_tests.split('\n', 1)[1].lstrip())

run = '''# 运行说明

更新日期2026-10-18。本文件说明当前中期软件的启动和从零准备；PPT由Claude继续更新，完整中期包尚未重新制作。当前构建来源ac298f6，15:12构建已更新本机teacher-demo，末次自动刷新修改未测试。旧运行和测试事实见每日工作记录，不使用历史PID或旧个人库路径作为启动前提。

## 使用完整可运行目录

目标平台Windows x64。完整提交目录应包含teacher-demo/app/recon.exe、随包Python、portable启动器、必要项目脚本及portable-manifest.json。

1. 解压到可写目录，保留相对目录，双击根目录“启动Demo.cmd”。只有源码时会提示缺少teacher-demo，不会读取作者个人数据库。
2. 首次需要联网从UCI官方取得原件、核验固定SHA，并独立准备两年度all，至少8GiB空闲空间；依赖由完整运行目录提供。失败会保留诊断，半成品不当完成库使用。
3. 打开http://localhost:8080。未设密码账户从页面卡片开始；依次设置强密码、实际验证自己的Passkey或TOTP、确认保存恢复码后进入业务。入口无15分钟限制，配置会话仍有限时；已有密码账户正常登录续办。
4. 默认“选择Passkey登录”不必先填账户，设备所选凭据决定账户；旧非可发现凭据可用指定账户兼容入口。不同独立实例需要各自绑定，不保证旧实例Passkey能登录新实例。
5. 查看交易、差异和配对；筛选原因、国家/地区、客户、商品或日期，排序、翻页并由财务导出CSV。批量工作台先生成建议、选择/排除、冻结预览，再明确提交。最近并列不预选；共享容量冲突明确跳过；闭单只读。
6. “停止Demo.cmd”只停止匹配实例，保留状态；以后重启沿用该状态。另建独立状态使用teacher-demo/新建独立Demo.cmd，不删除旧实例。端口占用报错，不自动结束其他进程。

隔离端口示例：`./tools/launch-demo.ps1 -Port 18080`。必须一直使用该端口的localhost地址；不要直接在新端口复用旧实例的认证或选择未知状态。完整先决条件和实例规则见[教师独立运行说明](教师独立运行说明.md)。本次资料刷新不启动服务、不创建账户、不重建数据。

## 从源码准备软件

源码布局为backend/、frontend/、tools/、tests/、portable/及根目录脚本。当前架构Go/Gin/SQLite、React/TypeScript/Vite和Python，锁定版本见tools/environment.lock.json、backend/go.mod/go.sum、frontend/package-lock.json。

```powershell
# 项目根目录，在非预留执行日准备环境和构建
./tools/bootstrap.ps1
./build.ps1
# 使用上一步实际输出的recon.exe绝对路径，输出必须为新的源码外目录
python portable/build_bundle.py --binary <recon.exe路径> --output <新的独立运行目录>
```

`python`须为已准备且具备锁定依赖的项目运行Python；不要把命令中的路径占位符直接复制执行。build_bundle创建软件目录，不替代报告、汇报与最终统一打包。正式完整中期包会放入相对teacher-demo目录；在PPT完成前不提前运行打包命令。

## 测试和实际结果

双击“测试Demo.cmd”或执行`./test.ps1`使用隔离夹具，不替换正式库。10-18一次统一为130 Go＋25 Python＋6前端通过；之后Go131/前端6及相关3项复验与Python复用分别有证据。15:12仅构建，不将旧通过冒称末次软件全测。资料刷新不重跑测试。说明和结果见[测试记录](测试记录.md)、[实际运行结果](实际运行结果.txt)。

课堂可演示一个主流程和一个异常/边界，例如派单→开始→非空结案与空结论拒绝。新设备安全配置、真实候选确认/恢复、物理另机及本人软件验收只按实际操作登记，不由模拟用例代替。密码、会话、OTP、二维码、恢复码和数据库不随源码或材料分发。

## 时间和当前边界

当前本机Demo根据15:12交接保持停止，需使用时自行启动。10-18曾按本人授权改选全新未设置实例，旧个人记录留在旧实例；这里只说明该事件，不预填当前账号状态或登录完成。PPT完成后统一检查软件/材料版本并打包；10-21前准备好中期，10-22不开发、补测或改材料。11-16前完成最终冻结，11-17不补工。产品自身正常启动和使用者安全配置不受开发预留日脚本约束。
'''
write('运行说明.md', run)

index = '''# 中期提交索引

更新2026-10-18，VC-R-03，王麒皓、20251101030、机国际2502，课程要求v1.2。当前仅非演示材料已刷新；PPT由Claude继续完成，现有演示文件仍为旧版，不作为已更新或冻结证明。统一中期包将在PPT完成后生成，当前没有新ZIP或发送回执。

| 核心类别 | 当前位置 | 状态 |
| --- | --- | --- |
| report | [大作业报告.docx](大作业报告.docx)、[同源中期报告](中期项目报告.md) | 更新到10-18；中期报告，非最终报告 |
| rules | [要做什么](要做什么.md)、[怎么做](怎么做.md)、[决定记录](决定记录.md) | 首轮保守规则与人工推荐分开；数量、权限和闭单只读明确 |
| tests | [测试记录](测试记录.md)、[验收定义](怎么算做对.md) | 输入、预期、实际和边界已索引；最新末次构建未测试明确保留 |
| test_source | backend/、tests/、frontend/tests/ | 实际Go/Python/前端测试及隔离夹具，不以截图替代测试源码 |
| run | [运行说明](运行说明.md)、[教师独立运行说明](教师独立运行说明.md) | 从零准备与完整目录启动分开，不依赖作者库 |
| source | backend/、frontend/、tools/、portable/及根目录脚本 | 当前源码基线__HEAD__，保持原相对目录 |
| result | [实际运行结果](实际运行结果.txt) | 初始三类/覆盖率、隔离批量成果、性能及限制分开 |
| slides | 中期汇报.pptx、PDF、网页版和讲稿 | 待Claude完成；本轮不编辑或导出这些文件 |

## 关键证据

- [推荐与批量实现](每日工作记录/2026-10-18/批量复核实现/实现与交接.md)、[官方all验证边界](每日工作记录/2026-10-18/批量复核实现/TC55官方全量隔离验证说明.md)、[聚合结果](每日工作记录/2026-10-18/批量复核实现/TC55官方all隔离HTTP-通过.json)。
- [Passkey选择与新实例](每日工作记录/2026-10-18/Passkey选择登录/修复与Demo重置.md)、[最新批量工作台修订](每日工作记录/2026-10-18/批量处理优化/最新Claude修订合并.md)、[末次仅构建回执](每日工作记录/2026-10-18/批量处理优化/自动刷新建议构建.json)。
- [原性能收尾及限制](每日工作记录/2026-10-17/优选目标续作/收尾与首次引导.md)、[原真实页面证据](每日工作记录/2026-10-16/截图/v12中期复核/)。模拟页面截图不能替代真实设备或本人审核。
- [本轮刷新记录](每日工作记录/2026-10-18/中期材料刷新/刷新与交接.md)、[材料清单](材料清单.md)、[PPT完成后的打包入口](中期打包说明.md)。

## 打包时保留的索引映射

```text
report=大作业报告.docx
rules=要做什么.md
tests=测试记录.md
test_source=backend
test_source_python=tests
test_source_frontend=frontend/tests
run=运行说明.md
result=实际运行结果.txt
source=.
slides=中期汇报.pptx
demo_start=启动Demo.cmd
demo_runtime=teacher-demo
bundle=项目存档.bundle
```

此映射表示后续包内布局，不冒称运行目录或bundle已被放入新包。源码与材料按白名单纳入，排除数据库、认证秘密、依赖缓存和课程原件；完整软件目录含必要运行依赖，不能误删随包Python。旧中期包保留，待全部完成后再按本人要求整体替换提交版本，不另交只有软件的压缩包。
'''.replace('__HEAD__', HEAD[:7])
write('提交索引.md', index)

write('材料清单.md', '''# 中期材料清单

更新2026-10-18。PPT继续由Claude处理，其余中期材料刷新至本次软件基线；待PPT完成再统一打包，不能称已冻结或已发送。

| 内容 | 位置 | 本轮状态 |
| --- | --- | --- |
| 中期报告 | 大作业报告.docx、中期项目报告.md | 更新两轮界面、批量复核、Passkey选择、数据/测试/性能边界；Word需以本轮版面核查回执为准 |
| 需求与关键规则 | 要做什么.md、怎么做.md、怎么算做对.md、决定记录.md | 采用10-18已实现规格；初始规则与人工推荐分开 |
| 实测与测试源 | 测试记录.md、backend/、tests/、frontend/tests/、每日工作记录/ | 最新统一161与跨轮去重162分别表述；15:12末次修改未测试，不虚填通过 |
| 运行与实际结果 | 运行说明.md、教师独立运行说明.md、实际运行结果.txt | 去除作者库依赖，当前软件/新实例、官方all、隔离批量和未验证项明确 |
| 源码与完整软件 | backend/frontend/tools/portable、根入口及teacher-demo | 源码和本机运行目录存在；新完整中期包尚未生成，不声称源码检出即可运行 |
| 汇报材料 | 中期汇报.pptx、PDF、网页版、讲稿 | 待Claude；本轮不编辑，不把旧版当最新成果 |
| 开发历史与过程 | Git、每日工作记录、AGENTS、自查表与决定记录 | 历史保留；待定稿时为选定提交生成完整bundle并纳入统一包 |
| 提交索引与包 | 提交索引.md、中期打包说明.md | 核心六类与运行结果可定位，PPT/统一打包/实际发送仍待 |

v1.2核心类别为report、rules、tests、test_source、run、source；结果和汇报材料要能定位。旧固定九类、八日记录、Hook或固定PPT页数不追加为统一课程门槛。保留原项目有效证据，缺少本人操作、真实设备、物理另机或课堂反馈时只记录缺口，不补造。
''')

write('中期打包说明.md', '''# 中期材料更新与后续打包

2026-10-18安排：PPT由Claude继续完成，其他中期材料先刷新；本轮不生成新包、不替换旧ZIP、不修改PPT/PDF/网页版/讲稿。完成状态见提交索引和本轮刷新记录。

## PPT完成后的统一处理

1. 接收Claude最终交接和演示文件，确认采用的PPT及必要PDF/网页版/讲稿，不并列多个冲突版本；按原要求不擅自修改已完成PPT内容。
2. 用本次最新版报告、规则、真实测试、实际结果和从零运行说明核对PPT数字，保留末次未测试、历史性能和模拟/本人操作的区别。若存在不一致，先形成具体修改点供Claude处理。
3. 固定一个材料与源码提交，使用最新teacher-demo完整可运行目录；该软件目录不包含作者业务库、账户或认证秘密。完整包同时包含软件、报告、汇报、规则、源码、测试、运行说明和提交索引，不能只单独交软件包。
4. 按白名单构造新的源码外暂存目录，保留相对路径与网页引用；更新项目存档.bundle、文件清单和散列。旧包保持，只有新版完整后再替换拟提交版本。
5. 只做一次必要的解包/路径/软件启动核对，复用已有无影响的测试。真实新设备安全配置由使用者完成，同机核查不冒称物理另机。
6. 记录实际压缩包路径、SHA、采用提交和仍待项；按老师公布的渠道和限制发送。没有真实回执就不标发送，v1.2未规定统一20MiB限制。

当前tools/package_midterm.py保留历史打包实现，不能仅复制源码而漏掉teacher-demo，也不能直接对整个工作目录递归打包。PPT未完成前不运行它或portable/build_bundle来提前生成新的提交交付目录。最终课程材料仍须等本人认可软件后再另行完成。
''')

write('资料/Claude中期PPT最新事实交接-20261018.md', f'''# 中期PPT更新所用事实

2026-10-18。本文件供本人转交Claude使用，没有自动向其他对话发送。PPT的设计与制作继续由Claude负责；本文只提供事实、限制和快速入口，不指定设计方向或方法。软件参考提交{HEAD[:7]}，报告与其他中期材料本轮先更新，PPT完成后再统一打包。

- 先读中期项目报告.md、实际运行结果.txt、提交索引.md和每日工作记录/2026-10-18/中期材料刷新/刷新与交接.md，采用其最新口径。
- 软件新增：七种差异原因中文与筛选、候选逐字段对比、确认结果持久展示、选择Passkey自动识别账户、review-1.0.0推荐及跨页批量工作台、预览分类/跳过原因、闭单隐藏、打开和提交后自动刷新建议。
- 首轮conservative-1.0.0仍保守；最近/整单/重复归组都是待人工复核的建议，不改初始自动率，不自动把最近订单当真实原单。重复归组只计一次容量，资源组件原子、冲突跳过、闭单只读。
- 官方all：1,067,371原始=1,038,818接纳+28,553排除；原始可识别取消19,494=接纳19,164+排除330；接纳取消19,164=9,714首轮配对+6,275疑似+3,175无合格原单，覆盖率50.69%，不是准确率。
- 官方all隔离批量：5,451选择，5,237成功/214跳过/0事务失败；分配总数14,951属于模拟人工复核后的隔离成果，不是首轮自动结果或本人审核。
- 测试：一次统一130 Go+25 Python+6前端=161；后续131 Go/6前端及3项复验与Python复用分开。去重162不是同轮；15:12自动刷新只构建、未测试。
- 历史NFR：1.7444502/67.3385582/5.5834541秒、十路300次P95 149.0449ms；45秒/5秒未达，UK类别304.451ms。TC55批量生成2.1699672秒、预览1.0450447秒、提交13.0402186秒及4路300次52.322ms是另一口径，不能替代正式NFR04。
- 新实例10-18按本人授权重新准备，旧Passkey配置证据不能代替新实例；真实设备、软件认可、恢复、物理另机和课堂发送没有记录时不得说完成。所有模拟截图须明确模拟，不新增截图任务或Hooks演示。
- 只调整本人交给你的汇报文件，不修改后端、前端或数据库；不要运行打包器。本辅导对话在收到完成交接后再统一打包，原包暂不替换。
''')

record = {'actual_time': NOW.isoformat(), 'source_head': HEAD, 'native_main_commits_at_start': COUNT,
          'presentation_hashes_before': before_slides,
          'presentation_hashes_after': {p.relative_to(ROOT).as_posix(): digest(p) for p in protected},
          'presentation_files_unchanged': all(digest(ROOT / n) == h for n, h in before_slides.items()),
          'software_sources_modified': False, 'software_tests_run': False,
          'package_created': False, 'final_materials_created': False,
          'report_sha256': digest(ROOT / '大作业报告.docx'),
          'latest_demo_sha256_from_saved_receipt': build['sha256'],
          'render_review': 'pending'}
assert record['presentation_files_unchanged']
assert subprocess.check_output(['git', 'diff', 'HEAD', '--name-only', '--', 'backend', 'frontend', 'portable'], cwd=ROOT) == before_sources
(DAY / '刷新核对.json').write_text(json.dumps(record, ensure_ascii=False, indent=2) + '\n', encoding='utf-8', newline='\n')
print(json.dumps({k: record[k] for k in ['source_head', 'presentation_files_unchanged', 'software_tests_run', 'package_created', 'report_sha256']}, ensure_ascii=False))

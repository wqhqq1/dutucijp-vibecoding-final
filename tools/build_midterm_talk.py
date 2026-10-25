"""Generate 中期汇报讲稿.md from the speaker notes in the web deck.

The spoken text of each page is copied verbatim from <aside class="notes"> (first
paragraph), so the talk, the presenter view and the PPTX notes cannot drift apart.
Durations are estimates at 4.3 spoken characters per second (digits counted one by
one, English words as two), not a rehearsal.

  python -I tools/build_midterm_talk.py
"""
from __future__ import annotations
import hashlib, html, re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECK = ROOT / '中期汇报网页版' / 'index.html'
OUT = ROOT / '中期汇报讲稿.md'
RATE = 4.3

QA = [
    ('为什么首轮不全自动配对？', '原始数据没有原单关联号。R-02 要求客户、商品、精确单价、国家/地区一致，销售严格在先、余量足够；FR-06 还要求候选唯一。像 0:1378 有两条合格候选，就留给人核查，不靠数量相等或先来后到挑一条。配错会把退款记到错误的销售上，比留给人看代价大。'),
    ('那整单、最近一笔这些建议算什么？', '它们是 review-1.0.0 的复核建议，和首轮 conservative-1.0.0 分开：不占余量、不改首轮分类，人工复核确认后才生效。最近时间并列不预选；八字段完全相同的重复销售归一组，只计一次容量，源行不删。'),
    ('50.69% 是准确率吗？', '不是。它是首轮自动配对覆盖率：all 范围 9,714 / 19,164（year1 为 5,214 / 10,205 ≈ 51.09%）。分母是接纳取消；原始可识别取消 19,494 = 接纳 19,164 + 清洗排除 330。没有真值标签，所以不能叫准确率。隔离副本上模拟复核后分配增到 14,951，也只是模拟复核成果，不是准确率。'),
    ('哪些是原始数据，哪些是你建的？', '交易行、八个原始列和源行号是官方事实；候选、自动配对、差异原因和复核建议是规则推断；派单、确认、结论、批量复核和审计是课程模拟的业务记录，由演示账户或隔离合成账户操作产生。'),
    ('批量复核会不会把余量用超？', '不会按先到先得抢余量。整单和共享同一笔销售余量的项目组成一个原子组，组内任一项冲突整组跳过，其余组继续；提交时重查权限、版本和余量。官方 all 隔离副本 5,451 条：成功 5,237、跳过 214（容量不足 139）、事务失败 0，余量守恒校验通过，重放不新增（TC-53/54/55）。'),
    ('主流程和边界有什么实证？', '0:182 在 10-16 旧实例上从 v1 到 v4，三条审计，资料不足结案、没有新增配对；重复派单被拒仍 v2；空结论被拒仍处理中 v3；多候选 0:1378 仍待处理。这四条的实拍原图在每日工作记录/2026-10-16/截图/v12中期复核/（证据列编号）；第 7 页下方缩略图是 10-18 最新界面在模拟预览里的同类重现，只示意界面。批量边界见上一问。'),
    ('现在的 Demo 和截图里的数字为什么不同？', '10-18 按本人授权换成新的独立实例：只含 all 一个运行、账户重新配置，旧实例的 0:182 结案和 year1 的 4,988 / 0 / 3 留在旧实例，不搬过来。所以截图按拍摄时点看，台上状态以新实例页面为准。'),
    ('性能过关了吗？', '四项必须底线都有通过证据：10-17 收尾单次一万行 1.74 s ≤ 5 s、全量首次 67.34 s ≤ 90 s；重算复用同核心候选 3 的 5.58 s ≤ 15 s；后台任务中十路 300 次查询 0 失败、P95 149 ms。45 s、5 s 优选和 UK 类 200 ms 没达到，已停止追优。批量提交 13.04 s 是 TC55 局部诊断，不算正式 NFR。'),
    ('Passkey 不填账户怎么知道是谁？', '浏览器让本人在设备上选凭据，后端按签名和凭据所属账户确定身份和角色，不信页面传来的账户或角色；错挑战、错签名、错来源都拒绝。三角色用隔离合成 ES256 凭据测过协议和 HTTP 边界，真实设备流程还要本人验证。'),
    ('AI 工具怎么用的？', '开发与验证主要用 Codex，Claude Code 参与前端界面修订和本汇报制作。汇报里的数字都对照原始 summary/JSON、准备清单和页面实拍；模拟数据截图都标明模拟。工具用途和核验方式写进报告。'),
]

DEMO = [
    ('选择 Passkey 登录（不填账户）', '本人在台前选 finance 凭据，验证后自动进入对应账户；保持 http://localhost:8080，不换成 127.0.0.1。前提：本人已在 10-18 新实例完成强密码、Passkey 与恢复码，台上不做首次配置。'),
    ('all 总览与来源守恒', '展开“查看来源、行数守恒与初始配对口径”：1,067,371 = 1,038,818 + 28,553；初始差异 9,450。各状态数以本人实际操作后的页面为准。'),
    ('按差异原因筛选', '七种原因均为中文；选“多个合格候选”后查询，列表只剩该原因，分页在服务端。'),
    ('打开 0:1378（多候选）', '候选逐字段对比：0:1271 / 0:1368 两条合格（10-16 实拍各余 12）；只看，不确认、不派单。'),
    ('打开批量工作台，只预览', '三类建议快照 → 选几条 → 预览“可提交 / 将跳过” → 点“调整选择”退出。预览和建议不占余量、不写业务表；不点“确认提交”。'),
    ('交易检索', '按客户或商品筛选、换排序、跳页；服务端每页 50 条。'),
]

def spoken_len(text: str) -> int:
    # CJK characters, each digit, and English words (about two syllables each).
    return len(re.findall(r'[一-鿿]|\d', text)) + 2 * len(re.findall(r'[A-Za-z]+', text))


def main() -> None:
    source = DECK.read_text(encoding='utf-8')
    pages = []
    for sec in re.findall(r'<section class="slide.*?</section>', source, re.S):
        title = re.search(r'data-title="([^"]+)"', sec).group(1)
        budget = int(re.search(r'data-sec="(\d+)"', sec).group(1))
        notes = re.search(r'<aside class="notes">(.*?)</aside>', sec, re.S).group(1)
        parts = [html.unescape(re.sub(r'<[^>]+>', '', p)).strip() for p in re.findall(r'<p[^>]*>(.*?)</p>', notes, re.S)]
        pages.append((title, budget, parts[0], parts[1:]))
    digest = hashlib.sha256(DECK.read_bytes()).hexdigest()
    total_budget = sum(p[1] for p in pages)
    total_est = sum(spoken_len(p[2]) / RATE for p in pages)
    out = ['# 中期汇报讲稿（课程 v1.2）：五分钟主讲＋两分钟追问', '',
           '王麒皓 · 20251101030 · 机国际2502 · VC-R-03。内容更新到 2026-10-18 的软件与证据（源码基线 e2aa569）；10-16 版保存在 `资料/中期汇报基线-20261016-v1.2/`。',
           f'本稿由 `tools/build_midterm_talk.py` 从 [网页版](中期汇报网页版/index.html)（SHA-256 `{digest[:12]}…`）的讲者备注生成，逐字一致；[中期汇报.pdf](中期汇报.pdf) 与 [中期汇报.pptx](中期汇报.pptx)（静态备份，页面为整页图片，备注相同）同源。',
           '下表秒数是按每秒约 4.3 字估算的计划，不是演练实测。', '',
           '## 上台用法', '',
           '- Chrome / Edge 打开 `中期汇报网页版/index.html`（整个文件夹可单独拷走，证据原图在 `证据原图/`）。',
           '- `F` 全屏，`P` 讲者视图（备注、下一页、总计时与逐页计时，超时变橙；`T` 开始/暂停，`R` 重置），`O` 总览，`D` 实机演示面板，`B` 黑屏，数字＋回车跳页，点图片看原图。',
           '- 实机：项目根目录双击 `启动Demo.cmd`，第 6 页按钮或 `D` 面板打开 http://localhost:8080。',
           '- 页面里的界面截图均为 10-18 最新前端（ac298f6）在开发模拟预览中的画面（左下角红色“模拟数据”标识，未登录、不连真实库），只示意界面；所有真实数字来自原始输出，旧时点实拍原图仍在每日工作记录中。',
           '- 网页打不开时用 `中期汇报.pdf` 或 `中期汇报.pptx`（PPTX 不能点击跳转实机，需手动切到浏览器）。', '',
           '## 时间计划', '', '| 页 | 标题 | 计划 | 讲稿估算 |', '| --- | --- | --- | --- |']
    for i, (title, budget, text, _) in enumerate(pages):
        out.append(f'| {i:02d} | {title} | {budget} 秒 | 约 {spoken_len(text) / RATE:.0f} 秒 |')
    out += [f'| | 合计 | {total_budget} 秒 | 约 {total_est:.0f} 秒 |', '',
            '## 逐页讲稿', '']
    for i, (title, budget, text, hints) in enumerate(pages):
        out += [f'### {i:02d} {title}（{budget} 秒）', '', text, '']
        out += [f'> 备查：{h}' for h in hints] + ([''] if hints else [])
    out += ['## 两分钟追问准备', '']
    out += [f'{n}. **{q}** {a}' for n, (q, a) in enumerate(QA, 1)]
    out += ['', '## 实机演示路线（追问时用）', '']
    out += [f'{n}. **{t}**：{d}' for n, (t, d) in enumerate(DEMO, 1)]
    out += ['', '派单、确认、结案和批量提交都会在演示库留下真实记录，台上不做；新实例没有 0:182 结案记录，单条主流程和两条边界用第 7 页表格讲（实拍原图在 v12中期复核）。“两个年度全部”数据任务约 70 秒，不在追问里现跑。10-22 当天只做正常登录和预设查看。', '',
            '## 工程备查（不占正文）', '',
            '- Git：截至 e2aa569（10-18 软件基线）main 共 68 个原生提交，均以 FR 编号开头；关键为 97b790a 首次全量优化、6ead220 性能收尾与首次引导、b9d8d8d 原因筛选、bc97763 推荐批量复核、eb499f9 工作台修订、ac298f6 自动刷新建议。实验分支 79ff9b2 比较后未合入；快照 746a7e1 已用 bundle 在新目录恢复复现。',
            '- 技能：run-app 由新的 Codex CLI 进程显式调用，退出码 0，原始 JSONL 与回执在 `每日工作记录/2026-10-16/原始输出/v12中期复核/`；16 图是该输出摘录的浏览器展示，不是 CLI 界面截图。',
            '- 测试口径：10-18 12:44 统一 130 Go 顶层 + 25 Python + 6 前端 = 161 项（批量复核实现/regression-summary.json）；之后修订 131 Go + 6 前端通过、3 项相关 Go 复验、Python 复用；跨轮去重 162 不是一次统一回归，15:12 末次自动刷新只构建未测试。10-16 为 97 项，10-15 本人亲跑截图是当时版本的 71 Go + 13 Python，各口径不混称。', '',
            '## 实际演练记录（本人填写）', '',
            '| 日期 | 主讲用时 | 追问用时 | 卡顿 / 修改 |', '| --- | --- | --- | --- |', '| | | | |', '']
    OUT.write_text('\n'.join(out), encoding='utf-8')
    print(OUT.name, total_budget, round(total_est))


if __name__ == '__main__':
    main()

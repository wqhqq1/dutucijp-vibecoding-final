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
    ('为什么不全自动配对？', '原始数据没有原单关联号。R-02 要求客户、商品、精确单价、国家一致，销售严格在先、余量足够；FR-06 还要求候选唯一。像 0:1378 有两条合格候选，就留给人核查，不靠数量相等或先来后到挑一条。配错会把退款记到错误的销售上，比留给人看代价大。'),
    ('50.69% 是准确率吗？', '不是。它是自动配对覆盖率：all 范围 9,714 / 19,164（year1 为 5,214 / 10,205 ≈ 51.09%）。没有真值标签，所以不能叫准确率；剩下的是疑似 6,275 和无合格原单 3,175，进入人工核查。'),
    ('哪些是原始数据，哪些是你建的？', '交易行、八个原始列和源行号是官方事实；候选、自动配对和差异原因是规则推断（conservative-1.0.0）；派单、结论和审计是课程模拟的业务记录，由演示账户操作产生。页面上也写着“规则推断 · 课程模拟”。'),
    ('主流程和边界有什么实证？', '0:182 从 v1 到 v4，三条审计，资料不足结案、没有新增配对；重复派单被拒仍 v2；空结论被拒仍处理中 v3；多候选 0:1378 仍待处理。第 7 页每张图都能点开原图，原图在每日工作记录/2026-10-16/截图/v12中期复核/。'),
    ('year1 和 all 的数字为什么不同？', 'year1 是 Year 2009-2010 一张表：当前工单 4,988 待处理、3 关闭，初始差异 4,991。all 是两张表：初始差异 9,450，本轮只查看没有操作。两个范围、两个时点分开看，不相加。'),
    ('性能过关了吗？', '四项必须底线都过了：一万行 1.84 s ≤ 5 s；全量首次最慢 72.34 s ≤ 90 s；重算 10.96 s ≤ 15 s；后台任务中 300 次查询 0 失败、P95 11.72 ms。45 s 和 5 s 两个优选目标没达到，已经决定不再追加优化。'),
    ('AI 工具怎么用的？', '开发与验证主要用 Codex，本网页版汇报用 Claude Code 制作。汇报里的数字都对照原始 summary.json、准备清单和页面实拍；工具用途和核验方式写进报告。'),
]

DEMO = [
    ('finance 用 Passkey 登录', '本人在台前完成登录；保持 http://localhost:8080，不换成 127.0.0.1。'),
    ('year1 总览与来源守恒', '4,988 待处理 / 0 处理中 / 3 关闭；展开“查看来源、行数守恒与初始配对口径”：525,461 = 521,770 + 3,691。'),
    ('打开 0:182（主流程结果）', '已关闭 v4：资料不足结论＋派单/开始/关闭三条审计；销售 0:191 不严格在先，合格候选 0。'),
    ('已关闭不能再改（边界，只读可见）', '面板显示“此差异已关闭，不能修改或重新开启”。重复派单、空结论两条边界用第 7 页实拍原图讲，不在演示库重复写入。'),
    ('切到 all，打开 0:1378（多候选）', '两条合格候选 0:1271 / 0:1368 各余 12，仍待处理；只查看，不派单、不确认。'),
    ('交易检索：客户 13526', '共 54 条，服务端分页 50 + 4，末页“下一页”禁用。'),
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
           '王麒皓 · 20251101030 · 机国际2502 · VC-R-03。',
           f'本稿由 `tools/build_midterm_talk.py` 从 [网页版](中期汇报网页版/index.html)（SHA-256 `{digest[:12]}…`）的讲者备注生成，逐字一致；[中期汇报.pdf](中期汇报.pdf) 与 [中期汇报.pptx](中期汇报.pptx)（静态备份，页面为整页图片，备注相同）同源。',
           '下表秒数是按每秒约 4.3 字估算的计划，不是演练实测。', '',
           '## 上台用法', '',
           '- Chrome / Edge 打开 `中期汇报网页版/index.html`（整个文件夹可单独拷走，证据原图在 `证据原图/`）。',
           '- `F` 全屏，`P` 讲者视图（备注、下一页、总计时与逐页计时，超时变橙；`T` 开始/暂停，`R` 重置），`O` 总览，`D` 实机演示面板，`B` 黑屏，数字＋回车跳页，点图片看原图。',
           '- 实机：项目根目录双击 `启动Demo.cmd`，第 6 页按钮或 `D` 面板打开 http://localhost:8080。',
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
    out += ['', '每次操作都会在演示库留下真实记录：台上不新关闭待处理差异、不重开已结案记录；“两个年度全部”数据任务约 72 秒，不在追问里现跑。10-22 当天只做正常登录和预设查看。', '',
            '## 工程备查（不占正文）', '',
            '- Git：截至 d3b3700（10-16 v1.2 复核提交）主线 43 次提交均以 FR 编号开头（10-14 6 次、10-15 31 次、10-16 6 次）；之后的新提交不回改此统计。实验分支 79ff9b2 比较后未合入；快照 746a7e1 已用 bundle 在新目录恢复复现。',
            '- 技能：run-app 由新的 Codex CLI 进程显式调用，退出码 0，原始 JSONL 与回执在 `每日工作记录/2026-10-16/原始输出/v12中期复核/`；16 图是该输出摘录的浏览器展示，不是 CLI 界面截图。',
            '- 测试口径：97 项 = 76 Go 顶层 + 15 Python + 6 前端（辅导收工回归/summary.json）；10-15 本人亲跑截图是当时版本的 71 Go + 13 Python，两者不混称。', '',
            '## 实际演练记录（本人填写）', '',
            '| 日期 | 主讲用时 | 追问用时 | 卡顿 / 修改 |', '| --- | --- | --- | --- |', '| | | | |', '']
    OUT.write_text('\n'.join(out), encoding='utf-8')
    print(OUT.name, total_budget, round(total_est))


if __name__ == '__main__':
    main()

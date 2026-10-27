# Codex交接：差异原因中文化、原因筛选与排版第二轮

- 实际时间：2026-10-17 夜间（Asia/Shanghai）
- 执行者：Claude Code（Opus 5.5）。接手者：Codex。
- Git：HEAD `30b2982`，本轮与上一轮的改动都**未提交**。`四段式任务草稿.md` 等其他未提交改动在开工前已经存在，本轮没有碰。
- 上一轮交接：[前端设计优化交接.md](前端设计优化交接.md)。其中"backend/全部只读、未改后端"的说法**已被本轮取代**。本人已取消后端修改限制，本轮改了 `backend/internal/store` 和 `backend/internal/web`。
- 本人要求：差异代码没有对应中文、有时超出格子，要优化；字体和排版更有设计感；筛选条件增加差异原因；提供 Codex 交接；取消后端修改限制；**不需要再提供页面截图**。

## 1. 本轮已完成

### 1.1 差异原因与其他代码中文化

前端原先只翻译了 4 个原因，其中 2 个后端根本不会产生，所以界面上直接露出了 `insufficient_remaining_quantity` 这种长英文代码。这类代码没有断行点，会撑出事实格子。现在按 `backend/internal/reconcile/engine.go`（规则版本 `conservative-1.0.0`）实际产生的代码全部对上：

| 代码 | 表格/筛选显示 | 详情说明 |
| --- | --- | --- |
| `missing_identity` | 缺客户或国家/地区 | 取消记录缺少客户编号或国家/地区，按规则不自动配对。 |
| `suspected_duplicate_cancel` | 取消行疑似重复 | 这条取消在同一工作表内疑似重复，需人工核查。 |
| `ambiguous_candidates` | 多个合格候选 | 两条以上销售同时满足条件，不按先后顺序猜测。 |
| `suspected_duplicate_sale` | 候选销售疑似重复 | 唯一合格的销售行在同一工作表内疑似重复。 |
| `no_identity_match` | 无同身份销售 | 当前范围内没有客户、商品、单价和国家/地区都相同的销售。 |
| `no_strictly_earlier_sale` | 无更早销售 | 同身份的销售都不早于这次取消。 |
| `insufficient_remaining_quantity` | 可退余量不足 | 更早的同身份销售，剩余可退数量都不够。 |

同时修正了以下几处：
- 决策时证据原因：`qualified` 合格候选，`suspected_duplicate_sale` 合格但销售行疑似重复，`remaining_quantity_too_small` 剩余数量不足，`sale_not_strictly_earlier` 销售不早于取消。
- 审计动作：后端人工确认写的是 `confirm_candidate`，前端原来按 `confirm_match` 匹配，因此一直显示英文。现在显示"人工确认候选"。
- 运行范围：`benchmark` 显示为首表固定一万行，`year1` 为首年度全部，`all` 为两个年度全部。
- 候选行：去掉空客户造成的"· ·"，时间里的 `T` 改为空格。
- 未知代码：照原样显示，不猜测含义，但改为可任意断行，不会再撑破格子。

### 1.2 差异原因筛选（前后端）

- 后端：`QueryFilter` 新增 `Reason`，HTTP 参数为 `reason`。只接受上表 7 个代码，其他值返回 422 `invalid_input`；条件参数化为 `c.reason=?`。
  - 新增 `caseFiltered(f)`（status/reason/assignee 任一非空），替换原来散落的 `f.Status != "" || f.Assignee != ""` 判断。原因筛选因此和状态筛选一样走实时 cases 连接，不会误走"不可变交易计数"和"来源顺序索引"两条快路径。
  - 差异、交易、配对列表和财务 CSV 导出都按同一口径生效。
- 前端：筛选区在"状态"旁新增"差异原因"下拉，默认为全部原因。`emptyFilters` 加了 `reason`，原有的 `query()` 会自动把它带进列表请求和导出链接；"已应用 N 个筛选条件"也会把它计入。
- 文档（先于实现补记）：`要做什么.md`、`怎么做.md`、`怎么算做对.md` 顶部各加了一条 2026-10-17 FR-07/TC-16 补充。

### 1.3 字体与排版

设计方向是"对账簿"：
- **字体**：中文标题用衬线体，正文用无衬线体，所有编号和数字统一用 IBM Plex Mono（等宽），按账簿的样子对齐。
- **字体来源**：中文用系统字体栈。衬线依次回退 Noto Serif SC → 思源宋体 → 宋体-简 → 华文宋体 → SimSun；正文依次回退 Noto Sans SC → 思源黑体 → 苹方 → 微软雅黑 → 等线。IBM Plex Mono 通过 npm 包 `@fontsource/ibm-plex-mono@5.3.0`（OFL 许可）随包自托管，只取 latin 400/500 两档，woff2/woff 共约 56 kB，符合 CSP `font-src` 回退到 `'self'` 的要求，不连外网。
- **唯一装饰**：品牌标改成朱红"账"字印章；已关闭差异的结论区加一个"已结案"印戳。除此之外不再用朱红。
- **账簿语言**：
  - 差异总数下方加双线，沿用"合计下划双线"的会计习惯。
  - 表头下方用实线，行与行之间用细线，去掉斑马纹。
  - 抽屉内的小节标题带一条延伸到边缘的细线。
  - 登录页左栏底部加淡淡的横线，作为全应用唯一的氛围背景。
- **防溢出**：
  - 差异原因单独一行，横跨整个事实区，并附中文说明。
  - 事实区改为固定 4 列，手机上 2 列。
  - 各网格项加 `min-width:0` 和 `overflow-wrap:anywhere`。
  - 统计数字用 `clamp()` 随宽度缩放。
  - 当前候选为 0 时显示"当前规则下没有合格候选"。
- **静态资源 MIME**：Go 内置 MIME 表没有 `.woff/.woff2`。`server.go` 在 `init()` 中注册了 `font/woff2` 和 `font/woff`，避免依赖 Windows 注册表。

### 1.4 变更文件

| 文件 | 改动 |
| --- | --- |
| `backend/internal/store/query.go` | `Reason` 字段、`caseReasons` 白名单、`caseFiltered()`、`c.reason=?` 条件 |
| `backend/internal/store/query_test.go` | 新增 `TestTC16_CaseReasonFilterMatchesCaseRows`；参考对照测试的过滤器表加了 4 组原因条件；非法原因拒绝 |
| `backend/internal/web/server.go` | `filter()` 读取 `reason`；注册字体 MIME |
| `backend/internal/web/frontend_test.go` | 新增 `TestBundledFontTypesDoNotDependOnHostRegistry` |
| `backend/internal/web/transaction_query_test.go` | 注入形状的 `reason` 返回 422 |
| `frontend/src/main.tsx` | 原因、证据、审计、范围的中文映射；原因筛选；字体导入；候选行文本清理；印章字 |
| `frontend/src/style.css` | 整体重写为对账簿风格（类名与上一轮一致） |
| `frontend/package.json`、`package-lock.json` | 新增依赖 `@fontsource/ibm-plex-mono` 5.3.0（精确版本） |
| `frontend/dev/mock-api.ts` | 模拟数据改用真实的原因、证据、审计和配对方式代码，并支持 `reason` 筛选（仅开发预览用，不进入 dist） |
| `要做什么.md`、`怎么做.md`、`怎么算做对.md` | 各加一条 FR-07/TC-16 原因筛选补充 |

## 2. 本轮实际验证

| 项目 | 结果 |
| --- | --- |
| `go build ./...` | 成功 |
| `go vet ./internal/store ./internal/web` | 无输出（通过） |
| `go test -count=1 ./internal/store ./internal/web` | store 42 项、web 13 项顶层测试全部通过，0 失败 / 0 跳过 |
| `npm test` | 6 pass / 0 fail |
| `npm run build`（含 `tsc --noEmit`） | 成功 |
| 页面检查 | 用 headless Chrome 走 CDP，对 `preview:mock`（即 dist 加后端同款 CSP）跑 11 项，全部 PASS，没有 CSP 违规或脚本异常，详情见下方。 |

页面检查覆盖的内容：
- 1440、820、390px 三种宽度下的差异列表：没有页面级横向溢出，没有内容溢出单元格，页面文本中没有残留的下划线英文代码，7 种原因都显示为中文。
- IBM Plex Mono 400/500 已加载，标题使用衬线字体栈。
- 选择"多个合格候选"后点查询：请求带上了 `reason=ambiguous_candidates`，列表只剩该原因，导出链接也带同样的参数。
- 桌面和手机两种宽度下的证据抽屉：原因、证据说明、审计动作都是中文。
- 确认候选后，审计里出现"人工确认候选"。
- 已关闭差异显示"已结案"印戳。
- 配对方式显示为"人工确认 / 规则自动"，运行范围显示中文。
- 数据任务能跑到完成，登录页正常。

网络日志里的失败只有两类，都在预期内：未登录时 `/api/auth/me` 返回 401，以及预览服务器没有 favicon 导致的 404。

检查脚本、输出和截图在 `.working/frontend-design-20261017/`（`cdp-check2.mjs`、`cdp-output2.txt`、`cdp-results2.json`、`shots2/`），已被 git 忽略，只作过程材料。按本人要求，这些截图不作为交付页面。

构建产物 `frontend/dist/`（2026-10-17 本轮最后一次构建）：
- `index.html` sha256 `1a98bf912574a65e8dfefaceee2a5a06b7eb5c3a5cfc357c2154d1c9e934abb6`
- `assets/index-DpGLKcvL.js` sha256 `fff23005d1934ab3f664c794ebd9100bb16d451250cbd41fd664fc0346208e7f`
- `assets/index-k7Kn9fox.css` sha256 `fc7b546db2b1081ca43a18ba41b18ac9ed32173b249a5f8122fd76c4b39da88d`
- `assets/ibm-plex-mono-latin-{400,500}-normal-*.woff2/.woff` 共 4 个字体文件

本轮没有改动的部分：
- 没有把 dist 复制进 `backend/internal/web/assets/`，那里仍是 21:45 的旧版。
- 没有运行 `build.ps1`、`test.ps1`，也没有跑 Python 测试和全量性能测试。
- 没有替换、重启或登录 8080 现行软件，没有重置账户。
- 本轮启动的模拟预览和 headless Chrome 都已结束。

## 3. 未验证项（不得写成通过）

- 新前端和新后端都还没在真实 8080 上运行过。因此以下都未验证：真实账户登录、真实 Passkey/TOTP 与恢复码、真实全量数据下的原因分布与显示、原因筛选在官方 all 规模下的查询耗时。
- 原因筛选会强制走 cases 连接。交易检索里"原因 + 无其他行条件"的组合在全量数据上的耗时没有测过。它和现有的 status 筛选走同一路径，按设计应与 status 筛选同量级，但没有测量。
- 只测了 Chrome（headless）。Edge、Firefox、Safari、读屏软件都没测。
- 没有 Noto Serif SC 的机器上，标题会回退到宋体或 SimSun，观感会弱一些，这一点没有在别的机器上实测。
- 完整的 Go 全包测试、Python 测试和 `test.ps1` 统一回归本轮都没有跑。

## 4. 交给 Codex 的任务（四段式）

### 目标

把本轮的前后端改动集成到可运行软件，完成必要的回归和隔离冒烟，然后由本人决定是否替换 8080，并完成真实验收。

### 背景

- 已完成的内容见第 1、2 节。项目规矩见根目录 `AGENTS.md`。原因代码的来源是 `backend/internal/reconcile/engine.go`。
- 前端是 React 19 + TS + Vite 8，由 Go/Gin 通过 `//go:embed assets/*` 嵌入。后端 CSP 为 `style-src 'self'`，所以页面不能用内联样式，也不能加载外部字体。
- 交易、配对、差异列表共用一个筛选表单。原因筛选在交易和配对页的含义是"关联差异的原因"，与现有状态筛选一致：自动配对没有差异记录，因此会被这两个筛选排除。

### 约束

- 本人已**取消后端修改限制**，backend 可以修改。但以下规矩照旧：
  - 不改配对规则、权限、认证、CSRF/Origin/Host/WebAuthn 校验。
  - 不缩小数据范围。
  - 筛选、排序、分页、导出口径不变。
  - 修改业务行为前，先更新需求、设计和用例。
- 不读取或保存真实密码、令牌、OTP、二维码、私钥和恢复码；不重置账户，不重建或改写业务库。8080 何时替换由本人决定，设备验证结果按实际操作保存。
- 不伪造测试结果或截图；模拟预览的结果和真实部署的结果必须分开写。
- **不需要再制作页面截图或预览页交付。**
- 不改中期材料，不制作最终报告、最终演示或最终提交包。
- 保留现有未提交改动，不回退他人文件。提交信息以 FR 编号开头，例如 `FR-07 差异原因中文化与原因筛选、对账簿排版`。

### 完成条件

1. 运行统一回归 `./test.ps1`（PowerShell 7），记录 Go 顶层、Python、前端的通过/失败/跳过数，0 失败。因为后端已改，这次需要跑完整回归，不能只复用旧成绩。
2. 运行 `./build.ps1` 完成集成：
   - 复制前先清掉 `backend/internal/web/assets/assets/` 里的旧哈希文件，保留 `placeholder.txt`。
   - 确认嵌入的 js、css 和 4 个字体文件的散列与第 2 节一致（如果重新构建了，就以新构建为准，并写明新散列）。
3. 在隔离端口和隔离库上做冒烟：
   - 首页返回 200，未登录访问业务接口返回 401。
   - `/assets/*.woff2` 返回 `Content-Type: font/woff2`。
   - `/api/cases?reason=invented` 返回 422。
   - 浏览器控制台没有 CSP 违规。
4. 如有条件，用同口径做一次交易检索的耗时对照：原因筛选与状态筛选各跑首页和第 2 页，如实记录，不追优。
5. 写一份 Codex 执行交接，列出提交号、实际命令与输出位置、未验证项。然后停写，交给本人决定是否替换 8080，并做真实登录验收。

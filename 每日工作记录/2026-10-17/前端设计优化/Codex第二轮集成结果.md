# 第二轮交接合并与运行结果

实际日期：2026-10-17，Asia/Shanghai。读取并合并[第二轮交接](Codex交接-差异原因与排版第二轮.md)，沿用本人“先不做备份，先运行起来让我测试”的安排。源码基线为 `30b2982`；两轮改动原本就在 main 工作区，本次完成资源嵌入、回归和运行版本更新，没有跨分支合并。最终提交号以本次 FR-07 提交为准。

## 已完成

- 两轮前端改动一并集成：七种真实差异原因中文化、证据/审计/范围中文化、原因筛选、长内容断行和字体排版。自托管字体随程序提供，不需要联网加载。
- 原因筛选接入差异、交易、配对及财务 CSV，使用实时 cases 条件，不走不适用的计数快路径。原配对引擎、认证、权限和原始数据处理源码没有修改。
- 补强现有 HTTP 用例：四个相关入口对未知原因返回 422；已配置的隔离身份按原因取得三条取消记录，分页显示两条，CSV 仍包含完整三条。没有增加登录豁免。
- 旧哈希资源已移入 Git 忽略的工作目录，保留顶层 placeholder.txt；嵌入目录只含本轮 JS、CSS 和四个字体文件，逐字节与 dist 一致。
- 隔离端口 18089/全新空测试库的 HTTP 冒烟通过，随后已停止该实例。真实 8080 已替换为第二轮构建；teacher-demo 软件验收目录同步更新且清单校验通过，没有改中期文件或既有 ZIP。

## 实际验证

| 检查 | 实际结果 |
| --- | --- |
| PowerShell 7.6.5 `./test.ps1` | Go 顶层 101、Python 25、前端 6，共 132 项；0 失败、0 跳过 |
| 补强后的同一 HTTP 用例定向复跑 | 1 项通过，仍属于上述 101 项，不重复加总 |
| `./build.ps1` | 锁定依赖安装、TypeScript、Vite、Go 集成构建成功 |
| 18089 隔离冒烟及 8080 资源复核 | 首页 200；未登录业务 401；JS/CSS/四个字体散列匹配；woff/woff2 MIME 正确 |
| 未知原因返回码 | 已配置合成身份的 HTTP 用例返回 422；未登录请求仍为 401，认证顺序不变 |
| CSP | 实际响应头保持原策略；本轮未做真实浏览器控制台检查 |
| 原个人状态 | 三账户 complete、各 1 个 Passkey；4 条 closed、14437 条 pending、13 条审计、year1/all；部署前后公开状态及业务指纹一致 |
| 教师软件运行文件 | 替换 app/recon.exe 并更新清单，launcher check 通过；未新建教师运行库或账户 |

原始结果见[原始输出](原始输出/第二轮集成/)；完整测试原始日志保留于项目外 `C:/Users/wqhqq/AppData/Local/dutucijp-vibecoding-final/verification/frontend-round2-20261017-tests/`。未重跑全量对账、NFR 压测或无关的独立下载验收。

## 当前运行

- 地址：<http://localhost:8080/>，公开实例 `personal-demo`。
- 构建：`C:/Users/wqhqq/AppData/Local/dutucijp-vibecoding-final/builds/frontend-round2-integrated-20261017/recon.exe`。
- SHA-256：`78e46697ae9a34c9778f14b6f72b94d10237192226569b022c836b3d7ccbcf0e`。
- 当前 PID 40924，前台工具会话 81692；以后应先核对实际进程，不能凭此历史 PID 直接停止。
- 服务停止后双击根目录“启动已有演示.cmd”，读取已更新的 active-build.json，沿用原个人库。原账号、Passkey 和业务记录保留；页面缓存旧版时 Ctrl+F5。
- 本轮 index/JS/CSS 散列与 Claude 第二轮交接一致，四个字体的实际散列见[集成回执](原始输出/第二轮集成/integration-result.json)。

实际主要命令：

```powershell
pwsh -NoLogo -NoProfile -File ./test.ps1 -OutputDir C:/Users/wqhqq/AppData/Local/dutucijp-vibecoding-final/verification/frontend-round2-20261017-tests
pwsh -NoLogo -NoProfile -File ./build.ps1 -OutputDirectory C:/Users/wqhqq/AppData/Local/dutucijp-vibecoding-final/builds/frontend-round2-integrated-20261017
# backend 目录，使用既有隔离身份夹具
go test -count=1 -json ./internal/web -run '^TestHTTP_TC16_CountrySortPaginationAndExportAgree$'
# 项目根目录
./tools/launch-existing-demo.ps1 -Database C:/Users/wqhqq/AppData/Local/dutucijp-vibecoding-final/demos/demo-20261015-170120-d36101c6/business.sqlite
```

## 保留的未验证项

真实 Passkey 登录后的第二轮页面、本人操作验收和真实浏览器 CSP 控制台尚未复核；不能把 Claude 的模拟预览写成真实 8080 通过。官方 all 规模下新增原因筛选首页/第 2 页的耗时尚未测量，未宣称达到性能目标。现有 NFR 历史成绩保留，不冒称本轮重新测过。真实设备、物理另机和软件本人认可仍按原状态。

本人已要求不再提供本轮页面截图；没有新增截图或预览页交付。GitHub 备份继续暂缓，未制作最终报告、最终演示或最终提交包。下一步由本人在 8080 使用原账号测试中文原因、筛选、分页和导出，再给出软件验收反馈。

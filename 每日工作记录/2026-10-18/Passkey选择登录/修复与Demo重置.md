# FR-14 Passkey选择登录与Demo清理

实际日期：2026-10-18，Asia/Shanghai。源码起点为 `461f2ac`。本人要求允许用户自行选择Passkey、根据所选凭据自动登录账户；随后明确要求当前Demo恢复为未设置状态，以便之后自己测试新版本。本轮一般事务模型约束保持，无子代理。

## 实现

- 默认登录不发送账户ID，使用现有锁定库v0.18.2的发现式登录选项，不发送allowCredentials。浏览器以 `mediation=required` 让用户选择凭据，按钮在账户和密码均为空时仍可用。
- 服务端为选择流程生成独立随机Cookie和2分钟一次性挑战；该Cookie不进入auth_sessions，不能访问配置或业务。finish仍核对同源、CSRF、挑战Cookie、用途及过期时间。
- 从credential_id查询实际归属，核对user_handle与begin时账户版本快照，再验证签名、RP ID、Origin、UP、UV、计数及克隆警告；在事务中消费挑战、保存凭据状态和登录审计、发放所选账户会话。身份与角色来自数据库，不相信客户端指定角色。未完成配置的账户只能续办配置。
- 新注册要求residentKey=required及用户验证。旧凭据可能未存成可发现凭据，登录页和因素页均保留指定账户兼容入口。取消或不支持时不会退回密码单独登录。
- 保留账户和来源限流；logout同时作废当前Cookie绑定的挑战。原Go/Gin/SQLite与React架构、页面CSS与此前侧栏修复保持。
- 先修改需求、设计与用例再实现。依据为[锁定版本官方库文档](https://pkg.go.dev/github.com/go-webauthn/webauthn@v0.18.2/webauthn#WebAuthn.BeginDiscoverableLogin)和本机同版本源码。

## 实测

| 项目 | 结果 | 原始证据 |
| --- | --- | --- |
| Go `go test -count=1 -json ./...` | 110项顶层通过，0失败、0跳过 | [输出](go-regression.jsonl)、[统计](go-summary.json) |
| 前端 `npm test` | 6项通过，0失败、0跳过 | [输出](frontend-tests.txt) |
| TypeScript/Vite生产构建及Go可执行文件构建 | 均通过；正式前端无mock入口 | [前端输出](frontend-build.txt)、[Go构建](production-build.json) |
| 合成Passkey选择真实账户 | finance/warehouse/admin三角色分别正确；匿名挑战无账户会话；不发送凭据列表；新注册要求可发现凭据 | Go新增TC-37 |
| 负例与撤销 | 错挑战/Origin/RP、缺UP/UV、错签名、缺/错handle、未知凭据、错Cookie、过期、用途错配、版本撤销、凭据删除、logout及替换旧挑战均拒绝 | Go新增TC-38，16项子场景 |
| 并发与重放 | 同一挑战并发仅一次成功，重放拒绝 | Go新增TC-38 |
| 未完成配置及限流 | 未保存恢复码的账户只有setup会话；不同来源的账户限流和切换挑战的来源限流均生效，五分钟窗口后恢复 | Go新增TC-36/44 |
| 审计失败回滚 | 不消费挑战、不更新凭据或增加会话；恢复后同挑战可重试 | Go新增TC-38 |
| HTTP边界 | 匿名挑战不能读取业务或注册；finish缺/错CSRF或错Origin拒绝；刷新/取消后的Cookie不阻断新的begin | Go新增Web TC-37 |
| 页面 | 正式登录页账户、密码为空，Passkey按钮可用且说明自动识别账户；确认后返回首次配置入口 | [正式页面检查](production-login-ui-check.json)、[入口截图](Passkey选择登录入口.jpg) |
| 正式运行与清单 | 健康及HTML 200，加载本次前端；251项运行目录清单校验通过 | [健康回执](runtime-health.json)、[更新回执](runtime-update.json) |

协议验证使用隔离SQLite及真实ES256签名合成凭据，不是在正式Demo里插入测试用户或绕过认证。没有自动操作用户设备凭据选择器；物理Passkey由本人稍后验证。Python源码及性能路径没有修改，本轮没有重跑旧NFR或制作最终报告/演示/提交包。

## 当前Demo状态

已通过既有启动器核实并停止原实例，旧状态保存在本机原路径，不删除其密码、设备绑定和业务历史。新实例只复用散列核对通过的只读官方工作簿及来源回执，独立生成全新两个年度all业务库，不复制旧数据库或认证私有资料。默认启动选择已切换到新实例，根目录启动/停止Demo仍适用。

| 核对 | 实际结果 |
| --- | --- |
| 当前地址 | `http://localhost:8080` |
| 当前运行标识 | `e4b63722dfccba388e3f9f736ad44582` |
| finance / warehouse / admin | 三账户全部state=new，密码为空 |
| Passkey、会话、恢复码、挑战 | 全部0 |
| 人工分配、已派单、非pending差异、业务操作历史 | 全部0 |
| 数据运行 | 一个正式published运行，scope=all |
| 当前页面 | 首次配置，显示三个未设置账户 |
| 二进制SHA-256 | `080aa9ed6089ae47516d9490a686d7d4055335895789a88d45d0f8b768791702` |

以上核对采用只读SQLite连接与公开健康/首次账户接口，未读取或记录秘密。[完整状态回执](fresh-demo-verification.json)、[首次配置截图](Demo未设置账户.jpg)。旧/新目录及本地可执行文件备份位置见[更新回执](runtime-update.json)。切回旧状态前需停止当前匹配实例，再恢复项目外默认selected.json的旧路径；本轮未删除设备中旧Passkey，新实例需要重新绑定。

工作前已有的中期材料与教师独立运行草稿未跟踪文件保持原样；本轮只提交自身代码、规格和证据。后续由本人从强密码、Passkey绑定、恢复码保存开始，退出后用“选择Passkey登录”测试新流程。

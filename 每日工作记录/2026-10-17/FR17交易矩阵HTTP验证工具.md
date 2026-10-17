# FR-17 交易矩阵HTTP验证工具

实际日期：2026-10-17。工具开发阶段只修改、构建并运行轻量工具测试；收到串行重型窗口授权后，已实际运行下述一次交易矩阵。结果和旧默认工作负载分开记录。

`backend/cmd/perfhttp/main.go`新增可选`--transaction-matrix`。不传时保留旧六类HTTP请求、十路并发、每路30次和最近秩P95计算；旧JSON字段保留，新增`workload`、`workload_queries`及逐请求`query_path`供口径核对。

交易矩阵使用同一已发布官方all范围，10类各30次，共300次真实认证HTTP：默认50条、客户17850、单号489434、商品85123A、2010整年日期、客户+商品+2009-12-01至2011-12-10组合、第10000页50条、UK、France、日期升序第10000页100条。条件与专项Store基准相同，不缩all数据。每个响应在HTTP计时外验证已测准确总数、当前分页、返回数量、来源、筛选、页内顺序及无重复行；校验失败计入请求失败。

负载仍由真实完成安全配置和密码+TOTP登录的隔离财务测试账户通过HTTP执行，后台真实新建year1准备/导入/对账；不插入业务会话，不记录秘密，不代表本人配置或设备验证。

旧观察判定只有running/queued，本次收紧为每个请求的前、后观察均为`running/importing`或`running/reconciling`。两种workload都只在300次均满足此窗口、零失败、P95≤500ms、源库散列不变、真实后台任务最终成功时才标`baseline_passed=true`。任一不符均保留真实结果并返回失败，不做额外优化，也不把活动窗口外请求算作合格。新增矩阵的结果通过`workload=official-all-transaction-matrix`明确标示，不冒充旧六类工作负载的同口径成绩。

轻量检查：`go test ./cmd/perfhttp -count=1`通过1.354s，3条顶层测试覆盖旧六类完整路径不变、矩阵均衡300次、真实后台状态边界、验收失败门槛及错误结果拒绝。已构建：

`C:\Users\wqhqq\AppData\Local\dutucijp-vibecoding-final\builds\perfhttp-transaction-20261017.exe`

由整合方在其他重型测试完成后串行运行（目标目录必须尚不存在，源库不得已有year1，工具会只读在线备份）：

```powershell
& 'C:\Users\wqhqq\AppData\Local\dutucijp-vibecoding-final\builds\perfhttp-transaction-20261017.exe' --source-db 'C:\Users\wqhqq\AppData\Local\dutucijp-vibecoding-final\verification\transaction-20261017-optimized-final\query.sqlite' --output-dir 'C:\Users\wqhqq\AppData\Local\dutucijp-vibecoding-final\verification\transaction-20261017-http-matrix' --project 'E:\软件编程大作业\退货对账与差异处理系统' --transaction-matrix
```

省略最后的`--transaction-matrix`，并改用另一个全新输出目录，即为旧六类默认工作负载。`summary.json`和`requests.jsonl`保存在各自输出目录。另机、本人验收和最终材料仍不在本次工具构建及隔离性能结果内。

## 本次真实交易矩阵结果

实际执行使用原索引官方all只读源`verification/optimization-20261016-query2/query.sqlite`；新输出目录`verification/transaction-20261017-nfr04`，没有使用或覆盖上方复跑命令示例目录。原库SHA前后均为`8b39ad979fd69df3276e5e5035450844b37feeccb104b47847b575503008c1bc`。

执行完成、退出码0。十路共300次HTTP请求全部在前后`running/importing`活动窗，零失败；聚合最近秩P95为450.8587ms，最大594.5663ms。`baseline_passed=true`，`target_passed=false`（200ms优先目标未达）。真实year1任务最终`succeeded/published`，`reused=false`；实际对账落库5.7975436s。测量结束观察仍为真实importing，没有使用任务结束后的请求凑足300次。

每类30次，均活动窗30次、失败0次；以下分类P95只表示本轮30个实际样本，不声称稳定的长期分布：

| 分类 | 次数 | P95/ms | 最大/ms |
| --- | ---: | ---: | ---: |
| 默认 | 30 | 233.72 | 391.88 |
| 客户 | 30 | 307.44 | 485.27 |
| 单号 | 30 | 301.79 | 342.60 |
| 商品 | 30 | 268.58 | 288.44 |
| 整年日期 | 30 | 565.33 | 594.57 |
| 客户+商品+日期组合 | 30 | 272.34 | 370.45 |
| 第10000页×50 | 30 | 393.43 | 407.19 |
| United Kingdom | 30 | 504.88 | 576.33 |
| France | 30 | 224.13 | 232.38 |
| 日期排序第10000页×100 | 30 | 523.94 | 552.67 |

原NFR-04门槛按全部300次聚合P95计算，不能将通过说成所有子路径P95均≤500ms；本轮UK、整年日期及日期排序深页子样本仍超过500ms。按授权如实记录后结束，没有追加优化或重复重型测量。此次新增交易矩阵与旧六类混合工作负载有明确不同，不替换或伪装旧结果。

证据：[summary](原始输出/交易检索/nfr04-summary.json)、[300次原始请求](原始输出/交易检索/nfr04-requests.jsonl)、[分类统计](原始输出/交易检索/nfr04-category-summary.json)、[实际stdout](原始输出/交易检索/nfr04-stdout.txt)、[stderr](原始输出/交易检索/nfr04-stderr.txt)、[退出码](原始输出/交易检索/nfr04-exit.txt)、[编译/散列/轻量检查](原始输出/交易检索/perfhttp-build.txt)。实际重建成功、Go1.27.2，工具SHA256为`bdbd2d9b08e4c5ba22ea7b5437bcbe85fb0195d8fc70391267d334cdb14ec6fa`，3条工具测试重跑通过1.196s。

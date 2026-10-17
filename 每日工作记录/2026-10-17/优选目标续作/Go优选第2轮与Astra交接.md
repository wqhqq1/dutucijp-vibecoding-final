# Go优选目标续作：第2轮与Astra交接

实际日期2026-10-17，Asia/Shanghai。本轮未提交、未制作最终材料；完成第2次正式实测后已停止生产源码修改，不追加Sol第3轮。根代理已按用户要求启动唯一GPT-6 Astra接续。

## 第2轮实现

store.go新增`persistBalances`：按计算结果已排序的唯一来源ID上界逐批写入，SQL使用`id>lower AND id<=upper`的primary-key范围，不使用累积OFFSET。每个事务最多4096余额，从当前已发布dataset的accepted/positive sale集合完整复制原量与初始零分配/满余量；核对INSERT的RowsAffected等于本批计算余额数。仅对实际有分配的销售执行稀疏UPDATE，守卫为run_id+sale_id+expectedOriginal+allocated=0+remaining=original，RowsAffected必须为1。全体余额完成后再写allocations，完整Compute/Validate、候选/详情证据、FK、WAL/FULL及最终SQL全量守恒均保留。

这条方案是在第1轮余额写2.89秒后尝试减少百万条Go参数绑定；实测没有使重算进一步变快，不将推测写成有效优化。是否保留该第二轮候选由Astra和整合方继续判断。

新增两条顶层反例/边界测试：第二余额事务失败后只保留前4096条staging不可见余额，整批回滚且重试恢复全部4116普通销售、10条排除和部分退货余量；最终UPDATE后触发器将分配改错、仍保持原量算式时，最终SQL核对拒绝发布。既有AFTER INSERT腐坏反例现在由稀疏更新的初值守卫拒绝，不能被静默修复。轻量store全包3.431秒通过。

Store结构中的`transactionCounts`字段按根代理请求代加，其具体缓存/查询逻辑、三个冗余索引的新库省略由根代理实现，本代理没有修改query.go或这些策略。

## 第2轮正式单次结果

原tools/measure-performance.ps1先NFR03后NFR02各一次、同机器串行。生产Go/SQL/Python在测量期间保持稳定；没有同时运行其他重型。Python已准确回退至其第1轮版本，不把失败的lxml候选混入源码。官方源只读、输入新库、prior run/output均0，未复用旧配对。输入模板复制SHA仍为`f9eaa0f575ae0c5c9ec4086183068d934f8d392aa7906905bde03001b96d241b`。

| 指标 | 第1轮 | 第2轮 | 优选 | 判定 |
| --- | ---: | ---: | ---: | --- |
| NFR03完整Store.Run+close | 6.9798736s | 6.9904288s | ≤5s | 两次优选均未达，≤15底线过 |
| NFR02完整首次 | 76.1350232s | 67.0101296s | ≤45s | 两次优选均未达，≤90底线过 |

NFR03第2轮Run6.940003秒+close0.0504258秒，进程峰值工作集1,082,494,976字节（含输入复制/open，非纯计算峰值）。phases如下：

| 阶段 | 秒 |
| --- | ---: |
| load | 1.7818973 |
| Compute | 1.1476588 |
| 余额集合写 | 2.9240447 |
| 其余决策/分配/差异/证据 | 0.3915547 |
| 全量SQL守恒 | 0.6790614 |
| persist合计 | 3.9946608 |

完整NFR02第2轮：Python进程23.0987373秒，Go总43.5222659秒，Import36.6438898秒、Run6.7622508秒、close0.104157秒；进程峰值1,093,783,552字节。Import阶段行处理14.6450608秒、索引20.7278701秒、SQL守恒0.9570435秒、前后SHA各约0.156秒。三个索引的新库省略使构建阶段由30.0698144降到20.7278701秒，是首次本轮下降的主要解释；不可将它归功于没有显著收益的余额集合写。

仍然是全部1,067,371原始、1,038,818接纳、28,553排除、1,019,654销售余额和19,164取消决策。配对9,714/疑似6,275/无原单3,175/差异9,450，`reused=false`，durable checks通过，与原基线相同。

## 原始证据与接续

运行库保存在项目外`verification/preferred-round2-NFR03-20261017`、`verification/preferred-round2-NFR02-20261017`。完整环境、源码/二进制散列、输入、计时和phases副本位于本目录：

- [NFR03完整输出](Go-第2轮-NFR03/go.stdout.txt)、[trial](Go-第2轮-NFR03/trial.json)、[environment](Go-第2轮-NFR03/environment.json)。
- [NFR02完整输出](Go-第2轮-NFR02/go.stdout.txt)、[trial](Go-第2轮-NFR02/trial.json)、[environment](Go-第2轮-NFR02/environment.json)、[Python进程](Go-第2轮-NFR02/prepare.process.json)。
- 原脚本console/error、进程记录、summary/trials也保留，不包含数据库、prepared CSV、原始Excel或秘密。

两目标各第2次失败已立即回报根代理；它已启动唯一`/root/astra_optimization`，本代理也已传达实证分解和源码入口。主瓶颈仍为SQL写余额/提交和首次索引构建，精简load后约1.78秒，Compute约1.15秒；没有通过改计时、省余额、跳守恒、缓存旧run或降持久化达标。此后核心性能由Astra接续，本代理不再修改生产源码、剖析或追加性能轮次。

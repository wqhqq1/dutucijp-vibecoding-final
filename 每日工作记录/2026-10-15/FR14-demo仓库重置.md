# FR-14 warehouse demo 一次性重置

实际日期2026-10-15，Asia/Shanghai。本人明确授权：“由于本次是demo，允许绕过规则直接重置”。此例外仅为已有独立demo的本地维护，不修改应用恢复规则或认证门禁，不计作标准恢复用例通过。

22:44:39由Codex本地维护执行；目标仅为demo-20261015-170120-d36101c6/business.sqlite中的warehouse。重置前账户complete/v4、已绑定TOTP、无Passkey、无会话；其他账户admin/finance均complete/v4。先在线备份到项目外private-auth受保护目录，再BEGIN IMMEDIATE：撤销warehouse旧会话/因素/恢复码/挑战/初始化链接，清空密码与TOTP，版本升至5并回到new；清除其账户级限流项，保留来源限流。事务内比较其他账户完整认证记录及全部原审计不变，追加auth_audit id=18，actor=codex_local_maintenance，如实标记本人授权、代理执行。业务表没有写入。

随后实际执行已有recon.exe init-auth --db <该独立demo数据库> --renew warehouse，退出0；仅生成私有初始化JSON，不读取或收存令牌。复核warehouse为new/v5、1个v5初始化入口、0会话，admin/finance仍complete/v4，首页HTTP200。应用未重启、未改代码；本人仍须先设置并保存强密码，再实际验证Passkey或TOTP、保存恢复码并完成配置。旧TOTP项目应由本人从认证器移除，再扫描新配置；不能继续使用旧密钥。

命令与无秘密原始结果见原始输出/FR14-demo仓库重置/一次性执行命令.py、实际结果.json、复核.json。脚本固定本次日期、独立demo路径和预期complete/v4，不可重跑；非产品重置接口。原备份包含敏感认证及业务数据，仅留项目外受保护运行目录，不进Git；初始化链接/二维码/OTP/密码/恢复码均不进Git或截图。本人忘记保存密码是本人反馈，不声称已完成新的配置。本轮不增加性能优化，软件整体未验收、PPT继续暂停。
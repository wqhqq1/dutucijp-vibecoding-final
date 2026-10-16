# 项目Hook启用前核对

已取消：2026-10-16本人要求按v1.2取消Hook计划及PPT演示。以下仅保留本轮核对历史，不再请求信任、启用或实际拦截，也不作为待办。

核对日期：2026-10-16。仅核对当前定义和准备本地配置，未代替本人信任、未使用跳过信任参数；实际拦截仍待真实工具调用。

- 定义：`.codex/hooks.json`，SHA-256 `1f08658c7ae29b0e61079e0fba948824fadf622c36dc06e26f0c9c30b1ef26e4`。
- 策略：`.codex/hooks/pre_tool_use_policy.py`，SHA-256 `80a84ee3384393a757376bc3d3b40b3206038aa3099ab70b5e1f3283ad119810`。
- 事件：`PreToolUse`，匹配`Bash`。策略仅从stdin读取拟执行命令，不执行该命令；命中Remove-Item、rm -rf、git reset --hard、git clean -f等模式时输出deny，其他输出为空。
- Windows入口使用既有隔离Python执行本项目策略；两个所需文件已存在。项目`.codex/config.toml`从既有example复制，仅含`features.hooks=true`，受Git忽略，不含凭据。
- 必须先核对原文件内容，再在项目Codex CLI使用`/hooks`审阅并信任当前定义。项目目录也需由本人确认可信。界面没有列出此项目来源时，不可随意信任其他Hook。
- 后续实测只针对项目外唯一命名、明确可丢弃的canary文本；记录真实Hook拒绝和文件仍存在的证据。不会请求删除源码、原件、数据库或私人文件，不用平台审批拒绝冒充项目Hook拦截。

官方依据：[Hooks](https://learn.chatgpt.com/docs/hooks)的“Where Codex looks for hooks”“Review and trust hooks”。非托管Hook在审阅信任前跳过，信任与当前定义的散列绑定。

以下操作需要本人在自己的CLI中完成：进入本项目的Codex会话，输入`/hooks`，审阅来源与上述定义，确认后选择信任。只信任这里核对过的项目Hook。

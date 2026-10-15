# FR-18 PowerShell测试入口兼容修复

实际日期2026-10-15，Asia/Shanghai。本人实际尝试`.\test.ps1`后提供NativeCommandError：首条Python用例显示ok，但test.ps1第15行原生命令重定向提前终止。本人没有提供完整Shell版本或修复后复跑结果，不能记本人已完成13/71条测试。

开始HEAD bebe77f，工作区干净；保留已有全部代码、日志和业务状态。此前自动验证实际使用PowerShell7.6.5，未覆盖Windows PowerShell5.1，这是执行者的验证遗漏。本轮在本机Windows PowerShell5.1.26100.9596原样复现退出1，非模拟业务故障。原脚本`$ErrorActionPreference='Stop'`配合`*>`捕获原生命令stderr，5.1把unittest正常进度转为NativeCommandError；之前7.6.5没有触发该路径。微软文档说明7.2起重定向原生命令错误记录的行为有变化：[about_Preference_Variables](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_preference_variables?view=powershell-7.6)。

修复：新增tools/invoke-test-process.ps1，用无Shell的ProcessStartInfo启动测试，两个异步流分别保存，按单个整数ExitCode判断，保留Stop以及真实失败抛错。Python stderr进度继续在python.txt，另存python.stdout.txt；Go JSON在go.jsonl，诊断另存go.stderr.txt。统一UTF-8读取，summary记录Shell版本和各流绝对路径。没有改业务代码、账户、认证、测试断言或性能实现；未追加第三轮性能优化，PPT仍暂停。

首次新包装器复验实际检出返回流读取任务对象混入退出码：两个版本底层测试均71/13通过，但summary的退出码为数组，入口正确地未报成功。补`[void]`丢弃任务返回对象后，以下最终复验通过。该轮实际失败目录PS51实测、PS7实测保留，不改写为通过。

| 实际命令（项目根） | 前提、预期与实际 | 原始输出/FR18-PowerShell兼容/ |
| --- | --- | --- |
| Windows powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./test.ps1 -OutputDir <新目录>（修复前） | 原未改脚本，真实首条ok即NativeCommandError，退出1，Go未进入 | 修复前.json、修复前.stdout.txt、修复前.stderr.txt；完整输出目录见json |
| 同命令（最终修复后） | 5.1.26100.9596，入口退出0；71 Go/13 Python通过，0失败/0跳过 | PS51最终.stdout.txt、PS51最终.stderr.txt、PS51最终复验/summary.json及两个原始流 |
| `./test.ps1 -OutputDir <PS7最终复验>` | PowerShell7.6.5，退出0；同样71/13通过，不与另一版本相加成不同用例 | PS7最终命令.txt、PS7最终复验/summary.json及两个原始流 |
| 两版本分别执行`./tests/test_native_runner.ps1 -OutputDir <新运行目录>` | 明确隔离子进程探针，正常stderr＋exit0仍为0，exit7仍为7；stdout/stderr各147456字节分开保存且中文完整。exit7是预期反例，非项目用例失败 | PS51进程反例.json、PS7进程反例.json及控制台文件；探针完整流在runtime/verification/native-probe-PS51-20261015-221636和native-probe-PS7-20261015-221636 |

上述runtime均指%LOCALAPPDATA%/dutucijp-vibecoding-final，源数据和秘密没有进入新证据。本轮测试只用既有隔离夹具，不操作官方业务历史。原始输出散列见同目录校验.json，不包含清单自身。

本人现在在原项目目录重新运行`.\test.ps1`即可，不需换终端或放宽全局错误策略。最终summary应有python_exit=0、go_exit=0、go_top_level_passed=71、go_top_level_failed=0、go_top_level_skipped=0、python_tests_ran=13。若真实测试失败，入口仍会抛错并保留诊断，不能忽略错误强报通过。本人的新复跑实际结果收到后再更新亲跑状态。

两项既有性能未达标和真机待做状态不变，PPT未制作；本轮仅FR-18测试入口兼容性修复。真实提交、交接保存后停写，由辅导对话按原分工采图、归档及插件备份，不能沿用旧备份回执。

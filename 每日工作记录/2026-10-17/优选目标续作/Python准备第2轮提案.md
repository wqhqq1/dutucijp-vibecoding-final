# Python准备第2轮提案

实际日期2026-10-17。完整第1轮首次76.135秒，45秒目标尚未达；本稿是第2轮实施前提案，不是实测结果。此时主执行者在做HTTP矩阵，未运行Python全量、未安装新库。

第1轮保留标准库ElementTree流读，每个v/c/row结束事件仍进入Python再判断tag。官方benchmark剖析中195,923次XML迭代事件才对应10,000行。提议用lxml.etree.iterparse的C端tag过滤，只返回完整row结束事件；共享字符串同样只返回si结束事件。节点处理结束后清空并释放已处理前兄弟，避免旧空行仍被父树保留。仍逐行解析源XML，保留行号、精确文本/数字词元、sheet顺序与全部八字段，继续用锁定openpyxl处理工作簿元数据与Excel日期，不读取旧产物替代源数据。

依据：[lxml官方解析文档](https://lxml.de/parsing.html)确认iterparse支持tag选择及流式清理；[官方性能说明](https://lxml.de/performance.html)同时说明Python节点包装可能产生代价，因此不能假定新增C库一定更快。无依赖SAX/自定义target仍需把每个start/end事件回调到Python，缺少同等的C端筛选优势。本轮先以相同官方benchmark和边界夹具验证实际收益/逐字节等价，再申请单次all独占窗口；不作多轮微调。

拟锁定官方[PyPI lxml 6.1.3](https://pypi.org/project/lxml/6.1.3/)，2026-09-02发布、未撤回。Windows x64 CPython3.12轮子 `lxml-6.1.3-cp312-cp312-win_amd64.whl`，4,005,999字节，SHA-256 `3e9a00d1c2c30936f7add097c41afc5da6556c580909104aafd382cac92a855c`，已从官方release JSON实际核实。直链为 https://files.pythonhosted.org/packages/3a/5b/6ed903e4e6278a020c8a6f0dbbe78030d041840a6b4a64ea441a1e414077/lxml-6.1.3-cp312-cp312-win_amd64.whl 。不需要本地C编译器；完整wheel内容及许可证要随可移植Python保留，实际嵌入版兼容性仍须小型导入/解析实测。

依赖传播范围：requirements.txt增加官方直链+SHA；environment.lock.json增加Python包版本；bootstrap.ps1仅Python依赖导入/版本检查；verify-environment.ps1增加版本和来源回执检查；portable/build_bundle.py的FILES与嵌入版运行探针增加lxml。父代理批准并确认文件无冲突后才实施，当前没有先装新库或改这些共享入口。

成功条件仍是全部小型清洗测试及完整官方CSV SHA与第1轮/旧版一致，实际记录依赖版本、源码SHA、测量缓存口径和进程峰值；Python阶段快不等于完整45秒首次目标达成。完整第2轮优化与测量仍未达目标时，由主执行者按用户要求交唯一Astra继续，本代理不擅开第三轮。

## 获准实施后的实测

主执行者批准依赖和小型/一次全量准备验证后，已按上述官方直链及SHA安装lxml6.1.3，pip check通过；同步候选requirements、bootstrap版本探针、环境锁/探针及portable的完整wheel清单/运行探针。14项清洗测试通过；官方benchmark全CSV散列与第1轮一致。一次同进程暖benchmark为标准库第1轮0.2589474秒、lxml0.3043548秒，存在变慢迹象。随后仅在同一提案内减少常见单v子节点的Python遍历，测得0.3027805秒，改善不足以区别噪声；未无限追加算法微调。

按获准独占窗口只运行一次all准备，10:43在新的项目外 `verification/python-prepare-round2-all-20261017-104346-a77bb286` 创建输出。进程25.8054172秒、内部24.7213346秒，分别比Python第1轮23.3741553/22.194899秒增加2.4312619/2.5264356秒。峰值709,722,112字节，比第1轮减少37,933,056字节（约36.2MiB）。缓存未主动清空，只是各一次样本，不能算稳定分位数。

完整1,067,371行、全部stats、两级守恒、全部CSV SHA与旧版完全一致；正确性通过，但本轮Python加速尝试失败，降低内存没有实现提速。原始环境、进程计时、stdout/stderr及核对结果在 `python-round2-all-*`。该阶段测量不是完整NFR02，完整45秒目标的第2次尝试仍由主执行者统筹。

已向主执行者报告并建议恢复第1轮Python，不保留没有速度收益的新依赖。当前候选尚未做可移植新目录构建（仅更新构建源码与本机venv导入/测试），不得写成已通过嵌入版兼容验收；若淘汰候选，无需再为它制作运行目录。重型窗口已释放，本代理不自行进入第三种Python算法；候选是否撤回由主执行者协调，失败记录保持。

## 已撤回候选，保留第1轮

主执行者明确要求撤回后，先保存 `python-round2-lxml-candidate.patch` 及相邻候选/恢复散列JSON，再只恢复本轮6个文件：prepare.py使用安装前留存的第1轮逐字节副本；requirements、bootstrap、verify-environment、environment.lock、portable/build_bundle使用本轮未修改前的Git版本。没有重置项目，没有覆盖Go/界面或其他代理修改，第1轮全部Python改动与新增两项测试保留。prepare.py当前SHA-256为 `98b7f7335b172de4b5df60356e1aa68d074dcce967fb8620fc13bca58f2206c7`。

依赖也校准为原锁定状态：两项原库按官方直链/SHA force-reinstall，lxml已卸除。普通路径pip最初因MSIX虚拟路径与物理路径规范化不同报无可卸文件；验证物理解释器与原venv为同一文件后，从该拥有的venv物理路径正常执行pip卸载成功，没有手工删除库目录。新进程importlib.metadata显示openpyxl3.1.5、et_xmlfile2.0.0、lxml不存在，find_spec(lxml)为None、openpyxl.LXML为False。实际状态见 `python-round2-restored-state.json`。恢复后14项准备测试和pip check通过，未再跑全量。

本轮lxml候选已淘汰，不属于可构建源码；它的候选补丁、失败测量和原始日志保留。主执行者将用第1轮Python加Go第2轮变更测完整目标，本代理已报告稳定点并停重型，不启动第三种Sol Python优化。

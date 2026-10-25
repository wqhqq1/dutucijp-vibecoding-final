# 隔离界面验证宿主

2026-10-25追加：`country-picker`检查长名称真实换行、不裁剪及原始值/已选缺省值保留；`account-tabs`检查官方SelectorBar四项切换、单项选中和两个待办角标。两种场景及edit-account/account-picker/filters/case-assignment/batch-scope/batch-results/requests在640、1100宽度共18项通过，见本日`FR21-国家地区与官方选项卡`。这些检查不代表物理设备或完整系统缩放验收。

这是独立测试程序，不打入发布目录，也不是正式后端或认证后门。链接真实客户端界面、ViewModel 和网络实现，使用临时 loopback 合成 HTTP 服务器，通过普通客户端协议取得合成会话，验证页面运行时加载。它不调用 Windows Hello/FIDO2，不读写用户设置、账户、业务数据库或原始数据。

使用 Visual Studio MSBuild 构建本项目，运行 `Reconciliation.UI.Tests.exe`，可传 `cases/transactions/allocations/review/jobs/accounts/requests/audit/security` 页面名。窗口明确标识“隔离合成数据”，不能将这些界面结果当作官方全量或本人操作证据。

## 参数与界面截图（2026-10-23 界面优化补充）

`Reconciliation.UI.Tests.exe [页面] [主题] [PNG路径] [逻辑窗口宽度]`

- 页面：上述页面名，另有 `case`（合成差异详情）、`settings`、`login`（只连接不登录）、`dialog`（合成记录详情）。裁剪回归场景为 `filters`（展开并滚动到筛选操作）、`review-assign`（切换到派单）、`account-picker`、`edit-account`（显示指定国家/地区）、`batch-results`、`dialog-resize`（弹窗打开后缩至640）。
- 主题：`Light`、`Dark` 或 `Default`；省略时为深色。
- 给出 PNG 路径时，窗口移到屏幕外，在进程内用 RenderTargetBitmap 渲染页面及打开的弹层（另存为 `-popup1.png` 等）后自动退出，不在桌面上弹出窗口。Mica 背景在截图中为透明。
- 项目链接客户端的 `app.manifest`（PerMonitorV2）。此前宿主没有清单，在高 DPI 屏幕上被系统位图拉伸而模糊；这与发布版客户端无关。修改清单后若未生效，删除 `obj/x64/Debug/.../Manifests/app.manifest` 再构建（WinAppSDK 合并清单的增量判断不会因外部清单更新而重做）。

宽度参数按当前 XamlRoot 缩放换算为窗口像素，窗口高度固定820逻辑像素；客户端区域因窗口边框略小。截图前检查自适应列和换行容器的子项横向边界、可见派单账户控件的实际尺寸、待审批/待配置计数及侧栏数字。账户弹窗另外验证搜索、角色组合筛选、选择与取消保留原值。筛选场景断言查询和重置按钮属于筛选控件树；缩窗场景断言已经打开的弹窗重新限宽。

2026-10-23此前14个场景：640和1100两种宽度分别运行上述六个裁剪/账户场景及`accounts`，其中`dialog-resize`会再次缩至640。原始日志及进程内渲染见`每日工作记录/2026-10-23/FR21-界面裁剪与账户选择`。这些是当前显示缩放下的合成测试，不能替代100%/150%/200%系统缩放、读屏、正式业务或物理另机验收。测试不点击保存、审批或批量提交。

国家/地区与居中追加：`case-assignment`使用有效国家/地区为法国、原始国家/地区为英国的合成差异，验证英国限定账户被隐藏及手输拒绝；`batch-scope`用51条英法记录验证跨页选择、全选、排除法国后允许英国账户、重新包含法国后拒绝，且非法手输未发送preview POST。`confirm`仅打开短确认弹窗，不确认写入。上述三种场景和原七种场景在640/1100宽度共20项通过，见`每日工作记录/2026-10-23/FR21-国家范围与弹窗居中`。

每个已打开ContentDialog都检查模板BackgroundElement相对客户区的实际坐标、宽高、水平/垂直中心误差和边界，允许2逻辑像素取整误差。本轮14个模态面板的最大实际中心误差约0.222逻辑像素；包括短确认、长详情/账户编辑/批量结果、账户选择及打开后缩窗。此前仅检查横向未溢出，不能证明居中。

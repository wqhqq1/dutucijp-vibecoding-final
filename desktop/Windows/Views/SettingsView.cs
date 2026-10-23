using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Reconciliation.Windows.ViewModels;

namespace Reconciliation.Windows.Views;
public sealed class SettingsView : UserControl
{
    public SettingsView(AppViewModel vm)
    {
        var address = UI.Field("后端地址", vm.Settings.Address);
        address.PlaceholderText = "http://192.168.1.20:8080";
        address.FontFamily = (Microsoft.UI.Xaml.Media.FontFamily)Application.Current.Resources["LedgerMonoFont"];
        var theme = new RadioButtons
        {
            Header = "外观",
            MaxColumns = 3
        };
        foreach (var(id, label)in new[]
        {
            ("Default", "跟随系统"),
            ("Light", "浅色"),
            ("Dark", "深色")
        }

        )
            theme.Items.Add(new RadioButton { Content = label, Tag = id });
        theme.SelectedIndex = Math.Max(0, Array.IndexOf(new[] { "Default", "Light", "Dark" }, vm.Settings.Theme));
        string Theme() => (theme.SelectedItem as RadioButton)?.Tag as string ?? "Default";
        var result = new InfoBar
        {
            IsOpen = true,
            IsClosable = false,
            Severity = InfoBarSeverity.Informational,
            Message = "支持 localhost、IPv4、IPv6 和域名；入口须已获管理员许可。"
        };
        var test = UI.Button(vm, "测试连接", async () =>
        {
            result.Severity = InfoBarSeverity.Informational;
            result.Title = "";
            result.Message = "正在连接…";
            try
            {
                var c = await vm.Probe(address.Text);
                result.Severity = InfoBarSeverity.Success;
                result.Title = "连接成功";
                result.Message = $"API {c.ApiVersion} · 后端身份 {c.BackendId}";
            }
            catch
            {
                result.Severity = InfoBarSeverity.Error;
                result.Title = "连接失败";
                result.Message = "请检查地址和服务是否已启动；详细原因见页面顶部。";
                throw;
            }
        }, Glyph.Plug);
        var save = UI.Primary(vm, "保存并连接", async () =>
        {
            await vm.Connect(address.Text, Theme());
            App.Window.ApplyTheme(Theme());
        }, Glyph.Save);
        var panel = UI.Stack(UI.PageHeader(vm.Settings.Configured ? "设置" : "连接后端", "选择已经启动的退货对账服务。客户端不会启动、停止或重置后端实例。"), UI.Section("连接与外观", UI.Stack(address, theme, UI.Flow(test, save), result), "保存后切换地址会清除当前会话与页面数据，需要重新登录；外观随保存生效。"));
        panel.Spacing = 16;
        if (vm.Configuration is { } cfg)
        {
            var g = new Grid
            {
                ColumnSpacing = 16,
                RowSpacing = 6
            };
            g.ColumnDefinitions.Add(new() { Width = new GridLength(124) });
            g.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
            int row = 0;
            foreach (var(label, value)in new[]
            {
                ("地址", vm.Settings.Address),
                ("后端身份", cfg.BackendId),
                ("API 版本", cfg.ApiVersion),
                ("原生 Passkey", cfg.NativePasskey.RpId),
                ("旧域名凭据", cfg.LegacyBindings.Length == 0 ? "无" : string.Join("、", cfg.LegacyBindings.Select(x => x.RpId)))
            }

            )
            {
                g.RowDefinitions.Add(new() { Height = GridLength.Auto });
                var name = UI.Muted(label);
                var text = UI.Mono(value, 13);
                Grid.SetRow(name, row);
                Grid.SetRow(text, row);
                Grid.SetColumn(text, 1);
                g.Add(name, text);
                row++;
            }

            panel.Children.Add(UI.Section("当前后端", g));
        }

        panel.Children.Add(UI.Section("关于", UI.Muted("退货对账与差异处理 Windows 客户端 1.0.0。设置只保存地址、主题和配置状态；密码、认证密钥和恢复码不写入本机文件，会话仅在当前进程内有效。", true)));
        panel.Padding = new Thickness(0, 0, 12, 24);
        Content = UI.Scroll(UI.Column(panel, 820));
    }
}

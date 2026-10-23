using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media.Imaging;
using Reconciliation.Windows.ViewModels;
using System.Security.Cryptography;
using System.Text;
using Windows.Storage.Streams;
using QRCoder;

namespace Reconciliation.Windows.Views;
public sealed class AuthView : UserControl
{
    readonly AppViewModel vm;
    readonly StackPanel panel = UI.Stack();
    readonly ContentControl enrollHost = UI.Host();
    readonly Grid frame;
    bool security;
    static readonly string[] SetupSteps = ["设置强密码", "绑定 Passkey 或 TOTP", "保存恢复码"];
    public AuthView(AppViewModel model, bool manage = false)
    {
        vm = model;
        security = manage;
        panel.Spacing = 16;
        panel.Padding = new Thickness(0, 8, 12, 24);
        frame = UI.Column(panel, double.PositiveInfinity);
        Content = UI.Scroll(frame);
        Render();
    }

    void Render()
    {
        panel.Children.Clear();
        var s = vm.Session;
        // Sign-in and setup are a centred column; the security page reads left-aligned like other pages.
        frame.ColumnDefinitions[0].MaxWidth = security ? 860 : double.PositiveInfinity;
        panel.MaxWidth = security ? double.PositiveInfinity : 560;
        if (s == null)
        {
            Login();
            _ = vm.Guard(LoadSetup);
            return;
        }

        if (s.State == "new")
        {
            panel.Add(Brand("完成安全配置", $"{s.Name}（{s.Id}）· 首次使用须依次完成三步"), UI.Steps(1, SetupSteps));
            PasswordSetup();
            return;
        }

        if (s.Scope == "login_factor" || s.Scope == "login_passkey")
        {
            panel.Children.Add(Brand("第二步验证", $"{s.Name}（{s.Id}）· 密码已验证"));
            var body = UI.Stack();
            if (s.HasTotp)
            {
                var otp = UI.Field("认证器验证码");
                otp.InputScope = new Microsoft.UI.Xaml.Input.InputScope
                {
                    Names =
                    {
                        new(Microsoft.UI.Xaml.Input.InputScopeNameValue.Number)
                    }
                };
                var verify = UI.Primary(vm, "验证并登录", () => vm.Authenticate("auth/totp/login", new { code = otp.Text }), Glyph.Accept);
                Submit(otp, verify);
                body.Add(otp, verify);
            }

            if (s.HasPasskey)
                body.Children.Add(PasskeyChoices(false));
            panel.Children.Add(UI.Section("选择验证方式", body));
            return;
        }

        if (security)
            panel.Children.Add(UI.PageHeader("账户安全", $"{s.Name}（{s.Id}）· {Labels.Of(s.Role)}"));
        else
            panel.Add(Brand("完成安全配置", $"{s.Name}（{s.Id}）"), UI.Steps(s.State == "factor" || vm.RecoveryCodes.Length > 0 ? 3 : 2, SetupSteps));
        Factors();
        if (security)
        {
            var password = UI.Password("新的强密码");
            password.HorizontalAlignment = HorizontalAlignment.Left;
            password.Width = 360;
            panel.Children.Add(UI.Section("更改密码", UI.Stack(password, UI.Button(vm, "更改密码", () => vm.Authenticate("auth/credentials/change", new { password = password.Password }), Glyph.Key)), "变更需在最近五分钟内完整认证；过期时请重新登录。"));
            panel.Children.Add(UI.Section("移除认证因素", UI.Flow(UI.Button(vm, "移除 Passkey", () => Remove("passkey"), Glyph.Delete), UI.Button(vm, "移除 TOTP", () => Remove("totp"), Glyph.Delete)), "仅在仍有其他有效因素时允许移除。"));
        }
    }

    static UIElement Brand(string title, string subtitle)
    {
        var mark = new Border
        {
            Style = (Style)Application.Current.Resources["BrandMarkStyle"],
            Width = 40,
            Height = 40,
            VerticalAlignment = VerticalAlignment.Center,
            Child = new TextBlock
            {
                Text = "账",
                FontSize = 22,
                FontWeight = Microsoft.UI.Text.FontWeights.Bold,
                Foreground = new Microsoft.UI.Xaml.Media.SolidColorBrush(Microsoft.UI.Colors.White),
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center
            }
        };
        var titles = new StackPanel
        {
            Spacing = 2,
            VerticalAlignment = VerticalAlignment.Center
        };
        titles.Add(UI.Heading(title, "TitleTextBlockStyle"), UI.Muted(subtitle));
        var row = UI.Flow(mark, titles);
        row.Spacing = 16;
        row.Margin = new Thickness(0, 16, 0, 4);
        return row;
    }

    // Enter in the last field runs the section's primary command.
    static void Submit(UIElement field, Button command) => field.KeyDown += (_, e) =>
    {
        if (e.Key == global::Windows.System.VirtualKey.Enter && command.IsEnabled)
        {
            e.Handled = true;
            var peer = new Microsoft.UI.Xaml.Automation.Peers.ButtonAutomationPeer(command);
            peer.Invoke();
        }
    };
    void Login()
    {
        panel.Children.Add(Brand("安全登录", vm.Settings.Address));
        var id = UI.Field("账户");
        var password = UI.Password("密码");
        password.MinWidth = 0;
        var login = UI.Primary(vm, "密码验证并继续", () => vm.Authenticate("auth/login", new { id = id.Text, password = password.Password }));
        login.HorizontalAlignment = HorizontalAlignment.Stretch;
        Submit(password, login);
        panel.Children.Add(UI.Section("账户与密码", UI.Stack(id, password, login), "密码验证后，按账户已绑定的 Passkey 或 TOTP 完成第二步。"));
        panel.Children.Add(UI.Section("使用 Passkey 登录", PasskeyChoices(false, id), "由 Windows Hello 或安全密钥选择凭据，自动登录对应账户。"));
        var code = UI.Password("一次性恢复码");
        var recovery = UI.Stack(UI.Muted("使用上方账户和密码，加一次性恢复码。恢复只授予重新绑定认证因素的受限权限。"), code, UI.Button(vm, "开始受限恢复", () => vm.Authenticate("auth/recover", new { id = id.Text, password = password.Password, code = code.Password }), Glyph.Shield));
        panel.Children.Add(new Expander { Header = "认证设备不可用，恢复访问", Content = recovery, HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
        var settings = new HyperlinkButton
        {
            Content = "更改后端地址或外观"
        };
        settings.Click += (_, _) => App.Window.ShowSettings();
        panel.Children.Add(settings);
    }

    async Task LoadSetup()
    {
        var gen = vm.Generation;
        var accounts = await vm.Api.Send("auth/setup/accounts", ct: vm.Token);
        if (gen != vm.Generation || vm.Session != null || accounts.AsArray().Count == 0)
            return;
        var list = UI.Stack();
        list.Spacing = 4;
        foreach (var a in accounts.AsArray())
        {
            var account = a!;
            var row = new Grid
            {
                ColumnSpacing = 12
            };
            row.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
            row.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
            var who = UI.Flow(UI.Text(account.S("display_name")), UI.Mono(account.S("id"), 13), UI.Badge(Labels.Of(account.S("role")), Tone.Neutral));
            who.Spacing = 10;
            var chevron = new FontIcon
            {
                Glyph = Glyph.Next,
                FontSize = 12
            };
            Grid.SetColumn(chevron, 1);
            row.Add(who, chevron);
            var b = UI.Button(vm, "", () => vm.Authenticate("auth/setup/start", new { id = account.S("id") }));
            b.Content = row;
            b.HorizontalAlignment = HorizontalAlignment.Stretch;
            b.HorizontalContentAlignment = HorizontalAlignment.Stretch;
            b.Padding = new Thickness(12, 8, 12, 8);
            Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(b, $"开始配置 {account.S("display_name")} {account.S("id")}");
            list.Children.Add(b);
        }

        panel.Children.Insert(1, UI.Section("首次配置", list, "以下本机账户尚未设置密码，点选即可开始受限配置。"));
    }

    void PasswordSetup()
    {
        var first = UI.Password("新密码（16–128字符）");
        var again = UI.Password("确认密码");
        first.MinWidth = again.MinWidth = 0;
        var set = UI.Primary(vm, "设置密码并继续", async () =>
        {
            if (first.Password != again.Password)
                throw new ArgumentException("两次密码不一致。");
            await vm.Authenticate("auth/setup/password", new { password = first.Password });
            first.Password = again.Password = "";
        }, Glyph.Next);
        Submit(again, set);
        panel.Children.Add(UI.Section("设置强密码", UI.Stack(first, again, UI.Flow(UI.Button(vm, "安全生成密码", () =>
        {
            first.Password = again.Password = Services.NativeWebAuthn.Encode(RandomNumberGenerator.GetBytes(24));
            first.PasswordRevealMode = PasswordRevealMode.Visible;
            again.PasswordRevealMode = PasswordRevealMode.Visible;
            return Task.CompletedTask;
        }, Glyph.Refresh), set)), "设置强密码后，验证自己的 Passkey 或 TOTP，再保存恢复码。生成的密码请存入密码管理器。"));
    }

    UIElement PasskeyChoices(bool register, TextBox? legacyAccount = null)
    {
        var bindings = new[]
        {
            vm.Configuration!.NativePasskey
        }.Concat(vm.Configuration.LegacyBindings).ToArray();
        var choice = UI.Select("Passkey 身份", bindings.Select(x => new Choice(x.Id, x.Id == "default" ? "此后端通用凭据" : $"旧域名凭据 · {x.RpId}")));
        var p = UI.Stack();
        // With only the backend-wide identity there is nothing to choose.
        if (bindings.Length > 1)
            p.Children.Add(choice);
        var go = (register ? UI.Button(vm, "绑定 Passkey", () => vm.Passkey(true, vm.Session?.Id, choice.Value()), Glyph.Fingerprint) : UI.Button(vm, "选择 Passkey 登录", () => vm.Passkey(false, null, choice.Value()), Glyph.Fingerprint));
        go.HorizontalAlignment = register ? HorizontalAlignment.Left : HorizontalAlignment.Stretch;
        p.Children.Add(go);
        if (!register && legacyAccount != null)
        {
            var named = new HyperlinkButton
            {
                Content = "按上方账户名使用指定凭据（旧凭据兼容）",
                Padding = new Thickness(0)
            };
            named.Click += async (_, _) =>
            {
                named.IsEnabled = false;
                try
                {
                    await vm.Guard(() => vm.Passkey(false, legacyAccount.Text, choice.Value()));
                }
                finally
                {
                    named.IsEnabled = true;
                }
            };
            p.Children.Add(named);
        }

        return p;
    }

    void Factors()
    {
        var s = vm.Session!;
        var state = UI.Flow(UI.Badge("Passkey " + (s.HasPasskey ? "已绑定" : "未绑定"), s.HasPasskey ? Tone.Done : Tone.Neutral), UI.Badge("TOTP " + (s.HasTotp ? "已绑定" : "未绑定"), s.HasTotp ? Tone.Done : Tone.Neutral));
        var factors = UI.Stack(state, PasskeyChoices(true));
        if (!s.HasTotp)
            factors.Children.Add(UI.Button(vm, "绑定认证器 TOTP", Enroll, Glyph.Phone));
        factors.Children.Add(enrollHost);
        panel.Children.Add(UI.Section("认证因素", factors, security ? "至少保留一种已验证的 Passkey 或 TOTP。" : "绑定并实际验证至少一种：Windows Hello / 安全密钥，或手机认证器。"));
        var recovery = UI.Stack(UI.Button(vm, "重新生成恢复码", () => vm.Authenticate("auth/recovery/reissue", new { }), Glyph.Refresh));
        if (vm.RecoveryCodes.Length > 0)
        {
            var codes = vm.RecoveryCodes.ToArray();
            var grid = UI.Columns(2, codes.Select(x => (UIElement)UI.Mono(x, 15)).ToArray());
            grid.RowSpacing = 6;
            var reveal = new Expander
            {
                Header = "查看本次恢复码",
                Content = grid,
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Stretch
            };
            var saved = new CheckBox
            {
                Content = "我已在私人位置安全保存恢复码"
            };
            recovery.Add(UI.Muted("恢复码仅展示本次，每个只能使用一次；请勿截入公开证据。"), reveal, UI.Button(vm, "保存恢复码到私人文件", () => UI.Save(Encoding.UTF8.GetBytes(string.Join("\n", codes)), "private-recovery-codes", ".txt"), Glyph.Save), saved, UI.Primary(vm, "完成安全配置", async () =>
            {
                if (saved.IsChecked != true)
                    throw new InvalidOperationException("请先保存恢复码并勾选确认。");
                await vm.Authenticate("auth/setup/complete", new { saved = true });
            }, Glyph.Accept));
        }

        panel.Children.Add(UI.Section("恢复码", recovery, "认证设备不可用时，用一次性恢复码取得受限恢复权限。"));
    }

    async Task Enroll()
    {
        var value = await vm.Api.Send("auth/totp/enroll", new { });
        var secret = UI.Mono(value.S("secret"));
        using var generator = new QRCodeGenerator();
        using var qr = generator.CreateQrCode(value.S("uri"), QRCodeGenerator.ECCLevel.M);
        // Render whole device pixels per module so the code stays sharp at any display scale.
        double scale = XamlRoot?.RasterizationScale ?? 1;
        int modules = qr.ModuleMatrix.Count;
        int perModule = Math.Max(2, (int)Math.Ceiling(220 * scale / modules));
        using var png = new PngByteQRCode(qr);
        var bytes = png.GetGraphic(perModule);
        using var stream = new InMemoryRandomAccessStream();
        using (var writer = new DataWriter(stream.GetOutputStreamAt(0)))
        {
            writer.WriteBytes(bytes);
            await writer.StoreAsync();
        }

        stream.Seek(0);
        var bitmap = new BitmapImage();
        await bitmap.SetSourceAsync(stream);
        double size = modules * perModule / scale;
        var image = new Image
        {
            Source = bitmap,
            Width = size,
            Height = size,
            Stretch = Microsoft.UI.Xaml.Media.Stretch.Fill,
            HorizontalAlignment = HorizontalAlignment.Left
        };
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(image, "TOTP 绑定二维码");
        var otp = UI.Field("认证器当前验证码");
        otp.Width = 220;
        otp.HorizontalAlignment = HorizontalAlignment.Left;
        var confirm = UI.Primary(vm, "验证绑定", () => vm.Authenticate("auth/totp/confirm", new { code = otp.Text }), Glyph.Accept);
        Submit(otp, confirm);
        var frame = new Border
        {
            Background = new Microsoft.UI.Xaml.Media.SolidColorBrush(Microsoft.UI.Colors.White),
            Padding = new Thickness(8),
            CornerRadius = new CornerRadius(6),
            HorizontalAlignment = HorizontalAlignment.Left,
            Child = image
        };
        enrollHost.Content = UI.Card(UI.Stack(UI.Muted("使用自己的认证器扫码；绑定资料仅显示在当前页面。"), frame, new Expander { Header = "无法扫码时手动输入密钥", Content = secret }, otp, confirm));
    }

    async Task Remove(string factor)
    {
        if (await UI.Confirm("移除认证因素", UI.Text("仅在仍有其他有效因素时允许移除。"), "移除"))
            await vm.Authenticate("auth/credentials/change", new { remove = factor });
    }
}

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;

namespace Reconciliation.Windows.Views;
public sealed class AccountsView : UserControl
{
    readonly AppViewModel vm;
    readonly string mode;
    readonly StackPanel header = UI.Stack();
    readonly ContentControl body = UI.Host();
    JsonArray accounts = [], requests = [], countries = [];
    string tab = "directory";
    long request;
    readonly Dictionary<string, (Microsoft.UI.Xaml.Controls.Primitives.ToggleButton Button, InfoBadge Badge)> tabs = [];
    void AttentionChanged(object? sender, System.ComponentModel.PropertyChangedEventArgs e)
    {
        if (e.PropertyName == "Attention")
            UpdateTabs();
    }

    void UpdateTabs()
    {
        foreach (var(key, item)in tabs)
        {
            item.Button.IsChecked = key == tab;
            int count = key == "requests" ? vm.PendingRequests : key == "directory" ? vm.UnconfiguredAccounts : 0;
            item.Badge.Value = count;
            item.Badge.Visibility = count == 0 ? Visibility.Collapsed : Visibility.Visible;
        }
    }

    public AccountsView(AppViewModel model, string kind)
    {
        vm = model;
        mode = kind;
        if (mode == "requests")
            tab = "requests";
        header.Children.Add(UI.PageHeader(mode == "accounts" ? "账户管理" : "仓库开户申请", mode == "accounts" ? "创建和维护财务、仓库账户及负责国家，审批开户申请，管理统一许可入口。" : "为负责国家申请新的仓库账户，查看审批进度。", UI.Button(vm, "刷新", Load, Glyph.Refresh)));
        var tabBar = UI.Flow();
        foreach (var(key, label)in mode == "accounts" ? new[]
        {
            ("directory", "账户名录"),
            ("requests", "开户申请"),
            ("audit", "操作记录"),
            ("origins", "许可入口")
        }

        : new[]
        {
            ("requests", "申请记录")
        }

        )
        {
            var badge = new InfoBadge
            {
                Visibility = Visibility.Collapsed
            };
            var button = new Microsoft.UI.Xaml.Controls.Primitives.ToggleButton
            {
                Content = UI.Flow(UI.Text(label), badge),
                IsChecked = key == tab
            };
            Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(button, label);
            button.Click += async (_, _) =>
            {
                tab = key;
                UpdateTabs();
                await vm.Guard(Load);
            };
            tabs[key] = (button, badge);
            tabBar.Children.Add(button);
        }

        header.Children.Add(tabBar);
        UpdateTabs();
        Content = UI.Layout(header, body);
        Loaded += async (_, _) =>
        {
            vm.PropertyChanged += AttentionChanged;
            UpdateTabs();
            await vm.Guard(Load);
        };
        Unloaded += (_, _) => vm.PropertyChanged -= AttentionChanged;
    }

    async Task Load()
    {
        var gen = vm.Generation;
        var serial = ++request;
        string requestedTab = tab;
        countries = (await vm.Api.Send("countries", ct: vm.Token)).AsArray();
        JsonNode value = await vm.Api.Send(requestedTab switch
        {
            "directory" => "admin/accounts",
            "audit" => "admin/account-audit",
            "origins" => "admin/passkey-domains",
            _ => "warehouse-requests"
        }, ct: vm.Token);
        if (gen != vm.Generation || serial != request || requestedTab != tab)
            return;
        await vm.RefreshAttention();
        if (gen != vm.Generation || serial != request || requestedTab != tab)
            return;
        if (tab == "directory")
        {
            accounts = value.AsArray();
            Directory();
        }
        else if (tab == "requests")
        {
            requests = value.AsArray();
            Requests();
        }
        else if (tab == "origins")
            Origins(value);
        else
            body.Content = UI.Table(value.AsArray(), [new("occurred_at", "时间", 230, ColumnKind.Mono), new("actor", "操作人"), new("action", "操作"), new("target", "目标"), new("request_id", "申请ID", 200, ColumnKind.Mono)], n => _ = vm.Guard(() => UI.Show("管理审计", UI.Details(n))));
    }

    string[] CountryValues => countries.Select(x => x!.ToString()).ToArray();

    void Directory()
    {
        int Count(Func<JsonNode?, bool> f) => accounts.Count(f);
        var figures = UI.Metrics(("账户总数", accounts.Count.ToString(), Tone.Neutral), ("财务", Count(n => n.S("role") == "finance").ToString(), Tone.Neutral), ("仓库", Count(n => n.S("role") == "warehouse").ToString(), Tone.Neutral), ("管理员", Count(n => n.S("role") == "admin").ToString(), Tone.Neutral), ("待配置", Count(n => n.S("state") != "complete" && n.S("state") != "deleted").ToString(), Tone.Pending), ("已配置", Count(n => n.S("state") == "complete").ToString(), Tone.Done));
        var query = UI.Field("搜索账户名、显示名或国家");
        var roleFilter = UI.Select("角色", UI.Choices(("", "全部"), ("finance", "财务"), ("warehouse", "仓库"), ("admin", "管理员")));
        var stateFilter = UI.Select("配置状态", UI.Choices(("", "全部"), ("new", "未设密码"), ("password", "待绑定因素"), ("factor", "待保存恢复码"), ("complete", "已配置")));
        var list = UI.Host();
        void Filter()
        {
            var rows = new JsonArray();
            foreach (var n in accounts)
            {
                if (roleFilter.Value() != "" && n.S("role") != roleFilter.Value() || stateFilter.Value() != "" && n.S("state") != stateFilter.Value())
                    continue;
                if (!n!.ToJsonString().Contains(query.Text, StringComparison.OrdinalIgnoreCase))
                    continue;
                rows.Add(n.DeepClone());
            }

            list.Content = UI.Table(rows, [new("id", "账户", 150, ColumnKind.Mono), new("display_name", "显示名", 160), new("role", "角色", 90), new("state", "配置状态", 120, ColumnKind.Status), new("countries", "负责国家", 260)], n => _ = vm.Guard(() => Edit(n)));
        }

        query.TextChanged += (_, _) => Filter();
        roleFilter.SelectionChanged += (_, _) => Filter();
        stateFilter.SelectionChanged += (_, _) => Filter();
        Filter();
        var create = UI.Primary(vm, "创建账户", () => Edit(null), Glyph.Add);
        create.VerticalAlignment = VerticalAlignment.Bottom;
        var filters = UI.Columns(4, query, roleFilter, stateFilter, create);
        body.Content = UI.Layout(UI.Stack(figures, filters, UI.Muted("负责国家为空表示全部国家。点击一行编辑或删除账户。", true)), list);
    }

    async Task Edit(JsonNode? account)
    {
        bool editing = account != null;
        var id = UI.Field("账户名", account.S("id"));
        id.IsEnabled = !editing;
        id.PlaceholderText = "3–64 位小写字母、数字、点、下划线或连字符";
        var name = UI.Field("显示名称", account.S("display_name"));
        var role = UI.Select("角色", UI.Choices(("finance", "财务"), ("warehouse", "仓库"), ("admin", "管理员")), account.S("role"));
        role.IsEnabled = !editing;
        var scope = UI.Select("国家范围", UI.Choices(("all", "全部国家"), ("selected", "指定国家")), account.S("country_scope"));
        var(picker, chosen) = UI.CountryPicker(CountryValues, account.A("countries").Select(x => x!.ToString()));
        var countryArea = UI.Stack(UI.Muted("负责国家", true), picker);
        countryArea.Spacing = 6;
        void Scope() => countryArea.Visibility = scope.Value() == "selected" ? Visibility.Visible : Visibility.Collapsed;
        scope.SelectionChanged += (_, _) => Scope();
        Scope();
        var form = UI.Stack(UI.Columns(2, id, name, role, scope), countryArea);
        if (editing)
            form.Children.Add(UI.Muted("登录名和角色不可修改；修改国家会撤销该账户原会话。", true));
        form.MinWidth = 0;
        var dialog = UI.Dialog(editing ? "编辑账户" : "创建账户", form);
        dialog.PrimaryButtonText = "保存";
        dialog.CloseButtonText = "取消";
        dialog.SecondaryButtonText = editing ? "删除账户" : "";
        var result = await dialog.ShowAsync();
        if (result == ContentDialogResult.Secondary)
        {
            await Delete(account!);
            return;
        }

        if (result == ContentDialogResult.Primary)
        {
            var selectedCountries = scope.Value() == "selected" ? chosen() : Array.Empty<string>();
            object payload = editing ? new
            {
                display_name = name.Text,
                country_scope = scope.Value(),
                countries = selectedCountries
            }

            : new
            {
                id = id.Text,
                display_name = name.Text,
                role = role.Value(),
                country_scope = scope.Value(),
                countries = selectedCountries
            };
            await vm.Api.Send(editing ? "admin/accounts/" + Uri.EscapeDataString(id.Text) : "admin/accounts", payload, editing ? HttpMethod.Patch : HttpMethod.Post);
            vm.Message(editing ? "账户已更新；权限变更按后端规则撤销原会话。" : "账户已创建；使用者需自行完成首次安全配置。");
            await Load();
        }
    }

    async Task Delete(JsonNode account)
    {
        var path = "admin/accounts/" + Uri.EscapeDataString(account.S("id"));
        var impact = await vm.Api.Send(path + "/deletion-preview", ct: vm.Token);
        var summary = UI.Metrics(("未办结差异", impact.I("open_cases").ToString(), Tone.Pending), ("已关闭差异", impact.I("closed_cases").ToString(), Tone.Neutral), ("Passkey", impact.I("passkey_count").ToString(), Tone.Danger));
        if (await UI.Confirm("删除账户并撤销访问", UI.Stack(UI.Text($"账户 {account.S("display_name")}（{account.S("id")}）"), summary, UI.Muted($"未办结 {impact.I("open_cases")} 条将退回未派单；已关闭 {impact.I("closed_cases")} 条保留。Passkey {impact.I("passkey_count")} 个将撤销，设备中的旧凭据可在系统密码管理器清理。")), "删除"))
        {
            await vm.Api.Send(path, new { }, HttpMethod.Delete);
            vm.Message("账户已删除，历史保留；未办结差异已退回未派单。");
            await Load();
        }
    }

    void Requests()
    {
        var filter = UI.Select("申请状态", UI.Choices(("", "全部"), ("pending", "待审批"), ("approved", "已批准"), ("rejected", "已驳回")));
        filter.HorizontalAlignment = HorizontalAlignment.Left;
        filter.MinWidth = 200;
        var create = UI.Primary(vm, "申请仓库账户", CreateRequest, Glyph.Add);
        create.VerticalAlignment = VerticalAlignment.Bottom;
        var table = UI.Host();
        void Show()
        {
            var rows = new JsonArray();
            foreach (var r in requests)
                if (filter.Value() == "" || r.S("status") == filter.Value())
                    rows.Add(r!.DeepClone());
            table.Content = UI.Table(rows, [new("account_id", "账户名", 160, ColumnKind.Mono), new("display_name", "显示名", 150), new("requester", "申请人", 150), new("status", "状态", 110, ColumnKind.Status), new("countries", "国家", 240)], n => _ = vm.Guard(() => ReviewRequest(n)));
        }

        filter.SelectionChanged += (_, _) => Show();
        Show();
        var pending = requests.Count(r => r.S("status") == "pending");
        var controls = UI.Flow(filter, create);
        controls.Spacing = 12;
        body.Content = UI.Layout(UI.Stack(controls, UI.Muted(pending > 0 ? $"待审批 {pending} 条。批准即开户，新账户仍须本人完成首次安全配置。" : "批准即开户，新账户仍须本人完成首次安全配置。", true)), table);
    }

    async Task CreateRequest()
    {
        var id = UI.Field("新仓库账户名");
        var name = UI.Field("显示名");
        var scope = UI.Select("国家范围", UI.Choices(("selected", "指定国家"), ("all", "全部国家")));
        var(picker, chosen) = UI.CountryPicker(CountryValues, vm.Session!.Countries);
        var countryArea = UI.Stack(UI.Muted("国家（须在本人负责范围内）", true), picker);
        countryArea.Spacing = 6;
        void Scope() => countryArea.Visibility = scope.Value() == "selected" ? Visibility.Visible : Visibility.Collapsed;
        scope.SelectionChanged += (_, _) => Scope();
        Scope();
        var reason = UI.Field("申请理由（可选）", "", true);
        var form = UI.Stack(UI.Columns(2, id, name), scope, countryArea, reason);
        form.MinWidth = 0;
        if (await UI.Confirm("申请仓库账户", form, "提交申请"))
        {
            await vm.Api.Send("warehouse-requests", new { id = id.Text, display_name = name.Text, role = "warehouse", country_scope = scope.Value(), countries = scope.Value() == "selected" ? chosen() : Array.Empty<string>(), reason = reason.Text });
            vm.Message("申请已提交，等待管理员审批。");
            await Load();
        }
    }

    async Task ReviewRequest(JsonNode req)
    {
        var canReview = vm.Session!.Role == "admin" && req.S("status") == "pending";
        var reason = UI.Field("驳回理由（驳回时必填）", "", true);
        var p = UI.Stack(UI.Flow(UI.Mono(req.S("account_id"), 16), UI.Status(req.S("status"))), UI.Details(req));
        if (canReview)
            p.Children.Add(reason);
        p.MinWidth = 0;
        var d = UI.Dialog("开户申请详情", p);
        d.CloseButtonText = "关闭";
        d.PrimaryButtonText = canReview ? "批准并开户" : "";
        d.SecondaryButtonText = canReview ? "驳回申请" : "";
        var result = await d.ShowAsync();
        if (!canReview || result == ContentDialogResult.None)
            return;
        if (result == ContentDialogResult.Primary)
            await vm.Api.Send("warehouse-requests/" + req.S("id") + "/approve", new { });
        else
            await vm.Api.Send("warehouse-requests/" + req.S("id") + "/reject", new { reason = reason.Text });
        await Load();
    }

    void Origins(JsonNode policy)
    {
        var entries = UI.Field("额外许可入口（每行一个完整 HTTP/HTTPS 地址）", string.Join("\n", policy.A("origins").Select(x => x!.ToString())), true);
        entries.MinHeight = 160;
        entries.FontFamily = (Microsoft.UI.Xaml.Media.FontFamily)Application.Current.Resources["LedgerMonoFont"];
        var fixedOrigin = UI.Flow(UI.Muted("固定本机入口"), UI.Mono(policy.S("base_origin")), UI.Badge($"版本 {policy.I("version")}", Tone.Neutral));
        fixedOrigin.Spacing = 12;
        var save = UI.Primary(vm, "保存许可入口", async () =>
        {
            await vm.Api.Send("admin/passkey-domains", new { version = policy.I("version"), origins = entries.Text.Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries) }, HttpMethod.Put);
            vm.Message("许可入口已保存，未完成的 Passkey 挑战已失效。");
            await Load();
        }, Glyph.Save);
        var p = UI.Stack(fixedOrigin, entries, UI.Muted("最多 20 个额外入口。获准的 localhost、IP 和域名均可完整使用 Windows 客户端，同一后端共用原生 Passkey。HTTPS 证书须通过系统校验。", true), save);
        var section = UI.Section("统一许可入口", p);
        section.MaxWidth = 860;
        section.HorizontalAlignment = HorizontalAlignment.Left;
        body.Content = UI.Scroll(section);
    }
}

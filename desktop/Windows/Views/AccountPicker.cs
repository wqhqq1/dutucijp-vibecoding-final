using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Markup;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;

namespace Reconciliation.Windows.Views;
public sealed class AccountPicker : UserControl
{
    readonly AppViewModel vm;
    readonly TextBox input;
    readonly Func<Task<string[]>>? requiredCountries;
    public string Text { get => input.Text; set => input.Text = value; }

    public AccountPicker(AppViewModel model, string label, Func<Task<string[]>>? countries = null)
    {
        vm = model;
        requiredCountries = countries;
        input = UI.Field(label);
        input.PlaceholderText = "输入账户名或点击选择";
        var choose = UI.Button(vm, "选择账户", async () => await CreateDialog().ShowAsync(), Glyph.People);
        choose.Content = new FontIcon
        {
            Glyph = Glyph.People,
            FontSize = 16
        };
        choose.Padding = new Thickness(8);
        ToolTipService.SetToolTip(choose, "选择、搜索及筛选账户");
        choose.VerticalAlignment = VerticalAlignment.Bottom;
        var grid = new Grid
        {
            ColumnSpacing = 8
        };
        grid.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
        Grid.SetColumn(choose, 1);
        grid.Children.Add(input);
        grid.Children.Add(choose);
        Content = grid;
        AutomationProperties.SetName(this, label);
    }

    Task<JsonNode> Users(CancellationToken ct)
    {
        var session = vm.Session!;
        return session.Role == "warehouse" ? Task.FromResult(Json.Node(new[] { new { id = session.Id, display_name = session.Name, role = session.Role, state = session.State, country_scope = session.CountryScope, countries = session.Countries } })) : vm.Api.Send("users", ct: ct);
    }

    public async Task ValidateSelection()
    {
        var gen = vm.Generation;
        var ct = vm.Token;
        string id = Text.Trim();
        var required = requiredCountries == null ? null : await requiredCountries();
        var users = await Users(ct);
        if (gen != vm.Generation || id != Text.Trim())
            throw new OperationCanceledException("账户选择已变化，请重试。");
        var user = users.AsArray().FirstOrDefault(u => u.S("id") == id);
        if (user == null || !AccountDirectory.Covers(user, required))
            throw new ArgumentException("请选择能覆盖全部所选记录国家/地区的账户；当前账户不在可派单范围内。");
    }

    // A dedicated, bounded picker keeps long display names/country scopes readable.
    public ContentDialog CreateDialog()
    {
        var query = UI.Field("搜索账户名、显示名或国家/地区");
        var role = UI.Select("角色", UI.Choices(("", "全部角色"), ("finance", "财务"), ("warehouse", "仓库"), ("admin", "管理员")));
        var country = UI.Select("负责国家/地区", [new Choice("", "全部国家/地区范围")]);
        var count = UI.Muted("正在读取账户…");
        var rows = new ListView
        {
            SelectionMode = ListViewSelectionMode.Single,
            HorizontalContentAlignment = HorizontalAlignment.Stretch
        };
        rows.ItemTemplate = (DataTemplate)XamlReader.Load("<DataTemplate xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'><StackPanel Padding='8,10' Spacing='4'><TextBlock Text='{Binding Name}' TextWrapping='Wrap' FontWeight='SemiBold'/><TextBlock Text='{Binding Scope}' TextWrapping='Wrap'/></StackPanel></DataTemplate>");
        AutomationProperties.SetName(rows, "可选账户");
        var grid = new Grid
        {
            Height = Math.Max(200, Math.Min(500, App.Window.XamlRoot.Size.Height - 260)),
            RowSpacing = 12
        };
        grid.RowDefinitions.Add(new() { Height = GridLength.Auto });
        grid.RowDefinitions.Add(new() { Height = GridLength.Auto });
        grid.RowDefinitions.Add(new() { Height = new GridLength(1, GridUnitType.Star) });
        grid.Children.Add(UI.Stack(query, UI.Columns(2, role, country)));
        Grid.SetRow(count, 1);
        grid.Children.Add(count);
        Grid.SetRow(rows, 2);
        grid.Children.Add(rows);
        var dialog = UI.Dialog("选择账户", grid);
        dialog.PrimaryButtonText = "选择此账户";
        dialog.CloseButtonText = "取消";
        dialog.IsPrimaryButtonEnabled = false;
        JsonNode[] users = [];
        string[]? required = null;
        int userCount = 0;
        var gen = vm.Generation;
        void Filter()
        {
            rows.ItemsSource = users.Where(u => AccountDirectory.Matches(u, query.Text, role.Value(), country.Value())).Select(u => new AccountOption(u.S("id"), $"{u.S("display_name")} · {u.S("id")}", $"{Labels.Of(u.S("role"))} · {Labels.Of(u.S("state"))} · {(u.S("country_scope") == "all" ? "全部国家/地区" : UI.Format(u, "countries"))}")).ToArray();
            count.Text = required == null ? $"共 {users.Length} 个账户 · 当前 {rows.Items.Count} 个匹配" : $"共 {userCount} 个账户 · {users.Length} 个覆盖所选国家/地区 · 当前 {rows.Items.Count} 个匹配";
            dialog.IsPrimaryButtonEnabled = false;
            rows.SelectedItem = rows.Items.Cast<AccountOption>().FirstOrDefault(x => x.Id == Text);
        }

        query.TextChanged += (_, _) => Filter();
        role.SelectionChanged += (_, _) => Filter();
        country.SelectionChanged += (_, _) => Filter();
        rows.SelectionChanged += (_, _) => dialog.IsPrimaryButtonEnabled = rows.SelectedItem is AccountOption;
        dialog.Opened += async (_, _) => await vm.Guard(async () =>
        {
            var ct = vm.Token;
            count.Text = requiredCountries == null ? "正在读取账户…" : "正在核对所选国家/地区范围…";
            required = requiredCountries == null ? null : await requiredCountries();
            if (gen != vm.Generation)
                return;
            var result = await Users(ct);
            if (gen != vm.Generation)
                return;
            userCount = result.AsArray().Count;
            users = result.AsArray().Where(x => x != null && AccountDirectory.Covers(x, required)).Select(x => x!).ToArray();
            foreach (var name in users.SelectMany(x => x.A("countries")).Select(x => x!.ToString()).Distinct().Order())
                country.Items.Add(new Choice(name, name));
            Filter();
        });
        dialog.PrimaryButtonClick += (_, e) =>
        {
            if (gen == vm.Generation && rows.SelectedItem is AccountOption picked && users.Any(u => u.S("id") == picked.Id && AccountDirectory.Covers(u, required)))
                Text = picked.Id;
            else
                e.Cancel = true;
        };
        return dialog;
    }

    public sealed record AccountOption(string Id, string Name, string Scope)
    {
        public override string ToString() => $"{Name}，{Scope}";
    }
}

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Reconciliation.Windows.ViewModels;
using Reconciliation.Windows.Views;

namespace Reconciliation.Windows;
public sealed partial class MainWindow : Window
{
    public AppViewModel VM { get; }
    public XamlRoot XamlRoot => Root.XamlRoot;
    /// <summary>Theme actually shown, for popups that do not inherit Root.RequestedTheme.</summary>
    public ElementTheme Theme => Root.ActualTheme;

    string page = "cases";
    bool ready, syncing;
    readonly Microsoft.UI.Dispatching.DispatcherQueueTimer attentionTimer;
    bool attentionRefreshing;
    public MainWindow(AppViewModel? model = null)
    {
        VM = model ?? new();
        LedgerTheme.Ensure();
        InitializeComponent();
        attentionTimer = DispatcherQueue.CreateTimer();
        attentionTimer.Interval = TimeSpan.FromSeconds(30);
        attentionTimer.Tick += async (_, _) => await RefreshAttention();
        attentionTimer.Start();
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(AppTitleBar);
        AppWindow.Resize(new global::Windows.Graphics.SizeInt32(1320, 900));
        VM.PropertyChanged += (_, _) => DispatcherQueue.TryEnqueue(UpdateStatus);
        VM.SessionChanged += () => DispatcherQueue.TryEnqueue(() =>
        {
            UpdateShell();
            ShowPage();
            _ = RefreshAttention();
        });
        Root.Loaded += async (_, _) =>
        {
            if (ready)
                return;
            ready = true;
            var scale = Root.XamlRoot.RasterizationScale;
            var work = Microsoft.UI.Windowing.DisplayArea.GetFromWindowId(AppWindow.Id, Microsoft.UI.Windowing.DisplayAreaFallback.Nearest).WorkArea;
            AppWindow.Resize(new global::Windows.Graphics.SizeInt32((int)Math.Min(1320 * scale, work.Width * 0.94), (int)Math.Min(880 * scale, work.Height * 0.94)));
            ApplyTheme(VM.Settings.Theme);
            UpdateShell();
            ShowSettings();
            if (VM.Settings.Configured)
                await VM.Guard(VM.Resume);
        };
        Closed += (_, _) =>
        {
            attentionTimer.Stop();
            VM.Dispose();
        };
    }

    public void ApplyTheme(string theme) => Root.RequestedTheme = Enum.TryParse<ElementTheme>(theme, out var t) ? t : ElementTheme.Default;
    void PaneToggleRequested(TitleBar sender, object args) => Nav.IsPaneOpen = !Nav.IsPaneOpen;
    void UpdateStatus()
    {
        UpdateBadges();
        Activity.Visibility = VM.Busy ? Visibility.Visible : Visibility.Collapsed;
        if (VM.Error != "")
        {
            Feedback.Severity = InfoBarSeverity.Error;
            Feedback.Title = "未完成";
            Feedback.Message = VM.Error;
            Feedback.IsOpen = true;
        }
        else if (VM.Notice != "")
        {
            Feedback.Severity = InfoBarSeverity.Success;
            Feedback.Title = "";
            Feedback.Message = VM.Notice;
            Feedback.IsOpen = true;
        }
        else
            Feedback.IsOpen = false;
    }

    IEnumerable<NavigationViewItem> NavItems => Nav.MenuItems.Concat(Nav.FooterMenuItems).OfType<NavigationViewItem>();

    async Task RefreshAttention()
    {
        if (attentionRefreshing || VM.Session?.Complete != true || VM.Api.ActiveWrites > 0)
            return;
        attentionRefreshing = true;
        try
        {
            await VM.RefreshAttention();
        }
        catch (Exception)
        { /* Next refresh or account page load rechecks; never publish a stale count. */
        }
        finally
        {
            attentionRefreshing = false;
        }
    }

    void UpdateBadges()
    {
        foreach (var item in NavItems)
        {
            int count = item.Tag?.ToString() switch
            {
                "accounts" => VM.PendingRequests + VM.UnconfiguredAccounts,
                "requests" => VM.PendingRequests,
                _ => 0
            };
            if (count == 0)
                item.InfoBadge = null;
            else if (item.InfoBadge?.Value != count)
                item.InfoBadge = new InfoBadge
                {
                    Value = count
                };
            if (item.Tag?.ToString()is "accounts" or "requests")
                ToolTipService.SetToolTip(item, $"待审批 {VM.PendingRequests} · 待配置账户 {VM.UnconfiguredAccounts}");
        }
    }

    void UpdateShell()
    {
        var s = VM.Session;
        AccountText.Text = s == null ? "尚未登录" : s.Name;
        AccountScope.Text = s == null ? (VM.Configuration == null ? "请先连接后端" : "请完成安全登录") : $"{Labels.Of(s.Role)} · {(s.CountryScope == "selected" ? string.Join("、", s.Countries) : "全部国家/地区")}";
        AccountPicture.Initials = s == null ? "" : s.Name[..1];
        ToolTipService.SetToolTip(AccountCard, s == null ? null : $"{s.Name}（{s.Id}）\n{AccountScope.Text}");
        LogoutButton.Visibility = s == null ? Visibility.Collapsed : Visibility.Visible;
        foreach (var item in NavItems)
        {
            var key = (string)item.Tag;
            bool allowed = s?.Complete == true && key switch
            {
                "jobs" => s.CanManageDataJobs,
                "accounts" => s.Role == "admin",
                "requests" => s.Role == "finance",
                "audit" => s.Role != "warehouse",
                _ => true
            };
            item.Visibility = allowed ? Visibility.Visible : Visibility.Collapsed;
        }

        // A group header is shown only when at least one page under it is available.
        NavigationViewItemHeader? header = null;
        bool any = false;
        foreach (var entry in Nav.MenuItems)
        {
            if (entry is NavigationViewItemHeader h)
            {
                if (header != null)
                    header.Visibility = any ? Visibility.Visible : Visibility.Collapsed;
                header = h;
                any = false;
            }
            else if (entry is NavigationViewItem { Visibility: Visibility.Visible })
                any = true;
        }

        if (header != null)
            header.Visibility = any ? Visibility.Visible : Visibility.Collapsed;
        AppTitleBar.Subtitle = VM.Configuration == null ? "选择后端" : VM.Settings.Address;
    }

    // Reflect the page on screen in the pane without re-running navigation.
    void Select(string? key)
    {
        syncing = true;
        try
        {
            Nav.SelectedItem = key == "settings" ? Nav.SettingsItem : NavItems.FirstOrDefault(x => (string)x.Tag == key && x.Visibility == Visibility.Visible);
        }
        finally
        {
            syncing = false;
        }
    }

    async void NavigationChanged(NavigationView sender, NavigationViewSelectionChangedEventArgs args)
    {
        if (!ready || syncing)
            return;
        if (VM.Api?.ActiveWrites > 0)
        {
            VM.Message("写操作尚未完成，请等待结果后切换页面。");
            Select(PageHost.Content is SettingsView ? "settings" : page);
            return;
        }

        VM.NewScope();
        if (args.IsSettingsSelected)
        {
            ShowSettings();
            return;
        }

        page = args.SelectedItemContainer?.Tag?.ToString() ?? "cases";
        ShowPage();
        await Task.CompletedTask;
    }

    public void ShowScreen(string key)
    {
        VM.NewScope();
        page = key;
        ShowPage();
    }

    public void ShowSettings()
    {
        PageHost.Content = new SettingsView(VM);
        Select("settings");
    }

    public void ShowPage()
    {
        if (VM.Api == null)
        {
            ShowSettings();
            return;
        }

        if (VM.Session?.Complete != true)
        {
            PageHost.Content = new AuthView(VM);
            Select(null);
            return;
        }

        // A page the new account may not use (e.g. after switching accounts) falls back to cases.
        if (NavItems.FirstOrDefault(x => (string)x.Tag == page)is { Visibility: Visibility.Collapsed })
            page = "cases";
        PageHost.Content = page switch
        {
            "security" => new AuthView(VM, true),
            "accounts" or "requests" => new AccountsView(VM, page),
            "review" => new ReviewView(VM),
            "jobs" => new JobsView(VM),
            _ => new WorkspaceView(VM, page)};
        Select(page);
    }

    public void OpenCase(string id)
    {
        if (VM.Api.ActiveWrites > 0)
        {
            VM.Message("写操作尚未完成，请等待结果后打开详情。");
            return;
        }

        VM.NewScope();
        PageHost.Content = new CaseView(VM, id, () =>
        {
            VM.NewScope();
            ShowPage();
        });
    }

    async void LogoutClick(object sender, RoutedEventArgs e) => await VM.Guard(VM.Logout);
}

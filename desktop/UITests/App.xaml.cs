using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Reconciliation.Windows;
using Reconciliation.Windows.Services;
using Reconciliation.Windows.ViewModels;
using System.Text.Json.Nodes;

namespace Reconciliation.UITests;
// Separate test executable: the production client contains no fixture login mode.
public partial class TestApp : Application
{
    TestServer? server;
    MainWindow? window;
    bool initialized;
    static string Evidence => Path.Combine(Path.GetTempPath(), "returns-recon-ui-test.log");

    public TestApp()
    {
        InitializeComponent();
        UnhandledException += (_, e) =>
        {
            File.AppendAllText(Evidence, e.Exception.ToString() + "\n");
            e.Handled = true;
            Exit();
        };
    }

    static IEnumerable<T> Find<T>(DependencyObject root)
        where T : DependencyObject
    {
        if (root is T item)
            yield return item;
        for (int i = 0; i < Microsoft.UI.Xaml.Media.VisualTreeHelper.GetChildrenCount(root); i++)
            foreach (var node in Find<T>(Microsoft.UI.Xaml.Media.VisualTreeHelper.GetChild(root, i)))
                yield return node;
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        File.WriteAllText(Evidence, "Isolated synthetic UI host. Not official data or a real authentication device test.\n");
        var native = typeof(NativeWebAuthn);
        foreach (var pair in new[]
        {
            ("MakeOptions", 72, 64),
            ("GetOptions", 80, 72)
        }

        )
        {
            var type = native.GetNestedType(pair.Item1, System.Reflection.BindingFlags.NonPublic)!;
            if (System.Runtime.InteropServices.Marshal.SizeOf(type) != pair.Item2 || System.Runtime.InteropServices.Marshal.OffsetOf(type, "Cancellation").ToInt32() != pair.Item3)
                throw new Exception("Native WebAuthn ABI mismatch");
        }

        if ((uint)native.GetField("MakeOptionsVersion", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static)!.GetRawConstantValue()! != 2 || (uint)native.GetField("GetOptionsVersion", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static)!.GetRawConstantValue()! != 3)
            throw new Exception("Native WebAuthn option version mismatch");
        File.AppendAllText(Evidence, "PASS x64 MakeCredential/GetAssertion layout, cancellation offset and SDK option versions.\n");
        server = new TestServer(Fixture.Reply);
        var vm = new AppViewModel(new ClientSettings(server.Address), _ =>
        {
        });
        window = new MainWindow(vm)
        {
            Title = "隔离合成数据 · WinUI 界面验证"
        };
        typeof(Reconciliation.Windows.App).GetProperty("Window")!.SetValue(null, window);
        var root = (FrameworkElement)window.Content;
        root.Loaded += async (_, _) =>
        {
            if (initialized)
                return;
            initialized = true;
            await Task.Yield();
            var first = Environment.GetCommandLineArgs().Skip(1).FirstOrDefault();
            await vm.Guard(async () =>
            {
                await vm.Connect(server.Address, "Default");
                if (first != "login")
                    await vm.Authenticate("auth/login", new { id = "ui-fixture", password = "Synthetic fixture only, not a real account" });
            });
            // Arguments: [screen] [theme] [png] [logical-width]. Screens use only the isolated fixture server.
            var cli = Environment.GetCommandLineArgs().Skip(1).ToArray();
            var screen = cli.ElementAtOrDefault(0);
            await Task.Delay(400);
            if (int.TryParse(cli.ElementAtOrDefault(3), out var width))
                window.AppWindow.Resize(new global::Windows.Graphics.SizeInt32((int)(width * root.XamlRoot.RasterizationScale), (int)(820 * root.XamlRoot.RasterizationScale)));
            await Task.Delay(200);
            if (screen == "review-assign")
            {
                window.ShowScreen("review");
                await Task.Delay(500);
                var operation = Find<ComboBox>(root).First(x => x.Header?.ToString() == "批量操作");
                operation.SelectedItem = operation.Items.OfType<Reconciliation.Windows.Views.Choice>().First(x => x.ID == "assign");
            }
            else if (screen == "case-assignment")
            {
                Fixture.PendingCase = true;
                window.OpenCase("case-ui");
                await Task.Delay(500);
                var picker = Find<Reconciliation.Windows.Views.AccountPicker>(root).Single();
                picker.Text = "uk-only";
                bool blocked = false;
                try
                {
                    await picker.ValidateSelection();
                }
                catch (ArgumentException)
                {
                    blocked = true;
                }

                if (!blocked)
                    throw new Exception("Single-case incompatible typed assignee accepted");
                picker.Text = "ui-fixture";
                await picker.ValidateSelection();
                var dialog = picker.CreateDialog();
                _ = dialog.ShowAsync();
                await Task.Delay(400);
                if (Find<ListView>(dialog).Single().Items.OfType<Reconciliation.Windows.Views.AccountPicker.AccountOption>().Any(x => x.Id == "uk-only"))
                    throw new Exception("Single-case incompatible account visible");
                File.AppendAllText(Evidence, "PASS single-case effective country hides UK-only account and rejects typed bypass.\n");
            }
            else if (screen == "batch-scope")
            {
                Fixture.MultiCountry = true;
                window.ShowScreen("review");
                await Task.Delay(500);
                var view = (Reconciliation.Windows.Views.ReviewView)Find<ContentControl>(root).First(x => x.Name == "PageHost").Content;
                var flags = System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.NonPublic;
                var op = Find<ComboBox>(view).First(x => x.Header?.ToString() == "批量操作");
                op.SelectedItem = op.Items.OfType<Reconciliation.Windows.Views.Choice>().First(x => x.ID == "assign");
                Find<CheckBox>(view).First(x => x.Tag is string).IsChecked = true;
                view.GetType().GetField("page", flags)!.SetValue(view, 2);
                await (Task)view.GetType().GetMethod("Load", flags)!.Invoke(view, null)!;
                await Task.Delay(100);
                var last = Find<CheckBox>(view).First(x => x.Tag is string);
                last.IsChecked = true;
                var picker = (Reconciliation.Windows.Views.AccountPicker)view.GetType().GetField("assignee", flags)!.GetValue(view)!;
                picker.Text = "uk-only";
                bool blocked = false;
                try
                {
                    await (Task)view.GetType().GetMethod("Preview", flags)!.Invoke(view, new object[] { false })!;
                }
                catch (ArgumentException)
                {
                    blocked = true;
                }

                if (!blocked || server.Requests.Any(x => x.Target == "/api/case-batches/preview"))
                    throw new Exception("Cross-page invalid assignee reached preview");
                var allPages = Find<CheckBox>(view).First(x => x.Content?.ToString() == "选择当前筛选的全部页");
                allPages.IsChecked = true;
                last.IsChecked = false;
                await picker.ValidateSelection(); // Remaining fifty UK rows are covered.
                last.IsChecked = true;
                blocked = false;
                try
                {
                    await picker.ValidateSelection();
                }
                catch (ArgumentException)
                {
                    blocked = true;
                }

                if (!blocked)
                    throw new Exception("All-page mixed countries accepted UK-only account");
                picker.Text = "ui-fixture";
                await picker.ValidateSelection();
                var dialog = picker.CreateDialog();
                _ = dialog.ShowAsync();
                await Task.Delay(400);
                if (Find<ListView>(dialog).Single().Items.OfType<Reconciliation.Windows.Views.AccountPicker.AccountOption>().Any(x => x.Id == "uk-only"))
                    throw new Exception("Batch incompatible account visible");
                File.AppendAllText(Evidence, "PASS cross-page, all-page and excluded country selection; typed bypass blocked before preview POST.\n");
            }
            else if (screen == "account-tabs")
            {
                window.ShowScreen("accounts");
                await Task.Delay(500);
                var selector = Find<SelectorBar>(root).Single();
                if (selector.Items.Count != 4 || Find<InfoBadge>(selector).Count(x => x.Visibility == Visibility.Visible && x.Value == 1) != 2)
                    throw new Exception("Native account tabs/badges");
                foreach (string key in new[]
                {
                    "requests",
                    "origins",
                    "audit",
                    "directory"
                }

                )
                {
                    selector.SelectedItem = selector.Items.First(x => x.Tag?.ToString() == key);
                    await Task.Delay(300);
                    if (selector.Items.Count(x => x.IsSelected) != 1 || selector.SelectedItem.Tag?.ToString() != key || vm.Error != "")
                        throw new Exception("Account tab navigation: " + key);
                }

                File.AppendAllText(Evidence, "PASS official SelectorBar switches all four tabs and retains both numeric badges.\n");
            }
            else if (screen == "country-picker")
            {
                var names = new[]
                {
                    "United Arab Emirates",
                    "South Georgia and the South Sandwich Islands",
                    "United Kingdom",
                    "France"
                };
                const string retained = "Retained region outside current source list";
                var(picker, selected) = Reconciliation.Windows.Views.UI.CountryPicker(names, new[] { retained });
                var dialog = Reconciliation.Windows.Views.UI.Dialog("负责国家/地区 · 长名称布局验证", picker);
                dialog.CloseButtonText = "关闭";
                _ = dialog.ShowAsync();
                await Task.Delay(400);
                var boxes = Find<CheckBox>(dialog).Where(x => x.Tag is string).ToArray();
                boxes.First(x => x.Tag?.ToString() == names[1]).IsChecked = true;
                if (!selected().Order().SequenceEqual(new[] { retained, names[1] }.Order()))
                    throw new Exception("Country source values changed");
                var longBox = boxes.First(x => x.Tag?.ToString() == names[1]);
                var label = (TextBlock)longBox.Content;
                var p = label.TransformToVisual(longBox).TransformPoint(new global::Windows.Foundation.Point());
                if (label.ActualHeight < label.FontSize * 2 || p.X + label.ActualWidth > longBox.ActualWidth + 1 || label.IsTextTrimmed)
                    throw new Exception("Long country label did not wrap within checkbox");
                File.AppendAllText(Evidence, $"PASS long country/region wraps {label.ActualWidth:F2}x{label.ActualHeight:F2}; exact source and retained selection preserved.\n");
            }
            else if (screen == "case")
                window.OpenCase("case-ui");
            else if (screen == "settings")
                window.ShowSettings();
            else if (screen is "dialog" or "confirm" or "dialog-resize" or "account-picker" or "edit-account" or "batch-results" or "filters")
            {
                window.ShowScreen(screen == "edit-account" ? "accounts" : screen == "batch-results" ? "review" : "transactions");
                await Task.Delay(500);
                var page = Find<ContentControl>(root).First(x => x.Name == "PageHost").Content;
                if (screen == "dialog")
                    _ = Reconciliation.Windows.Views.UI.Show("记录详情 · 隔离合成数据", Reconciliation.Windows.Views.UI.Details(Fixture.Row));
                if (screen == "confirm")
                    _ = Reconciliation.Windows.Views.UI.Confirm("确认操作 · 隔离布局验证", Reconciliation.Windows.Views.UI.Text("仅显示短确认弹窗，不执行任何业务写入。"));
                if (screen == "dialog-resize")
                {
                    var dialog = Reconciliation.Windows.Views.UI.Dialog("记录详情 · 窗口缩窄验证", Reconciliation.Windows.Views.UI.Details(Fixture.Row));
                    dialog.CloseButtonText = "关闭";
                    _ = dialog.ShowAsync();
                    await Task.Delay(300);
                    window.AppWindow.Resize(new global::Windows.Graphics.SizeInt32((int)(640 * root.XamlRoot.RasterizationScale), (int)(820 * root.XamlRoot.RasterizationScale)));
                    await Task.Delay(300);
                    var panel = Find<Border>(dialog).First(x => x.Name == "BackgroundElement");
                    if (panel.ActualWidth > root.ActualWidth - 47)
                        throw new Exception("Open dialog did not follow narrower window");
                    File.AppendAllText(Evidence, "PASS open dialog follows window resize.\n");
                }

                if (screen == "account-picker")
                {
                    var picker = new Reconciliation.Windows.Views.AccountPicker(vm, "测试账户")
                    {
                        Text = "ui-fixture"
                    };
                    var dialog = picker.CreateDialog();
                    var showing = dialog.ShowAsync();
                    await Task.Delay(600);
                    var search = Find<TextBox>(dialog).First(x => x.Header?.ToString() == "搜索账户名、显示名或国家/地区");
                    var role = Find<ComboBox>(dialog).First(x => x.Header?.ToString() == "角色");
                    var list = Find<ListView>(dialog).Single();
                    search.Text = "warehouse-pending";
                    await Task.Delay(100);
                    if (list.Items.Count != 1)
                        throw new Exception("Account picker search");
                    role.SelectedItem = role.Items.OfType<Reconciliation.Windows.Views.Choice>().First(x => x.ID == "finance");
                    await Task.Delay(100);
                    if (list.Items.Count != 0)
                        throw new Exception("Account picker role filter");
                    role.SelectedItem = role.Items.OfType<Reconciliation.Windows.Views.Choice>().First(x => x.ID == "warehouse");
                    await Task.Delay(100);
                    if (list.Items.Count != 1)
                        throw new Exception("Account picker combined filter");
                    list.SelectedIndex = 0;
                    if (!dialog.IsPrimaryButtonEnabled)
                        throw new Exception("Account picker selection");
                    dialog.Hide();
                    await showing;
                    if (picker.Text != "ui-fixture")
                        throw new Exception("Cancelled picker changed account");
                    File.AppendAllText(Evidence, "PASS account picker search, role filter, selection and cancel preservation.\n");
                    _ = picker.CreateDialog().ShowAsync();
                }

                if (screen == "edit-account")
                {
                    _ = page!.GetType().GetMethod("Edit", System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.NonPublic)!.Invoke(page, new object? [] { Fixture.Profile });
                    await Task.Delay(300);
                    var dialog = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetOpenPopupsForXamlRoot(root.XamlRoot).SelectMany(x => Find<ContentDialog>(x.Child)).Single();
                    var scope = Find<ComboBox>(dialog).First(x => x.Header?.ToString() == "国家/地区范围");
                    scope.SelectedItem = scope.Items.OfType<Reconciliation.Windows.Views.Choice>().First(x => x.ID == "selected");
                }

                if (screen == "batch-results")
                {
                    page!.GetType().GetField("batch", System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.NonPublic)!.SetValue(page, Fixture.Batch);
                    _ = page.GetType().GetMethod("ShowBatch", System.Reflection.BindingFlags.Instance | System.Reflection.BindingFlags.NonPublic)!.Invoke(page, null);
                }

                if (screen == "filters")
                {
                    var filter = Find<Expander>(root).First(x => x.Header?.ToString()?.StartsWith("筛选条件") == true);
                    filter.IsExpanded = true;
                    await Task.Delay(300);
                    for (var parent = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetParent(filter); parent != null; parent = Microsoft.UI.Xaml.Media.VisualTreeHelper.GetParent(parent))
                        if (parent is ScrollViewer scroll)
                        {
                            scroll.ChangeView(null, scroll.ScrollableHeight, null, true);
                            break;
                        }

                    if (!Find<TextBlock>(filter).Any(x => x.Text == "查询") || !Find<TextBlock>(filter).Any(x => x.Text == "重置筛选"))
                        throw new Exception("Query/reset outside filter");
                }
            }
            else if (!string.IsNullOrEmpty(screen) && screen != "login")
                window.ShowScreen(screen);
            window.ApplyTheme(cli.ElementAtOrDefault(1)is { Length: > 0 } theme ? theme : "Dark");
            if (vm.Session?.Complete == true)
                await vm.RefreshAttention();
            File.AppendAllText(Evidence, "Loaded " + (screen ?? "cases") + "; " + vm.Error + "\n");
            if (cli.ElementAtOrDefault(2)is { Length: > 0 } shot)
            {
                await Task.Delay(2500);
                await Snapshot.Save(root, shot);
                foreach (var dialog in Microsoft.UI.Xaml.Media.VisualTreeHelper.GetOpenPopupsForXamlRoot(root.XamlRoot).SelectMany(x => Find<ContentDialog>(x.Child)).Distinct())
                {
                    var panel = Find<Border>(dialog).First(x => x.Name == "BackgroundElement");
                    var origin = panel.TransformToVisual(root).TransformPoint(new global::Windows.Foundation.Point());
                    double dx = origin.X + panel.ActualWidth / 2 - root.ActualWidth / 2, dy = origin.Y + panel.ActualHeight / 2 - root.ActualHeight / 2;
                    File.AppendAllText(Evidence, $"Dialog center offset=({dx:F3},{dy:F3}); bounds=({origin.X:F3},{origin.Y:F3},{panel.ActualWidth:F3},{panel.ActualHeight:F3}); viewport={root.ActualWidth:F3}x{root.ActualHeight:F3}\n");
                    if (Math.Abs(dx) > 2 || Math.Abs(dy) > 2 || origin.X < -1 || origin.Y < -1 || origin.X + panel.ActualWidth > root.ActualWidth + 1 || origin.Y + panel.ActualHeight > root.ActualHeight + 1)
                        throw new Exception("Modal panel is not centered within the client area");
                }

                if (screen == "review-assign")
                    foreach (var picker in Find<Reconciliation.Windows.Views.AccountPicker>(root).Where(x => x.Visibility == Visibility.Visible))
                    {
                        File.AppendAllText(Evidence, $"Picker actual={picker.ActualWidth}x{picker.ActualHeight}; desired={picker.DesiredSize}\n");
                        if (picker.ActualWidth < 100 || picker.ActualHeight < 20)
                            throw new Exception("Visible account picker has no layout");
                    }

                foreach (var layer in new UIElement[]
                {
                    root
                }.Concat(Microsoft.UI.Xaml.Media.VisualTreeHelper.GetOpenPopupsForXamlRoot(root.XamlRoot).Select(x => x.Child)))
                    foreach (var panel in Find<Panel>(layer).Where(x => x is Reconciliation.Windows.Views.AdaptiveColumns or Reconciliation.Windows.Views.FlowPanel))
                        foreach (var child in panel.Children.OfType<FrameworkElement>().Where(x => x.Visibility == Visibility.Visible && x.ActualWidth > 0))
                        {
                            var point = child.TransformToVisual(panel).TransformPoint(new global::Windows.Foundation.Point());
                            if (point.X < -1 || point.X + child.ActualWidth > panel.ActualWidth + 1)
                                throw new Exception("Responsive child overflows: " + child.GetType().Name);
                        }

                if (vm.Session?.Complete == true && (vm.PendingRequests != 1 || vm.UnconfiguredAccounts != 1))
                    throw new Exception("Badge fixture counts");
                if (vm.Session?.Complete == true && Find<NavigationViewItem>(root).First(x => x.Tag?.ToString() == "accounts").InfoBadge?.Value != 2)
                    throw new Exception("Sidebar badge did not update");
                File.AppendAllText(Evidence, "PASS responsive panel bounds and pending/setup counts; logical width " + root.ActualWidth + "\n");
                Exit();
            }
        };
        // Snapshot runs render off-screen so they do not flash over the user's desktop.
        if (Environment.GetCommandLineArgs().Length > 3)
            window.AppWindow.Move(new global::Windows.Graphics.PointInt32(-30000, -30000));
        window.Activate();
    }
}

// Screen capture of this composition window is unreliable in automation sessions, so render in-process.
static class Snapshot
{
    public static async Task Save(FrameworkElement root, string path)
    {
        var layers = new List<UIElement>
        {
            root
        };
        layers.AddRange(Microsoft.UI.Xaml.Media.VisualTreeHelper.GetOpenPopupsForXamlRoot(root.XamlRoot).Select(p => p.Child).Where(c => c != null));
        for (int i = 0; i < layers.Count; i++)
        {
            var target = new Microsoft.UI.Xaml.Media.Imaging.RenderTargetBitmap();
            await target.RenderAsync(layers[i]);
            var pixels = System.Runtime.InteropServices.WindowsRuntime.WindowsRuntimeBufferExtensions.ToArray(await target.GetPixelsAsync());
            using var stream = File.Create(i == 0 ? path : Path.ChangeExtension(path, null) + "-popup" + i + ".png");
            var encoder = await global::Windows.Graphics.Imaging.BitmapEncoder.CreateAsync(global::Windows.Graphics.Imaging.BitmapEncoder.PngEncoderId, stream.AsRandomAccessStream());
            var dpi = 96 * root.XamlRoot.RasterizationScale;
            encoder.SetPixelData(global::Windows.Graphics.Imaging.BitmapPixelFormat.Bgra8, global::Windows.Graphics.Imaging.BitmapAlphaMode.Premultiplied, (uint)target.PixelWidth, (uint)target.PixelHeight, dpi, dpi, pixels);
            await encoder.FlushAsync();
        }
    }
}

static class Fixture
{
    public static bool PendingCase, MultiCountry;
    public static JsonNode Row => row;
    public static JsonNode Profile => Json.Node(profile);

    static readonly object pendingProfile = new
    {
        id = "warehouse-pending",
        role = "warehouse",
        display_name = "隔离待配置仓库账户 · 名称用于测试换行",
        country_scope = "selected",
        countries = new[]
        {
            "United Kingdom",
            "France",
            "Germany"
        },
        state = "new"
    };
    public static JsonNode Batch => Json.Node(new { id = "batch-ui", status = "completed", action = "confirm", total = 3, ready = 0, succeeded = 3, skipped = 0, failed = 0, view = "succeeded", view_total = 3, items = new[] { new { cancel_id = "0:4", invoice = "ISOLATED-RETURN", status = "succeeded", message = "隔离布局测试，不是实际提交" } }, reasons = Array.Empty<object>() });

    static readonly JsonNode row = Json.Node(new { id = "0:2", invoice = "ISOLATED-SALE", stock = "A", customer = "0042", country = "UK", price = "0.100000000000000001", quantity = 10, time = "2010-01-01T08:00:00.123456", sheet = "isolated synthetic", source_row = 2, kind = "sale", current_remaining = 10, raw_json = "[\"synthetic only\"]" });
    static readonly JsonNode cancel = Json.Node(new { id = "0:4", invoice = "ISOLATED-RETURN", stock = "A", customer = "0042", country = "UK", price = "0.100000000000000001", quantity = -4, time = "2010-01-02T08:00:00.123456", sheet = "isolated synthetic", source_row = 4, kind = "cancel" });
    static readonly JsonNode item = Json.Node(new { id = "case-ui", run_id = "run-ui", cancel_id = "0:4", reason = "ambiguous_candidates", status = "in_progress", version = 2, assignee = "ui-fixture", conclusion = "", source_row = 4, sheet = "isolated synthetic", generation = 1, is_current = true });
    static readonly object profile = new
    {
        id = "ui-fixture",
        role = "admin",
        display_name = "隔离界面验证账户",
        country_scope = "all",
        countries = Array.Empty<string>(),
        permissions_version = 1,
        state = "complete"
    };
    static readonly object run = new
    {
        run_id = "run-ui",
        dataset_id = "dataset-ui",
        scope = "all",
        cleaning_version = "synthetic-only",
        rule_version = "synthetic-only"
    };
    static object Page(IEnumerable<object> values) => new
    {
        items = values.ToArray(),
        total = values.Count(),
        page = 1,
        page_size = 50
    };
    public static Task<global::Reply> Reply(Request req)
    {
        var path = req.Target.Split('?')[0];
        object result = path switch
        {
            "/api/health" => new
            {
                application = "returns-recon",
                runtime_id = "isolated-ui-only"
            },
            "/api/auth/client-config" => new
            {
                api_version = "1.0.0",
                backend_id = "isolated-ui-only-not-a-business-instance",
                native_passkey = new
                {
                    id = "default",
                    rp_id = "localhost",
                    origin = "http://localhost:8080"
                },
                legacy_bindings = Array.Empty<object>()
            },
            "/api/auth/login" => new
            {
                session = new
                {
                    id = "ui-fixture",
                    role = "admin",
                    display_name = "隔离界面验证账户",
                    country_scope = "all",
                    countries = Array.Empty<string>(),
                    state = "complete",
                    scope = "business",
                    has_totp = true,
                    has_passkey = true,
                    csrf = "synthetic-not-a-session",
                    can_manage_data_jobs = true
                }
            },
            "/api/runs" => new[]
            {
                run
            },
            "/api/countries" => new[]
            {
                "UK",
                "France"
            },
            "/api/auth/setup/accounts" => new[]
            {
                new
                {
                    id = "isolated-new",
                    display_name = "隔离未配置账户",
                    role = "finance"
                }
            },
            "/api/users" => new[]
            {
                profile,
                pendingProfile,
                Json.Node(new { id = "uk-only", display_name = "仅英国仓库", role = "warehouse", country_scope = "selected", countries = new[] { "United Kingdom" } })
            },
            "/api/admin/accounts" => new[]
            {
                profile,
                pendingProfile
            },
            "/api/warehouse-requests" => new[]
            {
                new
                {
                    id = "request-ui",
                    status = "pending",
                    account_id = "pending-ui"
                }
            },
            "/api/overview" => new
            {
                cases = new
                {
                    total = 1,
                    pending = 0,
                    in_progress = 1,
                    closed = 0,
                    waiting = 0
                },
                currently_unallocated = 1,
                supplemental_sales = 0,
                reviewed_allocations = 0,
                caveat = "隔离合成界面数据；不是官方业务结果。",
                initial_reconciliation = new
                {
                    input = new
                    {
                        raw = 2,
                        accepted = 2,
                        excluded = 0
                    },
                    matched = 0,
                    suspected = 1,
                    no_original = 0
                }
            },
            "/api/transactions" => Page(new object[] { row, cancel }),
            "/api/cases" => Page(new object[] { item }),
            "/api/allocations" => Page(new object[] { new { cancel_id = "0:4", sale_id = "0:2", quantity = 4, amount = "0.400000000000000004", method = "manual", actor = "ui-fixture" } }),
            "/api/cases/case-ui/evidence" => new
            {
                @case = PendingCase ? PendingItem() : item,
                cancel = PendingCase ? FranceCancel() : cancel,
                original_cancel = cancel,
                effective_cancel = PendingCase ? FranceCancel() : cancel,
                decision_evidence = Array.Empty<object>(),
                candidates = Page(new object[] { row }),
                related_cases = Array.Empty<object>(),
                duplicate_rows = Array.Empty<object>(),
                remediations = Array.Empty<object>(),
                caveat = "隔离合成数据"
            },
            "/api/review-sets" => new
            {
                id = "set-ui",
                run_id = "run-ui",
                counts = new
                {
                    total = 1,
                    recommended = 1
                }
            },
            "/api/review-sets/set-ui" when MultiCountry => ReviewPage(req.Target),
            "/api/review-sets/set-ui" => Page(new object[] { new { @case = item, cancel, options = new[] { new { id = "option-ui", kind = "nearest", sale_id = "0:2", invoice = "ISOLATED-SALE", remaining = 10 } }, choice_id = "option-ui", has_allocation = false } }),
            "/api/review-sets/set-ui/groups" => Page(new object[] { new { key = "ISOLATED-RETURN", label = "ISOLATED-RETURN", count = 1, recommended = 1 } }),
            "/api/case-batches/latest" => new
            {
                id = "",
                items = Array.Empty<object>()
            },
            "/api/admin/passkey-domains" => new
            {
                base_origin = "http://localhost:8080",
                origins = new[]
                {
                    "http://192.0.2.1:8080",
                    "https://example.test"
                },
                version = 1
            },
            _ => Array.Empty<object>()};
        return Task.FromResult(new global::Reply(Body: Json.Node(result).ToJsonString()));
    }

    static JsonNode PendingItem()
    {
        var copy = item.DeepClone();
        copy["status"] = "pending";
        copy["assignee"] = "";
        return copy;
    }

    static JsonNode FranceCancel()
    {
        var copy = cancel.DeepClone();
        copy["country"] = "France";
        return copy;
    }

    static object ReviewPage(string target)
    {
        var q = target.Split('?', 2)[1].Split('&').Select(x => x.Split('=', 2)).ToDictionary(x => Uri.UnescapeDataString(x[0]), x => Uri.UnescapeDataString(x[1]));
        var rows = Enumerable.Range(0, 51).Select(i =>
        {
            var c = PendingItem();
            c["id"] = "batch-case-" + i;
            var source = cancel.DeepClone();
            source["country"] = i == 50 ? "France" : "United Kingdom";
            return new
            {
                @case = c,
                cancel = source,
                options = Array.Empty<object>(),
                choice_id = "",
                has_allocation = false
            };
        });
        if (q.TryGetValue("country", out var country))
            rows = rows.Where(x => x.cancel.S("country") == country);
        if (q.GetValueOrDefault("sort") == "country")
            rows = rows.OrderBy(x => x.cancel.S("country"), StringComparer.Ordinal);
        var values = rows.ToArray();
        int page = int.Parse(q.GetValueOrDefault("page", "1")), size = int.Parse(q.GetValueOrDefault("page_size", "50"));
        return new
        {
            items = values.Skip((page - 1) * size).Take(size).ToArray(),
            total = values.Length,
            page,
            page_size = size
        };
    }
}

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;
using Reconciliation.Windows.Services;

namespace Reconciliation.Windows.Views;
public sealed class ReviewView : UserControl
{
    readonly AppViewModel vm;
    readonly StackPanel header = UI.Stack();
    readonly ContentControl table = UI.Host();
    readonly TextBlock status = UI.Muted("", true);
    readonly TextBlock selection = UI.Text("");
    readonly TextBlock groupStatus = UI.Muted("", true);
    readonly HashSet<string> selected = [];
    readonly HashSet<string> excluded = [];
    readonly Dictionary<string, string> selectedCountries = [], excludedCountries = [];
    readonly Dictionary<string, string> choices = [];
    readonly CheckBox all = new()
    {
        Content = "选择当前筛选的全部页"
    };
    readonly ComboBox recommended, groupBy, group, operation, runs;
    readonly Dictionary<string, TextBox> filters = [];
    readonly Dictionary<string, ComboBox> filterOptions = [];
    readonly AccountPicker? assignee;
    readonly AccountPicker personFilter;
    readonly TextBox conclusion = UI.Field("共同核查结论");
    readonly Pager pager;
    string setID = "";
    int page = 1, total, groupPage = 1, groupTotal;
    JsonNode? batch;
    long request, groupRequest, generateRequest;
    bool selecting;
    public ReviewView(AppViewModel model)
    {
        vm = model;
        runs = UI.Select("数据运行", vm.Runs.Select(x => new Choice(x.RunId, x.Label)), vm.RunID);
        recommended = UI.Select("复核范围", UI.Choices(("true", "有推荐的项目"), ("false", "全部可处理项目")));
        groupBy = UI.Select("分组依据", UI.Choices(("invoice", "退货单"), ("customer", "客户"), ("reason", "原因"), ("stock", "商品")));
        group = UI.Select("当前组", [new Choice("", "全部组")]);
        operation = UI.Select("批量操作", UI.Choices(("confirm", "确认配对"), ("approve", "确认并结案"), ("assign", "派单"), ("start", "开始"), ("close", "关闭")));
        operation.MinWidth = 160;
        if (vm.Session!.Role == "warehouse")
            operation.Items.RemoveAt(2);
        else
            assignee = UI.AssigneePicker(vm, countries: RequiredCountries);
        conclusion.MinWidth = 0;
        personFilter = UI.AssigneePicker(vm, "当前责任人");
        foreach (var(k, label)in new[]
        {
            ("customer", "客户"),
            ("country", "国家"),
            ("stock", "商品"),
            ("invoice", "退货单"),
            ("date_from", "开始日期 yyyy-MM-dd"),
            ("date_to", "结束日期 yyyy-MM-dd")
        }

        )
        {
            var box = UI.Field(label);
            box.KeyDown += async (_, e) =>
            {
                if (e.Key == global::Windows.System.VirtualKey.Enter)
                {
                    e.Handled = true;
                    await vm.Guard(Apply);
                }
            };
            filters[k] = box;
        }

        filterOptions["reason"] = UI.Select("差异原因", new[] { new Choice("", "全部原因") }.Concat(Labels.CaseReasons.Select(x => new Choice(x, Labels.Of(x)))));
        filterOptions["status"] = UI.Select("差异状态", UI.Choices(("", "全部"), ("pending", "待处理"), ("in_progress", "处理中"), ("closed", "已关闭")));
        filterOptions["waiting"] = UI.Select("待补充", UI.Choices(("", "全部"), ("true", "待补充"), ("false", "非待补充")));
        filterOptions["classification"] = UI.Select("初始分类", UI.Choices(("", "全部"), ("matched", "已配对"), ("suspected", "疑似"), ("no_original", "无合格原单")));
        runs.SelectionChanged += async (_, _) => await vm.Guard(async () =>
        {
            vm.RunID = runs.Value();
            Clear();
            await Generate();
        });
        recommended.SelectionChanged += async (_, _) => await vm.Guard(async () =>
        {
            Clear();
            await LoadGroups();
            await Load();
        });
        groupBy.SelectionChanged += async (_, _) => await vm.Guard(async () =>
        {
            Clear();
            groupPage = 1;
            await LoadGroups();
            await Load();
        });
        group.SelectionChanged += async (_, _) =>
        {
            if (selecting)
                return;
            Clear();
            await vm.Guard(Load);
        };
        all.Checked += (_, _) =>
        {
            selected.Clear();
            excluded.Clear();
            selectedCountries.Clear();
            excludedCountries.Clear();
            ShowSelection();
            SyncChecks();
        };
        all.Unchecked += (_, _) =>
        {
            selected.Clear();
            excluded.Clear();
            selectedCountries.Clear();
            excludedCountries.Clear();
            ShowSelection();
            SyncChecks();
        };
        operation.SelectionChanged += (_, _) => ShowOperationInputs();
        header.Children.Add(UI.PageHeader("批量复核", "推荐须人工复核后提交；整单和共享余量组内原子处理，冲突组跳过，其余组继续。", UI.Button(vm, "重新生成建议", Generate, Glyph.Refresh), UI.Button(vm, "最近批量结果", async () =>
        {
            batch = await vm.Api.Send("case-batches/latest?run_id=" + Uri.EscapeDataString(vm.RunID), ct: vm.Token);
            await ShowBatch();
        }, Glyph.History)));
        groupStatus.VerticalAlignment = VerticalAlignment.Center;
        var groupPager = UI.Flow(UI.Button(vm, "上一页组", async () =>
        {
            if (groupPage > 1)
            {
                groupPage--;
                await LoadGroups();
            }
        }, Glyph.Prev), groupStatus, UI.Button(vm, "下一页组", async () =>
        {
            if (groupPage * 50 < groupTotal)
            {
                groupPage++;
                await LoadGroups();
            }
        }, Glyph.Next));
        groupPager.Spacing = 12;
        header.Children.Add(UI.Card(UI.Stack(UI.Columns(4, runs, recommended, groupBy, group), groupPager)));
        var apply = UI.Primary(vm, "应用筛选", Apply, Glyph.Filter);
        apply.HorizontalAlignment = HorizontalAlignment.Right;
        header.Children.Add(new Expander { Header = "筛选条件", Content = UI.Stack(UI.Columns(4, filters.Values.Cast<UIElement>().Concat(filterOptions.Values).Append(personFilter).ToArray()), apply), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
        header.Children.Add(status);
        pager = new Pager(vm, async p =>
        {
            page = p;
            await Load();
        }, "条", false);
        // Action bar: selection on the left, the operation and its required inputs, then preview.
        all.VerticalAlignment = VerticalAlignment.Center;
        selection.VerticalAlignment = VerticalAlignment.Center;
        selection.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
        var preview = UI.Primary(vm, "预览批量操作", () => Preview(false), Glyph.Preview);
        preview.VerticalAlignment = VerticalAlignment.Bottom;
        var inputs = UI.Flow(operation);
        if (assignee != null)
            inputs.Children.Add(assignee);
        inputs.Add(conclusion, preview);
        inputs.Spacing = 12;
        var bar = UI.Split(UI.Stack(UI.Flow(all, selection), pager), inputs, 980, 1);
        ShowOperationInputs();
        ShowSelection();
        Content = UI.Layout(header, table, UI.Card(bar));
        Loaded += async (_, _) => await vm.Guard(Generate);
    }

    // Only the fields the chosen action uses are shown; hidden ones are not submitted.
    void ShowOperationInputs()
    {
        var op = operation.Value();
        if (assignee != null)
            assignee.Visibility = op == "assign" ? Visibility.Visible : Visibility.Collapsed;
        conclusion.Visibility = op is "close" or "approve" ? Visibility.Visible : Visibility.Collapsed;
        (conclusion.Parent as UIElement)?.InvalidateMeasure();
    }

    int SelectedCount => all.IsChecked == true ? Math.Max(0, total - excluded.Count) : selected.Count;

    void ShowSelection() => selection.Text = $"已选 {SelectedCount} 条";
    void SyncChecks()
    {
        if (table.Content is ListView list)
            foreach (var check in list.Items.OfType<FrameworkElement>().Select(x => x.Tag).OfType<CheckBox>())
                check.IsChecked = all.IsChecked == true ? !excluded.Contains((string)check.Tag) : selected.Contains((string)check.Tag);
    }

    async Task Apply()
    {
        Clear();
        await LoadGroups();
        await Load();
    }

    void Clear()
    {
        all.IsChecked = false;
        selected.Clear();
        excluded.Clear();
        selectedCountries.Clear();
        excludedCountries.Clear();
        choices.Clear();
        page = 1;
        batch = null;
        ShowSelection();
    }

    Dictionary<string, string> Filters()
    {
        var f = filters.ToDictionary(x => x.Key, x => x.Value.Text.Trim());
        f["assignee"] = personFilter.Text.Trim();
        foreach (var(key, control)in filterOptions)
            f[key] = control.Value();
        f["recommended_only"] = recommended.Value();
        if (group.Value() != "")
        {
            f["group_by"] = groupBy.Value();
            f["group_key"] = group.Value();
        }

        return f;
    }

    string ScopeKey() => vm.Generation + ":" + setID + ":" + vm.Query(Filters()) + ":" + Json.Node(new { all = all.IsChecked == true, selected = selectedCountries.OrderBy(x => x.Key).ToArray(), excluded = excludedCountries.OrderBy(x => x.Key).ToArray() }).ToJsonString();
    async Task<string[]> RequiredCountries()
    {
        if (SelectedCount == 0 || setID == "")
            throw new ArgumentException("请先勾选要派单的项目。");
        string key = ScopeKey();
        string[] countries;
        if (all.IsChecked == true)
            countries = await AssignmentScope.ReviewCountries(vm.Api, setID, Filters(), new Dictionary<string, string>(excludedCountries), vm.Token);
        else
        {
            if (selected.Count != selectedCountries.Count)
                throw new InvalidOperationException("所选记录的国家未加载，请重新选择。");
            countries = selectedCountries.Values.Distinct().ToArray();
        }

        if (key != ScopeKey())
            throw new OperationCanceledException("选择范围已变化，请重新选择账户。");
        if (countries.Length == 0)
            throw new ArgumentException("当前筛选没有可派单的项目，请刷新。");
        return countries;
    }

    async Task Generate()
    {
        if (vm.RunID == "")
        {
            table.Content = UI.Empty("请先创建并发布数据运行。", Glyph.Info);
            return;
        }

        Clear();
        var serial = ++generateRequest;
        var gen = vm.Generation;
        var run = vm.RunID;
        JsonNode result;
        try
        {
            result = await vm.Api.Send("review-sets", new { run_id = run }, ct: vm.Token);
        }
        catch (Exception)when (gen != vm.Generation || serial != generateRequest || run != vm.RunID)
        {
            return;
        }

        if (gen != vm.Generation || serial != generateRequest || run != vm.RunID)
            return;
        setID = result.S("id");
        await LoadGroups();
        await Load();
    }

    async Task LoadGroups()
    {
        if (setID == "")
            return;
        var serial = ++groupRequest;
        var gen = vm.Generation;
        var f = Filters();
        f.Remove("group_key");
        f["group_by"] = groupBy.Value();
        JsonNode result;
        try
        {
            result = await vm.Api.Send($"review-sets/{setID}/groups?" + vm.Query(f, groupPage), ct: vm.Token);
        }
        catch (Exception)when (gen != vm.Generation || serial != groupRequest)
        {
            return;
        }

        if (gen != vm.Generation || serial != groupRequest)
            return;
        selecting = true;
        try
        {
            group.Items.Clear();
            group.Items.Add(new Choice("", "当前筛选全部组"));
            foreach (var n in result.A("items"))
                group.Items.Add(new Choice(n.S("key"), $"{n.S("label")} · {n.I("count")}条 / 推荐{n.I("recommended")}"));
            groupTotal = result.I("total");
            group.SelectedIndex = 0;
            groupStatus.Text = $"共 {groupTotal} 组 · 组列表第 {groupPage}/{Math.Max(1, (groupTotal + 49) / 50)} 页";
        }
        finally
        {
            selecting = false;
        }
    }

    async Task Load()
    {
        if (setID == "")
            return;
        long serial = ++request, gen = vm.Generation;
        JsonNode result;
        try
        {
            result = await vm.Api.Send($"review-sets/{setID}?" + vm.Query(Filters(), page), ct: vm.Token);
        }
        catch (Exception)when (gen != vm.Generation || serial != request)
        {
            return;
        }

        if (serial != request || gen != vm.Generation)
            return;
        total = result.I("total");
        pager.Update(page, total);
        status.Text = $"共 {total} 条可处理项目 · 已关闭只读隐藏 {result.I("readonly")} 条";
        ShowSelection();
        var rows = result.A("items");
        if (rows.Count == 0)
        {
            table.Content = UI.Empty("当前范围没有可处理项目，可调整范围或筛选条件。");
            return;
        }

        var cards = new ListView
        {
            SelectionMode = ListViewSelectionMode.None
        };
        foreach (var n in rows)
        {
            var item = n!;
            var c = item["case"]!;
            string caseID = c.S("id");
            var check = new CheckBox
            {
                Tag = caseID,
                MinWidth = 0,
                IsChecked = all.IsChecked == true ? !excluded.Contains(caseID) : selected.Contains(caseID),
                VerticalAlignment = VerticalAlignment.Top
            };
            Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(check, $"选择 {c.S("cancel_id")}");
            check.Checked += (_, _) =>
            {
                if (all.IsChecked == true)
                {
                    excluded.Remove(caseID);
                    excludedCountries.Remove(caseID);
                }
                else
                {
                    selected.Add(caseID);
                    selectedCountries[caseID] = item["cancel"].S("country");
                }

                ShowSelection();
            };
            check.Unchecked += (_, _) =>
            {
                if (all.IsChecked == true)
                {
                    excluded.Add(caseID);
                    excludedCountries[caseID] = item["cancel"].S("country");
                }
                else
                {
                    selected.Remove(caseID);
                    selectedCountries.Remove(caseID);
                }

                ShowSelection();
            };
            var options = item.A("options");
            var option = UI.Select("配对建议", new[] { new Choice("", "请选择配对建议") }.Concat(options.Select(o => new Choice(o.S("id"), $"{Labels.Of(o.S("kind"))} · {o.S("invoice")} · 销售{o.S("sale_id")} · 余量{o.S("remaining")}"))), choices.GetValueOrDefault(caseID, item.S("choice_id")));
            option.HorizontalAlignment = HorizontalAlignment.Left;
            option.MinWidth = 360;
            if (option.Value() != "")
                choices[caseID] = option.Value();
            option.SelectionChanged += (_, _) =>
            {
                if (option.Value() == "")
                    choices.Remove(caseID);
                else
                    choices[caseID] = option.Value();
            };
            var open = new HyperlinkButton
            {
                Content = "打开差异详情",
                VerticalAlignment = VerticalAlignment.Top
            };
            open.Click += (_, _) => App.Window.OpenCase(caseID);
            var title = UI.Flow(UI.Mono(c.S("cancel_id"), 15), UI.Text(Labels.Of(c.S("reason"))), UI.Status(c.S("status")));
            title.Spacing = 12;
            var info = UI.Stack(title);
            info.Spacing = 8;
            if (item.S("notice") != "")
                info.Children.Add(UI.Muted(item.S("notice"), true));
            info.Add(option, new Expander { Header = "候选、重复组和整单关联", Content = UI.Details(options), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
            var g = new Grid
            {
                ColumnSpacing = 12,
                Tag = check
            };
            g.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
            g.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
            g.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
            Grid.SetColumn(info, 1);
            Grid.SetColumn(open, 2);
            g.Add(check, info, open);
            var card = UI.Card(g);
            card.Tag = check;
            card.Margin = new Thickness(0, 0, 0, 8);
            cards.Items.Add(card);
        }

        table.Content = cards;
    }

    async Task Preview(bool retry)
    {
        if (retry)
        {
            var result = await vm.Api.Send("review-sets", new { run_id = vm.RunID });
            setID = result.S("id");
            batch = await vm.Api.Send("case-batches/preview", new { set_id = setID, retry_batch_id = batch!.S("id") });
        }
        else
        {
            var filter = Filters();
            filter.Remove("recommended_only");
            filter.Remove("group_by");
            filter.Remove("group_key");
            filter["run_id"] = vm.RunID;
            var op = operation.Value();
            if (op == "assign")
            {
                string key = ScopeKey();
                await assignee!.ValidateSelection();
                if (key != ScopeKey())
                    throw new OperationCanceledException("选择范围已变化，请重新预览。");
            }

            batch = await vm.Api.Send("case-batches/preview", new { set_id = setID, action = op, filter, recommended_only = recommended.Value() == "true", all = all.IsChecked == true, ids = selected.ToArray(), exclude_ids = excluded.ToArray(), choices, assignee = op == "assign" ? assignee?.Text.Trim() ?? "" : "", conclusion = op is "close" or "approve" ? conclusion.Text : "", group_by = group.Value() == "" ? "" : groupBy.Value(), group_key = group.Value() });
        }

        await ShowBatch();
    }

    async Task ShowBatch()
    {
        if (batch == null || batch.S("id") == "")
        {
            vm.Message("此运行还没有批量记录。");
            return;
        }

        var displayed = batch;
        var p = UI.Stack();
        p.MinWidth = 0;
        string view = displayed.S("view");
        int current = 1;
        void Display()
        {
            p.Children.Clear();
            p.Children.Add(UI.Metrics(("项目总数", displayed!.I("total").ToString(), Tone.Neutral), ("可提交", displayed.I("ready").ToString(), Tone.Progress), ("成功", displayed.I("succeeded").ToString(), Tone.Done), ("跳过", displayed.I("skipped").ToString(), Tone.Pending), ("失败", displayed.I("failed").ToString(), Tone.Danger)));
            var statuses = new SelectorBar();
            foreach (var k in new[]
            {
                "ready",
                "succeeded",
                "skipped",
                "failed"
            }

            )
                statuses.Items.Add(new SelectorBarItem { Text = Labels.Of(k), Tag = k, IsSelected = k == view });
            statuses.SelectionChanged += async (s, _) =>
            {
                if (s.SelectedItem?.Tag is string key && key != view)
                {
                    current = 1;
                    view = key;
                    await vm.Guard(Fetch);
                }
            };
            p.Children.Add(statuses);
            var items = displayed.A("items");
            if (items.Count == 0)
                p.Children.Add(UI.Muted("此分类没有项目。"));
            foreach (var n in items)
            {
                var head = UI.Flow(UI.Mono(n.S("cancel_id")), UI.Muted(n.S("invoice")), UI.Status(n.S("status")));
                head.Spacing = 12;
                var entry = UI.Stack(head);
                entry.Spacing = 6;
                if (n.S("message") != "")
                    entry.Children.Add(UI.Muted(n.S("message"), true));
                if (n!["option"] is JsonObject option)
                    entry.Children.Add(new Expander { Header = "采用的配对建议", Content = UI.Details(option), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
                p.Children.Add(UI.Card(entry));
            }

            var results = new Pager(vm, async page =>
            {
                current = page;
                await Fetch();
            }, "条", false);
            results.Update(current, displayed.I("view_total"));
            p.Children.Add(results);
            p.Children.Add(new Expander { Header = "跳过与失败原因", Content = UI.Details(displayed!["reasons"]), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
        }

        async Task Fetch()
        {
            displayed = await vm.Api.Send($"case-batches/{displayed!.S("id")}?page={current}&status=" + Uri.EscapeDataString(view), ct: vm.Token);
            Display();
        }

        Display();
        var d = UI.Dialog(displayed.S("status") == "preview" ? $"批量预览 · {Labels.Of(displayed.S("action"))}" : "批量结果", p);
        d.CloseButtonText = "关闭";
        d.PrimaryButtonText = displayed.S("status") == "preview" ? "提交批量操作" : "重新预览未成功项";
        if (await d.ShowAsync() == ContentDialogResult.Primary)
        {
            if (displayed.S("status") == "preview")
            {
                batch = await vm.Api.Send($"case-batches/{displayed.S("id")}/commit", new { });
                await ShowBatch();
                await Generate();
            }
            else
            {
                batch = displayed;
                await Preview(true);
            }
        }
    }
}

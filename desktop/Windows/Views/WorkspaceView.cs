using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;

namespace Reconciliation.Windows.Views;
public sealed class WorkspaceView : UserControl
{
    readonly AppViewModel vm;
    readonly string kind;
    readonly ContentControl table = UI.Host();
    readonly ContentControl overview = UI.Host();
    CancellationTokenSource? reading;
    readonly StackPanel header = UI.Stack();
    readonly Dictionary<string, TextBox> fields = [];
    readonly Dictionary<string, ComboBox> selects = [];
    readonly ComboBox runs;
    readonly AccountPicker personFilter;
    readonly Expander filterPanel = new()
    {
        Header = "筛选条件",
        HorizontalAlignment = HorizontalAlignment.Stretch,
        HorizontalContentAlignment = HorizontalAlignment.Stretch
    };
    readonly Pager pager;
    Dictionary<string, string> applied = [];
    int page = 1, total;
    long request;
    public WorkspaceView(AppViewModel model, string key)
    {
        vm = model;
        kind = key;
        runs = UI.Select("当前数据运行", vm.Runs.Select(x => new Choice(x.RunId, x.Label)), vm.RunID);
        runs.HorizontalAlignment = HorizontalAlignment.Stretch;
        runs.MinWidth = 0;
        personFilter = UI.AssigneePicker(vm, "责任人账户");
        runs.SelectionChanged += async (_, _) =>
        {
            vm.RunID = runs.Value();
            page = 1;
            await vm.Guard(Load);
        };
        var filters = new List<UIElement>();
        foreach (var(k, label)in new[]
        {
            ("customer", "客户编号"),
            ("stock", "商品编号"),
            ("invoice", "单号"),
            ("country", "国家/地区"),
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
                    await vm.Guard(Query);
                }
            };
            fields[k] = box;
            filters.Add(box);
        }

        filters.Add(personFilter);
        void Choice(string k, string label, params (string, string)[] values)
        {
            var box = UI.Select(label, UI.Choices(values));
            selects[k] = box;
            filters.Add(box);
        }

        Choice("status", "差异状态", ("", "全部"), ("pending", "待处理"), ("in_progress", "处理中"), ("closed", "已关闭"));
        Choice("waiting", "待补充", ("", "全部"), ("true", "待补充"), ("false", "非待补充"));
        Choice("reason", "差异原因", new[] { ("", "全部原因") }.Concat(Labels.CaseReasons.Select(x => (x, Labels.Of(x)))).ToArray());
        if (key == "transactions")
        {
            Choice("sort", "排序依据", ("source", "来源顺序"), ("date", "交易时间"), ("invoice", "单号"), ("customer", "客户"), ("stock", "商品"), ("country", "国家/地区"));
            Choice("direction", "排序方向", ("asc", "升序"), ("desc", "降序"));
            Choice("kind", "交易类型", ("", "全部"), ("sale", "销售"), ("cancel", "取消"));
            Choice("classification", "分类", ("", "全部"), ("matched", "已配对"), ("suspected", "疑似"), ("no_original", "无合格原单"));
        }

        var actions = new List<UIElement>();
        if (key == "audit")
            actions.Add(UI.Button(vm, "刷新", Load, Glyph.Refresh));
        if (key == "transactions" && vm.Session?.Role != "warehouse")
            actions.Add(UI.Button(vm, "导出筛选 CSV", async () => await UI.Save(await vm.Api.Export(vm.Query(applied), vm.Token), "reconciliation", ".csv"), Glyph.Download));
        var queryButton = UI.Primary(vm, "查询", Query, Glyph.Find);
        var resetButton = UI.Button(vm, "重置筛选", async () =>
        {
            foreach (var f in fields.Values)
                f.Text = "";
            personFilter.Text = "";
            foreach (var c in selects.Values)
                c.SelectedIndex = 0;
            await Query();
        }, Glyph.Clear);
        header.Children.Add(UI.PageHeader(key switch
        {
            "transactions" => "交易检索",
            "allocations" => "配对明细",
            "audit" => "审计记录",
            _ => "差异处理"
        }, key switch
        {
            "transactions" => "检索两个年度的源交易；单价与时间保持原始精度，筛选与排序均由服务端执行。",
            "allocations" => "已确认的退货与销售配对，含数量、精确金额与确认方式。",
            "audit" => "最近的业务操作记录，点击一行查看操作前后内容。",
            _ => "按国家/地区权限查看全部差异，点击一行进入核查、补充资料与处理。"
        }, actions.ToArray()));
        if (key != "audit")
        {
            header.Children.Add(runs);
            header.Children.Add(overview);
            filterPanel.Content = UI.Stack(UI.Columns(4, filters.ToArray()), UI.Flow(queryButton, resetButton));
            header.Children.Add(filterPanel);
        }

        pager = new Pager(vm, async p =>
        {
            page = p;
            await Load();
        });
        applied = Filters();
        Content = UI.Layout(header, table, key == "audit" ? null : pager);
        Loaded += async (_, _) => await vm.Guard(Load);
        Unloaded += (_, _) =>
        {
            request++;
            reading?.Cancel();
            reading?.Dispose();
            reading = null;
        };
    }

    async Task Query()
    {
        page = 1;
        applied = Filters();
        int active = applied.Count(x => x.Value != "" && x.Key is not ("sort" or "direction"));
        filterPanel.Header = active == 0 ? "筛选条件" : $"筛选条件 · 已应用 {active} 项";
        await Load();
    }

    Dictionary<string, string> Filters()
    {
        var result = fields.ToDictionary(x => x.Key, x => x.Value.Text.Trim());
        result["assignee"] = personFilter.Text.Trim();
        foreach (var(k, c)in selects)
            result[k] = c.Value();
        return result;
    }

    async Task Load()
    {
        if (kind != "audit" && vm.RunID == "")
        {
            table.Content = UI.Empty("还没有已发布的数据运行；请在“数据任务”中准备官方数据。", Glyph.Info);
            return;
        }

        var serial = ++request;
        var gen = vm.Generation;
        reading?.Cancel();
        reading?.Dispose();
        reading = CancellationTokenSource.CreateLinkedTokenSource(vm.Token);
        var ct = reading.Token;
        string run = vm.RunID;
        try
        {
            var value = await vm.Api.Send(kind + (kind == "audit" ? "" : "?" + vm.Query(applied, page)), ct: ct);
            if (gen != vm.Generation || serial != request)
                return;
            JsonArray rows;
            if (kind == "audit")
                rows = value.AsArray();
            else
            {
                rows = value.A("items");
                total = value.I("total");
            }

            pager.Update(page, total);
            Column[] cols = kind switch
            {
                "transactions" => [new("id", "来源ID", 120, ColumnKind.Mono), new("invoice", "单号", 130, ColumnKind.Mono), new("time", "交易时间", 230, ColumnKind.Mono), new("customer", "客户", 90, ColumnKind.Mono), new("stock", "商品", 100, ColumnKind.Mono), new("quantity", "数量", 70, ColumnKind.Number), new("price", "精确单价", 200, ColumnKind.Number), new("country", "国家/地区", 140)],
                "allocations" => [new("cancel_id", "取消来源", 130, ColumnKind.Mono), new("sale_id", "销售来源", 130, ColumnKind.Mono), new("quantity", "数量", 80, ColumnKind.Number), new("amount", "精确金额", 220, ColumnKind.Number), new("method", "方式", 120), new("actor", "操作人", 140)],
                "audit" => [new("occurred_at", "时间", 230, ColumnKind.Mono), new("actor", "操作人"), new("action", "操作"), new("method", "方式"), new("result", "结果")],
                _ => [new("cancel_id", "来源记录", 130, ColumnKind.Mono), new("reason", "差异原因", 200), new("status", "状态", 100, ColumnKind.Status), new("assignee", "责任人", 140), new("version", "版本", 60, ColumnKind.Number), new("source_row", "原行", 90, ColumnKind.Number)]
            };
            table.Content = UI.Table(rows, cols, n =>
            {
                if (kind == "cases")
                    App.Window.OpenCase(n.S("id"));
                else
                    _ = vm.Guard(() => UI.Show("记录详情", UI.Details(n)));
            });
            if (kind != "audit")
            {
                var o = await vm.Api.Send("overview?run_id=" + Uri.EscapeDataString(run), ct: ct);
                if (serial != request || gen != vm.Generation)
                    return;
                var cases = o["cases"];
                var figures = UI.Metrics(("差异总数", cases.I("total").ToString(), Tone.Neutral), ("待处理", cases.I("pending").ToString(), Tone.Pending), ("处理中", cases.I("in_progress").ToString(), Tone.Progress), ("已关闭", cases.I("closed").ToString(), Tone.Done), ("待补充", cases.I("waiting").ToString(), Tone.Pending), ("尚未配对", o.I("currently_unallocated").ToString(), Tone.Neutral));
                var note = UI.Muted($"补录销售 {o.I("supplemental_sales")} · 人工复核 {o.I("reviewed_allocations")} · {o.S("caveat")}", true);
                note.VerticalAlignment = VerticalAlignment.Center;
                overview.Content = UI.Stack(figures, UI.Flow(note, new HyperlinkButton { Content = "来源及行数守恒", Padding = new Thickness(4, 0, 4, 0) }.With(b => b.Click += (_, _) => _ = vm.Guard(() => UI.Show("来源及行数守恒", UI.Details(o["initial_reconciliation"]))))));
            }
        }
        catch (Exception)when (serial != request || gen != vm.Generation || ct.IsCancellationRequested)
        {
        }
    }
}

static class ElementExtensions
{
    public static T With<T>(this T element, Action<T> setup)
    {
        setup(element);
        return element;
    }
}

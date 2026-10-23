using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;

namespace Reconciliation.Windows.Views;
public sealed class CaseView : UserControl
{
    readonly AppViewModel vm;
    readonly string id;
    readonly Action back;
    readonly StackPanel panel = UI.Stack();
    JsonNode data = new JsonObject();
    int candidatePage = 1;
    readonly Dictionary<string, string> requestIDs = [];
    public CaseView(AppViewModel model, string caseID, Action goBack)
    {
        vm = model;
        id = caseID;
        back = goBack;
        panel.Spacing = 16;
        panel.Padding = new Thickness(0, 0, 12, 24);
        Content = UI.Scroll(panel);
        Loaded += async (_, _) => await vm.Guard(Load);
    }

    async Task Load()
    {
        var gen = vm.Generation;
        var value = await vm.Api.Send($"cases/{Uri.EscapeDataString(id)}/evidence?page={candidatePage}&page_size=50", ct: vm.Token);
        var history = await vm.Api.Send($"cases/{Uri.EscapeDataString(id)}/audit", ct: vm.Token);
        if (gen != vm.Generation)
            return;
        data = value;
        Render(history);
    }

    async Task Mutate(string action, object body)
    {
        var result = await vm.Api.Send($"cases/{Uri.EscapeDataString(id)}/{action}", body);
        vm.Message(result.S("message")is { Length: > 0 } m ? m : "操作已保存。");
        await Load();
    }

    void Render(JsonNode history)
    {
        panel.Children.Clear();
        var c = data["case"]!;
        var row = data["effective_cancel"] ?? data["cancel"]!;
        var version = c.I("version");
        bool owner = c.S("assignee") == vm.Session!.Id;
        bool editable = owner && c.S("status") == "in_progress";
        bool closed = c.S("status") == "closed";
        var confirmedSale = data["confirmation"]?["allocation"].S("sale_id");
        // Title block: record, state, round and owner at a glance.
        var nav = new Grid();
        nav.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
        nav.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
        nav.Children.Add(UI.Button(vm, "返回列表", () =>
        {
            back();
            return Task.CompletedTask;
        }, Glyph.Back));
        var refresh = UI.Button(vm, "刷新详情", Load, Glyph.Refresh);
        Grid.SetColumn(refresh, 1);
        nav.Children.Add(refresh);
        var title = UI.Flow(UI.Heading("差异", "TitleTextBlockStyle"), new TextBlock { Text = c.S("cancel_id"), Style = (Style)Application.Current.Resources["TitleTextBlockStyle"], FontFamily = (Microsoft.UI.Xaml.Media.FontFamily)Application.Current.Resources["LedgerMonoFont"], IsTextSelectionEnabled = true }, UI.Status(c.S("status")));
        title.Spacing = 12;
        if (c.S("waiting") == "true")
            title.Children.Add(UI.Badge("待补充", Tone.Pending));
        if (closed)
            title.Children.Add(UI.Seal("已结案"));
        var meta = UI.Muted($"{Labels.Of(c.S("reason"))} · 第 {Math.Max(1, c.I("generation"))} 轮 · 责任人 {(c.S("assignee")is { Length: > 0 } who ? who : "未派单")} · 版本 {version}");
        panel.Add(nav, UI.Stack(title, meta));
        panel.Children.Add(UI.Split(UI.Section("退货记录", UI.Details(row), data["effective_cancel"] != null ? "显示当前有效值；原始记录在下方证据中保留。" : null), Actions(c, version, owner, editable, closed)));
        if (data["confirmation"] is JsonNode confirmation)
            panel.Children.Add(UI.Section("已确认配对", UI.Details(confirmation), "此取消行已经配对，其余候选不可再选。"));
        panel.Children.Add(Candidates(editable, version, confirmedSale));
        if (editable)
            panel.Children.Add(Remediation(c, row));
        var related = data.A("related_cases");
        if (related.Count > 0)
        {
            var links = UI.Stack();
            links.Spacing = 4;
            foreach (var item in related)
            {
                var r = item!;
                var link = new HyperlinkButton
                {
                    Content = $"关联问题 {r.S("cancel_id")} · {Labels.Of(r.S("reason"))}"};
                link.Click += (_, _) => App.Window.OpenCase(r.S("id"));
                links.Children.Add(link);
            }

            panel.Children.Add(UI.Section("关联问题", links, "原因改变后生成的关联差异，点击打开。"));
        }

        panel.Children.Add(new Expander { Header = "原始记录、决策证据与补充历史", Content = UI.Stack(UI.Heading("原始记录", "BodyStrongTextBlockStyle"), UI.Details(data["original_cancel"]), UI.Heading("决策证据", "BodyStrongTextBlockStyle"), UI.Details(data["decision_evidence"]), UI.Heading("补充历史", "BodyStrongTextBlockStyle"), UI.Details(data["remediations"])), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
        panel.Children.Add(new Expander { Header = "操作历史", Content = UI.Details(history), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
    }

    // The side panel always says what the signed-in account can do next, and why not otherwise.
    UIElement Actions(JsonNode c, int version, bool owner, bool editable, bool closed)
    {
        var p = UI.Stack();
        if (closed)
        {
            p.Children.Add(UI.Seal("已结案 · 只读"));
            p.Children.Add(UI.Heading("核查结论", "BodyStrongTextBlockStyle"));
            var conclusion = UI.Text(c.S("conclusion")is { Length: > 0 } text ? text : "无");
            conclusion.IsTextSelectionEnabled = true;
            p.Children.Add(conclusion);
            return UI.Section("处理", p);
        }

        if (c.S("status") == "pending" && c.S("assignee") == "" && vm.Session!.Role != "warehouse")
        {
            var assignee = UI.AssigneePicker(vm, countries: () => Task.FromResult(new[] { (data["effective_cancel"] ?? data["cancel"]).S("country") }));
            p.Add(UI.Muted("尚未派单。选择负责此国家的处理人后派单。"), assignee, UI.Primary(vm, "派单", async () =>
            {
                await assignee.ValidateSelection();
                await Mutate("assign", new { assignee = assignee.Text.Trim() });
            }, Glyph.Send));
        }
        else if (owner && c.S("status") == "pending")
            p.Add(UI.Muted("已派给你，开始处理后可确认候选、补充资料和结案。"), UI.Primary(vm, "开始处理", () => Mutate("start", new { version }), Glyph.Play));
        else if (editable)
        {
            var conclusion = UI.Field("核查结论", c.S("conclusion"), true);
            p.Add(UI.Muted("只有配对或核实重复等已解决问题才能结案；未解决请先补充资料或登记待补充。"), conclusion, UI.Primary(vm, "保存结论并结案", async () =>
            {
                if (await UI.Confirm("关闭差异", UI.Text("只有配对或核实重复等已解决问题才能关闭；未解决问题请补充资料或登记待补充。"), "结案"))
                    await Mutate("close", new { version, conclusion = conclusion.Text });
            }, Glyph.Accept));
        }
        else
            p.Children.Add(UI.Muted(c.S("assignee") == "" ? "等待财务或管理员派单。" : $"由 {c.S("assignee")} 负责；只有责任人在处理中时可以确认、补充或结案。"));
        return UI.Section("处理", p);
    }

    UIElement Candidates(bool editable, int version, string? confirmedSale)
    {
        var candidates = data["candidates"]!;
        var body = UI.Stack(Comparison(candidates.A("items")));
        foreach (var candidate in candidates.A("items"))
        {
            var sale = candidate!;
            var chosen = confirmedSale == sale.S("id");
            var b = chosen ? UI.Button(vm, "已确认", () => Task.CompletedTask, Glyph.Accept) : UI.Primary(vm, "确认此候选", async () =>
            {
                if (await UI.Confirm("确认销售候选", UI.Stack(UI.Text("核对客户、商品、时间、精确单价和余量后确认。"), UI.Details(sale))))
                    await Mutate("confirm-match", new { version, sale_row_id = sale.S("id") });
            });
            b.IsEnabled = editable && data["confirmation"] == null;
            var grid = new Grid
            {
                ColumnSpacing = 16
            };
            grid.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
            grid.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
            var heading = UI.Flow(UI.Mono($"销售 {sale.S("id")}", 15), UI.Muted(sale.S("invoice")));
            heading.Spacing = 12;
            if (chosen)
                heading.Children.Add(UI.Badge("已确认", Tone.Done));
            var info = UI.Stack(heading, UI.Mono($"客户 {sale.S("customer")} · 商品 {sale.S("stock")} · 国家 {sale.S("country")}\n时间 {sale.S("time")} · 数量 {sale.S("quantity")} · 精确单价 {sale.S("price")} · 当前余量 {sale.S("current_remaining")}", 13));
            info.Spacing = 6;
            if (sale["duplicate_group"] is JsonNode group && group is not JsonValue)
                info.Children.Add(new Expander { Header = "重复组", Content = UI.Details(group), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
            b.VerticalAlignment = VerticalAlignment.Top;
            Grid.SetColumn(b, 1);
            grid.Add(info, b);
            body.Children.Add(UI.Card(grid));
        }

        var pager = new Pager(vm, async p =>
        {
            candidatePage = p;
            await Load();
        }, "条候选", false);
        pager.Update(candidatePage, candidates.I("total"));
        if (candidates.I("total") > 50)
            body.Children.Add(pager);
        return UI.Section("销售候选对比", body, $"合格候选 {candidates.I("total")} 条" + (editable ? "；确认前请逐项核对字段差异。" : "；仅责任人在处理中时可确认。"));
    }

    UIElement Comparison(JsonArray current)
    {
        var rows = current.Where(x => x != null).Select(x => x!).ToList();
        if (data["confirmation"]?["sale"] is JsonNode saved)
        {
            rows.RemoveAll(x => x.S("id") == saved.S("id"));
            rows.Insert(0, saved);
        }

        if (rows.Count == 0)
            return UI.Empty("暂无当前合格候选。");
        var fields = new[]
        {
            ("invoice", "单号"),
            ("time", "销售时间"),
            ("customer", "客户"),
            ("stock", "商品"),
            ("price", "精确单价"),
            ("country", "国家"),
            ("quantity", "数量"),
            ("current_remaining", "当前余量")
        };
        if (rows.Count > 4)
        {
            var table = UI.Table(new JsonArray(rows.Select(x => x.DeepClone()).ToArray()), fields.Select(x => new Column(x.Item1, x.Item2, x.Item1 is "time" or "price" ? 210 : 120, x.Item1 is "country" ? ColumnKind.Text : x.Item1 is "quantity" or "current_remaining" ? ColumnKind.Number : ColumnKind.Mono)).ToArray());
            table.Height = 340;
            return table;
        }

        var g = new Grid
        {
            ColumnSpacing = 2,
            RowSpacing = 2
        };
        g.ColumnDefinitions.Add(new() { Width = new GridLength(110) });
        for (int i = 0; i < rows.Count; i++)
            g.ColumnDefinitions.Add(new() { Width = new GridLength(220) });
        for (int i = 0; i <= fields.Length; i++)
            g.RowDefinitions.Add(new() { Height = GridLength.Auto });
        void Cell(int row, int col, string text, bool different, bool head = false)
        {
            var t = col == 0 ? UI.Muted(text) : UI.Mono(text, 13);
            if (head)
                t.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
            var b = new Border
            {
                Padding = new Thickness(10, 6, 10, 6),
                CornerRadius = new CornerRadius(4),
                Child = t
            };
            if (different)
                b.Style = (Style)Application.Current.Resources["DiffCellStyle"];
            Grid.SetRow(b, row);
            Grid.SetColumn(b, col);
            g.Children.Add(b);
        }

        Cell(0, 0, "对比字段", false, true);
        for (int i = 0; i < rows.Count; i++)
            Cell(0, i + 1, "销售 " + rows[i].S("id") + (data["confirmation"]?["allocation"].S("sale_id") == rows[i].S("id") ? " · 已确认" : ""), false, true);
        for (int r = 0; r < fields.Length; r++)
        {
            var(key, label) = fields[r];
            bool differs = rows.Select(x => x.S(key)).Distinct().Count() > 1;
            Cell(r + 1, 0, label, false);
            for (int c = 0; c < rows.Count; c++)
                Cell(r + 1, c + 1, rows[c].S(key), differs);
        }

        return UI.Stack(UI.Muted(rows.Count > 1 ? "每列一条销售，琥珀底色标出各候选之间取值不同的字段。" : "当前唯一候选的关键字段。", true), new ScrollViewer { Content = g, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, VerticalScrollBarVisibility = ScrollBarVisibility.Disabled });
    }

    UIElement Remediation(JsonNode c, JsonNode row)
    {
        var action = UI.Select("处理方法", UI.Choices(("correct_return", "核实修正退货"), ("supplement_sale", "补录销售原单"), ("verify_duplicate", "核实重复或独立记录"), ("wait_materials", "登记待补充"), ("recheck", "重新核查")));
        action.HorizontalAlignment = HorizontalAlignment.Left;
        action.MinWidth = 260;
        var dynamic = UI.Host();
        var evidence = UI.Field("依据及凭证说明", "", true);
        var box = UI.Stack(action, dynamic, evidence);
        Dictionary<string, TextBox> fields = [];
        ComboBox? disposition = null;
        TextBox? ids = null, canonical = null;
        SourceTimeField? time = null;
        void Inputs()
        {
            fields.Clear();
            var p = UI.Stack();
            string mode = action.Value();
            if (mode is "correct_return" or "supplement_sale")
            {
                var keys = mode == "correct_return" ? new[]
                {
                    ("customer", "客户编号"),
                    ("country", "国家"),
                    ("stock", "商品编号"),
                    ("price", "精确单价"),
                    ("quantity", "数量（正整数）")
                }

                : new[]
                {
                    ("invoice", "销售单号"),
                    ("document_ref", "凭证号"),
                    ("line_ref", "凭证明细号"),
                    ("description", "商品说明"),
                    ("customer", "客户编号"),
                    ("country", "国家"),
                    ("stock", "商品编号"),
                    ("price", "精确单价"),
                    ("quantity", "销售数量")
                };
                var inputs = new List<UIElement>();
                foreach (var(k, label)in keys)
                {
                    string initial = k == "quantity" && mode == "correct_return" ? Math.Abs(long.Parse(row.S(k))).ToString() : k == "quantity" ? "" : row.S(k);
                    var input = UI.Field(label, initial);
                    input.IsReadOnly = mode == "supplement_sale" && new[]
                    {
                        "customer",
                        "country",
                        "stock",
                        "price"
                    }.Contains(k);
                    fields[k] = input;
                    inputs.Add(input);
                }

                p.Children.Add(UI.Columns(3, inputs.ToArray()));
                time = new SourceTimeField(mode == "correct_return" ? "退货时间" : "销售时间", mode == "correct_return" ? row.S("time") : "");
                p.Children.Add(time);
            }
            else if (mode == "verify_duplicate")
            {
                ids = UI.Field("核实源行 ID（逗号分隔）");
                canonical = UI.Field("重复组代表源行 ID");
                disposition = UI.Select("核实结果", UI.Choices(("duplicate", "确为重复"), ("independent", "各自独立")));
                p.Add(new Expander { Header = "疑似重复的源行", Content = UI.Details(data["duplicate_rows"]), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch }, UI.Columns(3, ids, canonical, disposition));
            }
            else if (mode == "wait_materials")
            {
                fields["missing_materials"] = UI.Field("缺少的资料", c.S("missing_materials"), true);
                fields["next_step"] = UI.Field("下一步", c.S("next_step"), true);
                p.Children.Add(UI.Columns(2, fields.Values.ToArray()));
            }
            else
                p.Children.Add(UI.Muted("按当前有效值重新运行配对核查，不修改记录。"));
            dynamic.Content = p;
        }

        action.SelectionChanged += (_, _) => Inputs();
        Inputs();
        box.Children.Add(UI.Primary(vm, "保存并重新核查", async () =>
        {
            var body = new JsonObject
            {
                ["version"] = c.I("version"),
                ["action"] = action.Value(),
                ["evidence"] = evidence.Text
            };
            if (action.Value() == "correct_return")
            {
                var changes = new JsonObject();
                foreach (var(k, f)in fields)
                {
                    string original = k == "quantity" ? Math.Abs(long.Parse(row.S(k))).ToString() : row.S(k);
                    if (f.Text != original)
                        changes[k] = f.Text;
                }

                if (time!.Value != row.S("time"))
                    changes["time"] = time.Value;
                body["changes"] = changes;
            }
            else if (action.Value() == "supplement_sale")
            {
                var sale = new JsonObject();
                foreach (var(k, f)in fields)
                    sale[k] = f.Text;
                sale["time"] = time!.Value;
                body["sale"] = sale;
            }
            else if (action.Value() == "verify_duplicate")
            {
                body["row_ids"] = Json.Node(ids!.Text.Split(',', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries));
                body["canonical_id"] = canonical!.Text;
                body["disposition"] = disposition!.Value();
            }
            else if (action.Value() == "wait_materials")
                foreach (var(k, f)in fields)
                    body[k] = f.Text;
            var fingerprint = body.ToJsonString();
            if (!requestIDs.TryGetValue(fingerprint, out var request))
                requestIDs[fingerprint] = request = Guid.NewGuid().ToString();
            body["request_id"] = request;
            await Mutate("remediations", body);
        }, Glyph.Refresh));
        return UI.Section("补充资料与重新核查", box, "修正只覆盖有效值，保留原始数据与初始配对历史。");
    }
}

public sealed class SourceTimeField : UserControl
{
    readonly string original;
    readonly DatePicker date = new()
    {
        Header = "日期"
    };
    readonly TimePicker time = new()
    {
        Header = "时分",
        ClockIdentifier = "24HourClock"
    };
    readonly TextBox seconds = UI.Field("秒（可含小数）", "00");
    bool changed;
    public string Value => !changed ? original : SourceTimestamp.Edit(original, date.Date.Date, time.Time.Hours, time.Time.Minutes, seconds.Text);

    public SourceTimeField(string label, string value)
    {
        original = value;
        if (value.Length >= 19 && DateTime.TryParseExact(value[..19], "yyyy-MM-dd'T'HH:mm:ss", System.Globalization.CultureInfo.InvariantCulture, System.Globalization.DateTimeStyles.None, out var parsed))
        {
            date.Date = new DateTimeOffset(parsed);
            time.Time = parsed.TimeOfDay;
            seconds.Text = value[17..];
        }

        date.DateChanged += (_, _) => changed = true;
        time.TimeChanged += (_, _) => changed = true;
        seconds.TextChanged += (_, _) => changed = true;
        seconds.Width = 160;
        var head = UI.Flow(UI.Heading(label, "BodyStrongTextBlockStyle"), UI.Muted(value == "" ? "不做时区换算" : $"原值 {value} · 不做时区换算", true));
        head.Spacing = 12;
        Content = UI.Stack(head, UI.Flow(date, time, seconds));
    }
}

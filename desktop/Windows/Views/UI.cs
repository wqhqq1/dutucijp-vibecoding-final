using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Markup;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Automation;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;
using Windows.Storage.Pickers;

namespace Reconciliation.Windows.Views;
public record Choice(string ID, string Label)
{
    public override string ToString() => Label;
}

public enum ColumnKind
{
    Text,
    Mono,
    Number,
    Status
}

public record Column(string Key, string Label, double Width = 150, ColumnKind Kind = ColumnKind.Text);
public record FlatRow(JsonNode Value, string[] Cells, Tone[] Tones)
{
    public override string ToString() => string.Join("；", Cells);
}

public enum Tone
{
    Neutral,
    Pending,
    Progress,
    Done,
    Danger
}

public static class Glyph
{
    public const string Back = "\uE72B", Prev = "\uE76B", Next = "\uE76C", Refresh = "\uE72C", Find = "\uE721", Clear = "\uE894", Download = "\uE896", Add = "\uE710", Delete = "\uE74D", Save = "\uE74E", Accept = "\uE8FB", Send = "\uE724", Play = "\uE768", Open = "\uE8A7", Link = "\uE71B", Key = "\uE8D7", Fingerprint = "\uE928", Phone = "\uE8EA", Edit = "\uE70F", History = "\uE81C", Plug = "\uE703", SignOut = "\uF3B1", Shield = "\uEA18", People = "\uE716", Preview = "\uE890", Filter = "\uE71C", Empty = "\uE7C3", Info = "\uE946";
}

public static class UI
{
    static Style S(string key) => (Style)Application.Current.Resources[key];
    public static TextBlock Text(string text, double size = 14) => new()
    {
        Text = text,
        FontSize = size,
        TextWrapping = TextWrapping.Wrap
    };
    public static TextBlock Muted(string text, bool caption = false) => new()
    {
        Text = text,
        Style = S(caption ? "MutedCaptionStyle" : "MutedTextStyle"),
        TextWrapping = TextWrapping.Wrap
    };
    public static TextBlock Heading(string text, string style = "SubtitleTextBlockStyle") => new()
    {
        Text = text,
        Style = S(style),
        TextWrapping = TextWrapping.Wrap
    };
    public static TextBlock Mono(string text, double size = 14) => new()
    {
        Text = text,
        FontSize = size,
        FontFamily = (FontFamily)Application.Current.Resources["LedgerMonoFont"],
        IsTextSelectionEnabled = true,
        TextWrapping = TextWrapping.Wrap
    };
    public static StackPanel Stack(params UIElement[] children)
    {
        var p = new StackPanel
        {
            Spacing = 12
        };
        foreach (var c in children)
            p.Children.Add(c);
        return p;
    }

    public static StackPanel Row(params UIElement[] children)
    {
        var p = Stack(children);
        p.Orientation = Orientation.Horizontal;
        p.Spacing = 8;
        return p;
    }

    public static FlowPanel Flow(params UIElement[] children)
    {
        var panel = new FlowPanel();
        foreach (var child in children)
            panel.Children.Add(child);
        return panel;
    }

    public static Border Card(UIElement content) => new()
    {
        Style = S("LedgerCardStyle"),
        Child = content
    };
    /// <summary>Card with a title line, optional caption and right-aligned actions.</summary>
    public static Border Section(string title, UIElement content, string? caption = null, params UIElement[] actions)
    {
        var head = new Grid
        {
            ColumnSpacing = 12
        };
        head.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
        head.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
        var titles = new StackPanel
        {
            Spacing = 2
        };
        titles.Children.Add(Heading(title, "BodyStrongTextBlockStyle"));
        if (!string.IsNullOrEmpty(caption))
            titles.Children.Add(Muted(caption, true));
        head.Children.Add(titles);
        if (actions.Length > 0)
        {
            var bar = Row(actions);
            bar.VerticalAlignment = VerticalAlignment.Center;
            Grid.SetColumn(bar, 1);
            head.Children.Add(bar);
        }

        return Card(new StackPanel { Spacing = 14, Children = { head, content } });
    }

    /// <summary>Page title, one-line purpose and the page's own commands on the right.</summary>
    public static Grid PageHeader(string title, string? subtitle = null, params UIElement[] actions)
    {
        var titles = Stack(Heading(title, "TitleTextBlockStyle"));
        if (!string.IsNullOrEmpty(subtitle))
            titles.Children.Add(Muted(subtitle));
        if (actions.Length == 0)
        {
            var grid = new Grid();
            grid.Children.Add(titles);
            return grid;
        }

        return Split(titles, Flow(actions), 850, 1.5);
    }

    public static Tone ToneOf(string status) => status switch
    {
        "pending" or "new" or "password" or "factor" or "recovering" or "queued" or "suspected" or "waiting" => Tone.Pending,
        "in_progress" or "running" or "ready" => Tone.Progress,
        "closed" or "complete" or "succeeded" or "approved" or "matched" => Tone.Done,
        "failed" or "rejected" or "deleted" or "interrupted" or "no_original" => Tone.Danger,
        _ => Tone.Neutral
    };
    public static void Paint(Border badge, Tone tone)
    {
        badge.Style = S($"Tone{tone}Badge");
        if (badge.Child is TextBlock t)
            t.Style = S($"Tone{tone}Text");
    }

    /// <summary>Pill showing a state label in the shared case-state colours.</summary>
    public static Border Badge(string label, Tone tone)
    {
        var b = new Border
        {
            CornerRadius = new CornerRadius(10),
            Padding = new Thickness(9, 1, 9, 2),
            HorizontalAlignment = HorizontalAlignment.Left,
            VerticalAlignment = VerticalAlignment.Center,
            Child = new TextBlock
            {
                Text = label,
                FontSize = 12
            }
        };
        Paint(b, tone);
        AutomationProperties.SetName(b, label);
        return b;
    }

    public static Border Status(string status) => Badge(Labels.Of(status), ToneOf(status));
    /// <summary>The vermilion seal stamp used for closed, read-only cases.</summary>
    public static Border Seal(string label) => new()
    {
        Style = S("SealStampStyle"),
        VerticalAlignment = VerticalAlignment.Center,
        HorizontalAlignment = HorizontalAlignment.Left,
        RenderTransform = new RotateTransform
        {
            Angle = -4
        },
        Child = new TextBlock
        {
            Text = label,
            Style = S("SealTextStyle")
        }
    };
    /// <summary>
    /// Ledger figures in one strip. The first figure is the total and sits on a double rule,
    /// as in the web client; attention figures carry a tone dot.
    /// </summary>
    public static Border Metrics(params (string Label, string Value, Tone Tone)[] items)
    {
        var grid = new AdaptiveColumns
        {
            MaxColumns = Math.Max(1, items.Length),
            MinimumCellWidth = 128,
            ColumnSpacing = 16,
            RowSpacing = 16,
            BalanceRows = true
        };
        foreach (var(label, value, tone)in items)
        {
            var name = Flow();
            if (tone != Tone.Neutral)
                name.Children.Add(ToneDot(tone));
            name.Children.Add(Muted(label, true));
            var figure = new TextBlock
            {
                Text = value,
                Style = S("MetricValueStyle"),
                TextWrapping = TextWrapping.Wrap
            };
            var cell = Stack(name, figure);
            cell.Spacing = 2;
            if (grid.Children.Count == 0)
                cell.Children.Add(new Border { Style = S("LedgerRuleStyle"), BorderThickness = new Thickness(0, 1, 0, 1), Height = 4, Width = 28, HorizontalAlignment = HorizontalAlignment.Left });
            AutomationProperties.SetName(cell, $"{label} {value}");
            grid.Children.Add(cell);
        }

        return Card(grid);
    }

    // The tone's text colour (not its wash) marks figures that need attention.
    static TextBlock ToneDot(Tone tone) => new()
    {
        Text = "\u25CF",
        FontSize = 9,
        Style = S($"Tone{tone}Text"),
        VerticalAlignment = VerticalAlignment.Center
    };
    /// <summary>Content slot that lets tables and cards take the full width and height.</summary>
    public static ContentControl Host() => new()
    {
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
        VerticalContentAlignment = VerticalAlignment.Stretch,
        IsTabStop = false
    };
    public static ScrollViewer Scroll(UIElement content) => new()
    {
        Content = content,
        HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
        Padding = new Thickness(0, 0, 12, 8),
        HorizontalContentAlignment = HorizontalAlignment.Stretch
    };
    public static TextBox Field(string label, string value = "", bool multiline = false)
    {
        var box = new TextBox
        {
            Header = label,
            Text = value,
            VerticalAlignment = VerticalAlignment.Bottom,
            MinWidth = 0,
            AcceptsReturn = multiline,
            TextWrapping = multiline ? TextWrapping.Wrap : TextWrapping.NoWrap
        };
        if (multiline)
            box.MinHeight = 72;
        AutomationProperties.SetName(box, label);
        return box;
    }

    public static PasswordBox Password(string label)
    {
        var box = new PasswordBox
        {
            Header = label,
            MinWidth = 0
        };
        AutomationProperties.SetName(box, label);
        return box;
    }

    public static ComboBox Select(string label, IEnumerable<Choice> choices, string selected = "")
    {
        var c = new ComboBox
        {
            Header = label,
            MinWidth = 0,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Bottom
        };
        foreach (var choice in choices)
            c.Items.Add(choice);
        c.SelectedItem = c.Items.Cast<Choice>().FirstOrDefault(x => x.ID == selected) ?? c.Items.FirstOrDefault();
        AutomationProperties.SetName(c, label);
        return c;
    }

    public static string Value(this ComboBox c) => (c.SelectedItem as Choice)?.ID ?? "";
    static object Label(string label, string? glyph) => glyph == null ? label : new StackPanel
    {
        Orientation = Orientation.Horizontal,
        Spacing = 8,
        Children =
        {
            new FontIcon
            {
                Glyph = glyph,
                FontSize = 14
            },
            new TextBlock
            {
                Text = label
            }
        }
    };
    public static Button Button(AppViewModel vm, string label, Func<Task> action, string? glyph = null)
    {
        var b = new Button
        {
            Content = Label(label, glyph)
        };
        AutomationProperties.SetName(b, label);
        b.Click += async (_, _) =>
        {
            if (!b.IsEnabled)
                return;
            b.IsEnabled = false;
            try
            {
                await vm.Guard(action);
            }
            finally
            {
                b.IsEnabled = true;
            }
        };
        return b;
    }

    /// <summary>The single emphasised command of a screen or section.</summary>
    public static Button Primary(AppViewModel vm, string label, Func<Task> action, string? glyph = null)
    {
        var b = Button(vm, label, action, glyph);
        b.Style = S("AccentButtonStyle");
        return b;
    }

    public static void Add(this Panel p, params UIElement[] children)
    {
        foreach (var c in children)
            p.Children.Add(c);
    }

    public static AdaptiveColumns Form(params UIElement[] fields) => Columns(3, fields);
    public static AdaptiveColumns Columns(int count, params UIElement[] fields)
    {
        var grid = new AdaptiveColumns
        {
            MaxColumns = count
        };
        foreach (var field in fields)
            grid.Children.Add(field);
        return grid;
    }

    public static Grid Layout(UIElement header, UIElement content, UIElement? footer = null)
    {
        var g = new Grid
        {
            RowSpacing = 16
        };
        g.RowDefinitions.Add(new() { Height = GridLength.Auto });
        g.RowDefinitions.Add(new() { Height = new GridLength(1, GridUnitType.Star) });
        var headerScroll = Scroll(header);
        headerScroll.MaxHeight = 420;
        g.SizeChanged += (_, e) => headerScroll.MaxHeight = Math.Max(100, e.NewSize.Height * 0.50);
        g.Children.Add(headerScroll);
        Grid.SetRow((FrameworkElement)content, 1);
        g.Children.Add(content);
        if (footer != null)
        {
            g.RowDefinitions.Add(new() { Height = GridLength.Auto });
            var footerScroll = new ScrollViewer
            {
                Content = footer,
                HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
                VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
                Padding = new Thickness(0, 0, 12, 8)
            };
            g.SizeChanged += (_, e) => footerScroll.MaxHeight = Math.Max(100, e.NewSize.Height * 0.38);
            Grid.SetRow(footerScroll, 2);
            g.Children.Add(footerScroll);
        }

        return g;
    }

    public static UIElement Empty(string text, string glyph = Glyph.Empty)
    {
        var p = new StackPanel
        {
            Spacing = 8,
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center,
            Margin = new Thickness(24, 40, 24, 40)
        };
        p.Children.Add(new FontIcon { Glyph = glyph, FontSize = 28, Opacity = 0.55 });
        var t = Muted(text);
        t.TextAlignment = TextAlignment.Center;
        p.Children.Add(t);
        return p;
    }

    /// <summary>
    /// Server-paged ledger table. Header and cells share one padding so columns line up; IDs and
    /// figures use the mono face, figures align right, states render as badges. Wider than the
    /// viewport scrolls sideways without breaking the ListView's vertical virtualisation.
    /// </summary>
    public static FrameworkElement Table(JsonArray rows, Column[] columns, Action<JsonNode>? selected = null)
    {
        const string mono = "Cascadia Mono, Consolas";
        var g = new Grid();
        g.RowDefinitions.Add(new() { Height = GridLength.Auto });
        g.RowDefinitions.Add(new() { Height = new GridLength(1, GridUnitType.Star) });
        var header = new Grid
        {
            Padding = new Thickness(16, 10, 16, 10),
            ColumnSpacing = 12
        };
        foreach (var c in columns)
            header.ColumnDefinitions.Add(new() { Width = new GridLength(c.Width) });
        for (int i = 0; i < columns.Length; i++)
        {
            var t = Muted(columns[i].Label, true);
            t.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
            t.TextWrapping = TextWrapping.NoWrap;
            t.TextTrimming = TextTrimming.CharacterEllipsis;
            if (columns[i].Kind == ColumnKind.Number)
                t.TextAlignment = TextAlignment.Right;
            Grid.SetColumn(t, i);
            header.Children.Add(t);
        }

        g.Children.Add(new Border { Style = S("LedgerTableHeaderStyle"), Child = header });
        var lv = new ListView
        {
            SelectionMode = ListViewSelectionMode.Single,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            IsItemClickEnabled = selected != null,
            Padding = new Thickness(0, 4, 0, 4)
        };
        var container = new Style(typeof(ListViewItem));
        container.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(12, 0, 12, 0)));
        container.Setters.Add(new Setter(FrameworkElement.MinHeightProperty, 40d));
        container.Setters.Add(new Setter(Control.HorizontalContentAlignmentProperty, HorizontalAlignment.Stretch));
        lv.ItemContainerStyle = container;
        var widths = string.Join("", columns.Select(c => $"<ColumnDefinition Width='{c.Width}'/>"));
        var cells = string.Join("", columns.Select((c, i) => c.Kind switch
        {
            ColumnKind.Status => $"<Border Grid.Column='{i}' CornerRadius='10' Padding='9,1,9,2' HorizontalAlignment='Left' VerticalAlignment='Center'><TextBlock FontSize='12' Text='{{Binding Cells[{i}]}}'/></Border>",
            _ => $"<TextBlock Grid.Column='{i}' Text='{{Binding Cells[{i}]}}' TextTrimming='CharacterEllipsis' ToolTipService.ToolTip='{{Binding Cells[{i}]}}' VerticalAlignment='Center'{(c.Kind is ColumnKind.Mono or ColumnKind.Number ? $" FontFamily='{mono}' FontSize='13'" : "")}{(c.Kind == ColumnKind.Number ? " TextAlignment='Right'" : "")}/>"}));
        lv.ItemTemplate = (DataTemplate)XamlReader.Load($"<DataTemplate xmlns='http://schemas.microsoft.com/winfx/2006/xaml/presentation'><Grid Padding='4,9' ColumnSpacing='12'><Grid.ColumnDefinitions>{widths}</Grid.ColumnDefinitions>{cells}</Grid></DataTemplate>");
        if (columns.Any(c => c.Kind == ColumnKind.Status))
            lv.ContainerContentChanging += (_, e) =>
            {
                if (e.InRecycleQueue || e.Item is not FlatRow row || e.ItemContainer.ContentTemplateRoot is not Grid cellsGrid)
                    return;
                for (int i = 0; i < columns.Length && i < cellsGrid.Children.Count; i++)
                    if (columns[i].Kind == ColumnKind.Status && cellsGrid.Children[i] is Border badge)
                        Paint(badge, row.Tones[i]);
            };
        lv.ItemsSource = rows.Where(n => n != null).Select(n => new FlatRow(n!, columns.Select(c => Format(n!, c.Key)).ToArray(), columns.Select(c => c.Kind == ColumnKind.Status ? ToneOf(Raw(n!, c.Key)) : Tone.Neutral).ToArray())).ToArray();
        if (selected != null)
            lv.ItemClick += (_, e) => selected(((FlatRow)e.ClickedItem).Value);
        Grid.SetRow(lv, 1);
        g.Children.Add(lv);
        if (rows.Count == 0)
        {
            var empty = Empty("当前范围没有记录，可调整筛选条件后重新查询。");
            Grid.SetRow((FrameworkElement)empty, 1);
            g.Children.Add(empty);
        }

        var frame = new Border
        {
            Style = S("LedgerTableStyle"),
            Child = g,
            MinWidth = columns.Sum(c => c.Width) + 12 * columns.Length + 56
        };
        var scroll = new ScrollViewer
        {
            Content = frame,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled
        };
        // Inside a horizontal scroller the frame would shrink to its columns; keep it full width.
        double natural = frame.MinWidth;
        scroll.SizeChanged += (_, e) => frame.MinWidth = Math.Max(natural, e.NewSize.Width);
        return scroll;
    }

    static string Raw(JsonNode n, string key)
    {
        JsonNode? value = n;
        foreach (var part in key.Split('.'))
            value = value?[part];
        return value?.ToString() ?? "";
    }

    public static string Format(JsonNode n, string key)
    {
        JsonNode? value = n;
        foreach (var part in key.Split('.'))
            value = value?[part];
        if (value is JsonArray a)
            return string.Join("、", a.Select(x => x?.ToString()));
        var s = value?.ToString() ?? "—";
        return Labels.Of(s);
    }

    static readonly HashSet<string> MonoFields = ["id", "invoice", "customer", "stock", "price", "quantity", "time", "version", "source_row", "amount", "remaining", "current_remaining", "original_quantity", "generation", "document_ref", "line_ref", "raw_json", "total"];
    static bool IsMono(string key) => MonoFields.Contains(key) || key.EndsWith("_id") || key.EndsWith("_at") || key.EndsWith("_row");
    /// <summary>
    /// Structured label/value view of a record. Values stay the exact service strings, are
    /// selectable for copying, and nested objects fold into expanders.
    /// </summary>
    public static UIElement Details(JsonNode? node, string title = "来源与证据")
    {
        if (node is JsonObject o)
        {
            var g = new Grid
            {
                ColumnSpacing = 16,
                RowSpacing = 6
            };
            g.ColumnDefinitions.Add(new() { Width = new GridLength(124) });
            g.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
            var nested = new List<UIElement>();
            int row = 0;
            foreach (var pair in o)
            {
                if (pair.Value is JsonObject or JsonArray)
                {
                    bool empty = pair.Value is JsonArray { Count: 0 } || pair.Value is JsonObject { Count: 0 };
                    if (empty)
                    {
                        AddPair(g, row++, Labels.Field(pair.Key), "无", false);
                        continue;
                    }

                    nested.Add(new Expander { Header = Labels.Field(pair.Key), Content = Details(pair.Value, pair.Key), HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Stretch });
                }
                else
                    AddPair(g, row++, Labels.Field(pair.Key), Labels.Of(pair.Value?.ToString() ?? "无"), IsMono(pair.Key));
            }

            if (nested.Count == 0)
                return g;
            var p = new StackPanel
            {
                Spacing = 8
            };
            if (row > 0)
                p.Children.Add(g);
            foreach (var n in nested)
                p.Children.Add(n);
            return p;
        }

        if (node is JsonArray a)
        {
            if (a.Count == 0)
                return Muted("无记录");
            var p = new StackPanel
            {
                Spacing = 10
            };
            for (int i = 0; i < a.Count; i++)
            {
                if (i > 0)
                    p.Children.Add(new Border { Style = S("LedgerDividerStyle"), Height = 1 });
                p.Children.Add(Details(a[i]));
            }

            return p;
        }

        var t = Text(node?.ToString() ?? "无");
        t.IsTextSelectionEnabled = true;
        return t;
    }

    static void AddPair(Grid g, int row, string label, string value, bool mono)
    {
        g.RowDefinitions.Add(new() { Height = GridLength.Auto });
        var name = Muted(label);
        var text = mono ? Mono(value) : Text(value);
        text.IsTextSelectionEnabled = true;
        AutomationProperties.SetName(text, $"{label} {value}");
        Grid.SetRow(name, row);
        Grid.SetRow(text, row);
        Grid.SetColumn(text, 1);
        g.Children.Add(name);
        g.Children.Add(text);
    }

    /// <summary>Left-aligned reading column: full width up to maxWidth (plain left alignment would shrink to content).</summary>
    public static Grid Column(UIElement content, double maxWidth)
    {
        var g = new Grid();
        g.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star), MaxWidth = maxWidth });
        g.Children.Add(content);
        return g;
    }

    /// <summary>Two panes side by side, stacked when the page is narrower than the breakpoint.</summary>
    public static Grid Split(UIElement main, UIElement side, double breakpoint = 980, double ratio = 1.6)
    {
        var g = new Grid
        {
            ColumnSpacing = 16,
            RowSpacing = 16
        };
        g.ColumnDefinitions.Add(new() { Width = new GridLength(ratio, GridUnitType.Star) });
        g.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
        g.RowDefinitions.Add(new() { Height = GridLength.Auto });
        g.RowDefinitions.Add(new() { Height = GridLength.Auto });
        g.Children.Add(main);
        g.Children.Add(side);
        void Arrange(double width)
        {
            bool wide = width >= breakpoint;
            Grid.SetColumn((FrameworkElement)side, wide ? 1 : 0);
            Grid.SetRow((FrameworkElement)side, wide ? 0 : 1);
            Grid.SetColumnSpan((FrameworkElement)main, wide ? 1 : 2);
            Grid.SetColumnSpan((FrameworkElement)side, wide ? 1 : 2);
        }

        Arrange(breakpoint);
        g.SizeChanged += (_, e) => Arrange(e.NewSize.Width);
        return g;
    }

    /// <summary>Numbered progress through a fixed sequence; earlier steps show as done.</summary>
    public static StackPanel Steps(int current, params string[] steps)
    {
        var row = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8
        };
        for (int i = 0; i < steps.Length; i++)
        {
            if (i > 0)
                row.Children.Add(new Border { Style = S("LedgerDividerStyle"), Width = 28, Height = 1, VerticalAlignment = VerticalAlignment.Center });
            var tone = i + 1 < current ? Tone.Done : i + 1 == current ? Tone.Progress : Tone.Neutral;
            var badge = Badge($"{(i + 1 < current ? "✓" : (i + 1).ToString())}  {steps[i]}", tone);
            if (i + 1 == current && badge.Child is TextBlock t)
                t.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
            row.Children.Add(badge);
        }

        AutomationProperties.SetName(row, $"第 {current} 步，共 {steps.Length} 步：{string.Join("、", steps)}");
        return row;
    }

    /// <summary>
    /// Searchable responsible-person field. Suggestions come from /api/users (name, account,
    /// role, countries); the submitted value is the account ID typed or chosen.
    /// </summary>
    public static AccountPicker AssigneePicker(AppViewModel vm, string label = "派单目标账户", Func<Task<string[]>>? countries = null) => new(vm, label, countries);
    /// <summary>
    /// Country checkboxes using the service's source values. Selected countries missing from
    /// the offered list stay listed (and checked) so editing never silently drops them.
    /// </summary>
    public static (UIElement View, Func<string[]> Selected) CountryPicker(IEnumerable<string> available, IEnumerable<string> selected)
    {
        var chosen = selected.ToHashSet();
        var all = available.Concat(chosen.Where(c => !available.Contains(c))).Distinct().ToArray();
        var boxes = all.Select(c =>
        {
            var box = new CheckBox
            {
                Content = Text(c),
                Tag = c,
                IsChecked = chosen.Contains(c),
                MinWidth = 0,
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Stretch
            };
            AutomationProperties.SetName(box, c);
            ToolTipService.SetToolTip(box, c);
            return box;
        }).ToArray();
        var wrap = new AdaptiveColumns
        {
            MaxColumns = 3,
            MinimumCellWidth = 210,
            RowSpacing = 4
        };
        foreach (var b in boxes)
            wrap.Children.Add(b);
        var view = all.Length == 0 ? (UIElement)Muted("当前没有可选国家/地区。") : new ScrollViewer
        {
            Content = wrap,
            MaxHeight = 220,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto
        };
        return (view, () => boxes.Where(b => b.IsChecked == true).Select(b => (string)b.Tag).ToArray());
    }

    /// <summary>ContentDialog in the window's current theme with the Fluent default style.</summary>
    public static ContentDialog Dialog(string title, UIElement content)
    {
        var dialog = new ContentDialog
        {
            Title = title,
            Content = Scroll(content),
            Style = S("DefaultContentDialogStyle"),
            RequestedTheme = App.Window.Theme,
            XamlRoot = App.Window.XamlRoot,
            DefaultButton = ContentDialogButton.None,
            HorizontalContentAlignment = HorizontalAlignment.Stretch
        };
        void Fit()
        {
            double width = Math.Max(240, Math.Min(880, App.Window.XamlRoot.Size.Width - 48));
            // ContentDialog is the full-window overlay host. Constraining that host
            // also moves its centered template panel away from the window center.
            dialog.Resources["ContentDialogMaxWidth"] = width;
            dialog.Resources["ContentDialogMinWidth"] = Math.Min(320d, width);
            dialog.Resources["ContentDialogMaxHeight"] = Math.Max(184, Math.Min(756, App.Window.XamlRoot.Size.Height - 48));
        }

        Fit();
        var root = dialog.XamlRoot;
        void RootChanged(XamlRoot sender, XamlRootChangedEventArgs args) => Fit();
        dialog.Opened += (_, _) =>
        {
            root.Changed += RootChanged;
            Fit();
        };
        dialog.Closed += (_, _) => root.Changed -= RootChanged;
        return dialog;
    }

    public static async Task<bool> Confirm(string title, UIElement content, string primary = "确认")
    {
        var d = Dialog(title, content);
        d.PrimaryButtonText = primary;
        d.CloseButtonText = "取消";
        return await d.ShowAsync() == ContentDialogResult.Primary;
    }

    public static async Task Show(string title, UIElement content)
    {
        var d = Dialog(title, content);
        d.CloseButtonText = "关闭";
        d.DefaultButton = ContentDialogButton.Close;
        await d.ShowAsync();
    }

    public static async Task Save(byte[] bytes, string name, string extension)
    {
        var picker = new FileSavePicker
        {
            SuggestedFileName = name
        };
        picker.FileTypeChoices.Add(extension, new[] { extension });
        WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(App.Window));
        var file = await picker.PickSaveFileAsync();
        if (file != null)
            await global::Windows.Storage.FileIO.WriteBytesAsync(file, bytes);
    }

    public static Choice[] Choices(params (string, string)[] entries) => entries.Select(x => new Choice(x.Item1, x.Item2)).ToArray();
}

/// <summary>Previous / page of pages / next, plus a jump box, in one aligned bar.</summary>
public sealed class Pager : UserControl
{
    readonly TextBlock status = UI.Muted("");
    readonly NumberBox jump = new()
    {
        Minimum = 1,
        Value = 1,
        Width = 96,
        SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact
    };
    public int Page { get; private set; } = 1;
    public int Total { get; private set; }
    public int PageSize { get; init; } = 50;
    public int Pages => Math.Max(1, (Total + PageSize - 1) / PageSize);

    public Pager(AppViewModel vm, Func<int, Task> go, string unit = "条", bool jumpBox = true)
    {
        Unit = unit;
        AutomationProperties.SetName(jump, "跳至页");
        var prev = UI.Button(vm, "上一页", async () =>
        {
            if (Page > 1)
                await go(Page - 1);
        }, Glyph.Prev);
        var next = UI.Button(vm, "下一页", async () =>
        {
            if (Page < Pages)
                await go(Page + 1);
        }, Glyph.Next);
        status.VerticalAlignment = VerticalAlignment.Center;
        status.TextWrapping = TextWrapping.Wrap;
        var row = UI.Flow(prev, status, next);
        row.Spacing = 12;
        if (jumpBox)
        {
            var label = UI.Muted("跳至");
            label.VerticalAlignment = VerticalAlignment.Center;
            row.Children.Add(new Border { Width = 12 });
            row.Add(label, jump, UI.Button(vm, "跳页", async () => await go(Math.Clamp((int)(double.IsNaN(jump.Value) ? 1 : jump.Value), 1, Pages))));
        }

        Content = row;
        Update(1, 0);
    }

    string Unit { get; }

    public void Update(int page, int total)
    {
        Page = page;
        Total = total;
        status.Text = $"第 {page} / {Pages} 页 · 共 {total} {Unit}";
        jump.Maximum = Pages;
        jump.Value = page;
    }
}

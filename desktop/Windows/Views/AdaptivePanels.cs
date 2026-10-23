using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Foundation;

namespace Reconciliation.Windows.Views;
// Measure against the actual viewport, including a dialog's narrower content area.
public sealed class AdaptiveColumns : Panel
{
    public int MaxColumns { get; set; } = 3;
    public double MinimumCellWidth { get; set; } = 180;
    public double ColumnSpacing { get; set; } = 12;
    public double RowSpacing { get; set; } = 12;
    public bool BalanceRows { get; set; }

    int Count(double width)
    {
        int cols = Math.Max(1, Math.Min(MaxColumns, double.IsFinite(width) ? (int)((width + ColumnSpacing) / (MinimumCellWidth + ColumnSpacing)) : MaxColumns));
        int total = Children.Count(c => c.Visibility == Visibility.Visible);
        return BalanceRows && total > 0 ? (int)Math.Ceiling((double)total / Math.Ceiling((double)total / cols)) : cols;
    }

    protected override Size MeasureOverride(Size available)
    {
        int cols = Count(available.Width);
        double cell = double.IsFinite(available.Width) ? Math.Max(0, (available.Width - ColumnSpacing * (cols - 1)) / cols) : MinimumCellWidth;
        double height = 0, row = 0;
        int index = 0;
        foreach (var child in Children)
        {
            child.Measure(new Size(cell, double.PositiveInfinity));
            if (child.Visibility != Visibility.Visible)
                continue;
            row = Math.Max(row, child.DesiredSize.Height);
            if (++index % cols == 0)
            {
                height += row + RowSpacing;
                row = 0;
            }
        }

        if (index % cols != 0)
            height += row + RowSpacing;
        return new Size(double.IsFinite(available.Width) ? available.Width : cols * (cell + ColumnSpacing) - ColumnSpacing, Math.Max(0, height - RowSpacing));
    }

    protected override Size ArrangeOverride(Size final)
    {
        int cols = Count(final.Width);
        double cell = Math.Max(0, (final.Width - ColumnSpacing * (cols - 1)) / cols), y = 0;
        var children = Children.Where(c => c.Visibility == Visibility.Visible).ToArray();
        for (int i = 0; i < children.Length; i += cols)
        {
            double height = children.Skip(i).Take(cols).Max(c => c.DesiredSize.Height);
            for (int j = 0; j < cols && i + j < children.Length; j++)
                children[i + j].Arrange(new Rect(j * (cell + ColumnSpacing), y, cell, height));
            y += height + RowSpacing;
        }

        return final;
    }
}

public sealed class FlowPanel : Panel
{
    public double Spacing { get; set; } = 8;
    public double RowSpacing { get; set; } = 12;

    protected override Size MeasureOverride(Size available)
    {
        double x = 0, y = 0, row = 0, width = 0;
        foreach (var child in Children)
        {
            child.Measure(new Size(available.Width, double.PositiveInfinity));
            if (child.Visibility != Visibility.Visible)
                continue;
            var size = child.DesiredSize;
            if (x > 0 && x + size.Width > available.Width)
            {
                y += row + RowSpacing;
                x = 0;
                row = 0;
            }

            width = Math.Max(width, x + size.Width);
            x += size.Width + Spacing;
            row = Math.Max(row, size.Height);
        }

        return new Size(Math.Min(width, available.Width), y + row);
    }

    protected override Size ArrangeOverride(Size final)
    {
        var rows = new List<List<UIElement>>();
        var row = new List<UIElement>();
        double width = 0, y = 0;
        foreach (var child in Children.Where(c => c.Visibility == Visibility.Visible))
        {
            if (row.Count > 0 && width + child.DesiredSize.Width > final.Width)
            {
                rows.Add(row);
                row = [];
                width = 0;
            }

            row.Add(child);
            width += child.DesiredSize.Width + Spacing;
        }

        if (row.Count > 0)
            rows.Add(row);
        foreach (var cells in rows)
        {
            double height = cells.Max(c => c.DesiredSize.Height), x = 0;
            foreach (var child in cells)
            {
                double w = Math.Min(final.Width, child.DesiredSize.Width);
                child.Arrange(new Rect(x, y + height - child.DesiredSize.Height, w, child.DesiredSize.Height));
                x += w + Spacing;
            }

            y += height + RowSpacing;
        }

        return final;
    }
}

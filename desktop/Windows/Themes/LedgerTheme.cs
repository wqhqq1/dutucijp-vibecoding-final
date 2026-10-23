using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Markup;

namespace Reconciliation.Windows;
/// <summary>Merges the embedded Themes/Ledger.xaml into the running Application once.</summary>
public static class LedgerTheme
{
    static bool loaded;
    public static void Ensure()
    {
        if (loaded)
            return;
        using var stream = typeof(LedgerTheme).Assembly.GetManifestResourceStream("Reconciliation.Windows.Themes.Ledger.xaml") ?? throw new InvalidOperationException("界面主题资源缺失。");
        using var reader = new StreamReader(stream);
        Application.Current.Resources.MergedDictionaries.Add((ResourceDictionary)XamlReader.Load(reader.ReadToEnd()));
        loaded = true;
    }
}

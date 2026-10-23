using System.Text.Json;

namespace Reconciliation.Windows.Services;
public record ClientSettings(string Address = "http://localhost:8080", string Theme = "Default", bool Configured = false)
{
    public static string FilePath => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "ReturnsReconciliation.Windows", "settings.json");

    public static ClientSettings Load()
    {
        try
        {
            return JsonSerializer.Deserialize<ClientSettings>(File.ReadAllText(FilePath), Json.Options) ?? new();
        }
        catch (Exception e)when (e is IOException or JsonException or UnauthorizedAccessException)
        {
            return new();
        }
    }

    public void Save()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(FilePath)!);
        var temporary = FilePath + ".tmp";
        File.WriteAllText(temporary, JsonSerializer.Serialize(this, Json.Options));
        File.Move(temporary, FilePath, true);
    }
}

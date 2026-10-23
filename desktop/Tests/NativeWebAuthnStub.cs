using System.Text.Json.Nodes;
namespace Reconciliation.Windows.Services;
// Network/state tests never invoke platform authentication or alter real credentials.
public static class NativeWebAuthn {
    public static Func<JsonNode,NativeBinding,bool,CancellationToken,Task<JsonNode>>? Handler { get; set; }
    public static int Calls { get; private set; }
    public static void Reset() { Calls=0; Handler=null; }
    public static Task<JsonNode> Perform(JsonNode options, NativeBinding binding, bool register, CancellationToken ct) {
        Calls++;
        return Handler?.Invoke(options,binding,register,ct) ?? throw new InvalidOperationException("Native WebAuthn is outside this isolated test suite.");
    }
}

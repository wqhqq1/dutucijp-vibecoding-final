using System.ComponentModel;
using System.Runtime.CompilerServices;
using System.Text.Json.Nodes;
using Reconciliation.Windows.Services;

namespace Reconciliation.Windows.ViewModels;
public sealed class AppViewModel : INotifyPropertyChanged, IDisposable
{
    public event PropertyChangedEventHandler? PropertyChanged;
    public event Action? SessionChanged;
    public ClientSettings Settings { get; private set; }

    readonly Action<ClientSettings> persist;
    public AppViewModel(ClientSettings? initialSettings = null, Action<ClientSettings>? persistSettings = null)
    {
        Settings = initialSettings ?? ClientSettings.Load();
        persist = persistSettings ?? (settings => settings.Save());
    }

    public BackendClient Api { get; private set; } = null !;
    public ClientConfiguration? Configuration { get; private set; }
    public UserSession? Session { get; private set; }
    public RunInfo[] Runs { get; private set; } = [];
    public string RunID { get; set; } = "";
    public string[] RecoveryCodes { get; private set; } = [];
    public JsonNode? CurrentJob { get; set; }
    public int PendingRequests { get; private set; }
    public int UnconfiguredAccounts { get; private set; }

    long attentionRequest;
    void ClearAttention()
    {
        attentionRequest++;
        PendingRequests = 0;
        UnconfiguredAccounts = 0;
        Notify("Attention");
    }

    public async Task RefreshAttention()
    {
        var session = Session;
        var gen = generation;
        var serial = ++attentionRequest;
        var api = Api;
        var ct = Token;
        if (session?.Complete != true || session.Role == "warehouse")
        {
            ClearAttention();
            return;
        }

        var requests = (await api.Send("warehouse-requests", ct: ct)).AsArray();
        if (gen != generation || serial != attentionRequest || !ReferenceEquals(api, Api))
            return;
        var accounts = session.Role == "admin" ? (await api.Send("admin/accounts", ct: ct)).AsArray() : new JsonArray();
        if (gen != generation || serial != attentionRequest || !ReferenceEquals(api, Api) || session.Id != Session?.Id)
            return;
        PendingRequests = AccountDirectory.Pending(requests);
        UnconfiguredAccounts = AccountDirectory.Unconfigured(accounts);
        Notify("Attention");
    }

    public string Error { get; private set; } = "";
    public string Notice { get; private set; } = "";
    public bool Busy => pending > 0;

    int pending;
    CancellationTokenSource readScope = new();
    long generation;
    public CancellationToken Token => readScope.Token;
    public long Generation => generation;

    public void NewScope()
    {
        generation++;
        readScope.Cancel();
        readScope.Dispose();
        readScope = new();
    }

    void Notify([CallerMemberName] string? name = null) => PropertyChanged?.Invoke(this, new(name));
    public void Message(string text)
    {
        Notice = text;
        Error = "";
        Notify(nameof(Notice));
    }

    public async Task Guard(Func<Task> work)
    {
        Interlocked.Increment(ref pending);
        Notify(nameof(Busy));
        Error = "";
        Notice = "";
        Notify(nameof(Error));
        var epoch = generation;
        try
        {
            await work();
        }
        catch (OperationCanceledException)when (epoch != generation || Token.IsCancellationRequested)
        {
        }
        catch (Exception e)
        {
            if (epoch != generation)
                return;
            if (e is ApiException { Status: 401 } && Session != null)
                ForgetSession();
            Error = e.Message + (e is TaskCanceledException ? "；写入结果请刷新核对，勿重复提交。" : "");
            Notify(nameof(Error));
        }
        finally
        {
            Interlocked.Decrement(ref pending);
            Notify(nameof(Busy));
        }
    }

    public async Task<ClientConfiguration> Probe(string address)
    {
        using var probe = new BackendClient(address);
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(6));
        var health = await probe.Send("health", ct: cts.Token);
        if (health.S("application") != "returns-recon")
            throw new InvalidDataException("该地址不是退货对账服务。");
        var config = (await probe.Send("auth/client-config", ct: cts.Token)).Decode<ClientConfiguration>();
        if (config.ApiVersion != "1.0.0" || string.IsNullOrEmpty(config.BackendId) || config.NativePasskey == null)
            throw new InvalidDataException("后端版本不支持此客户端，请更新共用 Go 服务。");
        return config;
    }

    public async Task Connect(string address, string theme)
    {
        if (Api?.ActiveWrites > 0)
            throw new InvalidOperationException("写操作正在执行，请等待结果后切换后端。");
        var config = await Probe(address);
        if (Api?.ActiveWrites > 0)
            throw new InvalidOperationException("写操作尚未完成。");
        var nextSettings = new ClientSettings(BackendClient.Normalize(address).GetLeftPart(UriPartial.Authority), theme, true);
        persist(nextSettings);
        NewScope();
        Api?.Dispose();
        Api = new BackendClient(address);
        Configuration = config;
        Settings = nextSettings;
        Session = null;
        ClearAttention();
        RecoveryCodes = [];
        Runs = [];
        RunID = "";
        CurrentJob = null;
        Message("后端连接成功，请登录。");
        SessionChanged?.Invoke();
    }

    public async Task Resume()
    {
        if (!Settings.Configured)
            return;
        await Connect(Settings.Address, Settings.Theme);
    }

    public async Task Accept(JsonNode node)
    {
        if (node["session"] is JsonNode value)
        {
            var next = value.Decode<UserSession>();
            if (Session?.Id != next.Id || Session?.Csrf != next.Csrf)
            {
                ClearAttention();
                NewScope();
                Runs = [];
                RunID = "";
            }

            Session = next;
            Api.Csrf = next.Csrf;
        }

        RecoveryCodes = node.A("recovery_codes").Select(x => x!.ToString()).ToArray();
        try
        {
            if (Session?.Complete == true)
                await Guard(LoadRuns);
        }
        finally
        {
            SessionChanged?.Invoke();
        }
    }

    public async Task Authenticate(string path, object data)
    {
        var epoch = generation;
        var api = Api;
        var result = await api.Send(path, data);
        if (epoch == generation && ReferenceEquals(api, Api))
            await Accept(result);
    }

    void ForgetSession()
    {
        NewScope();
        Api.Dispose();
        Api = new(Settings.Address);
        Session = null;
        ClearAttention();
        RecoveryCodes = [];
        Runs = [];
        RunID = "";
        CurrentJob = null;
        SessionChanged?.Invoke();
    }

    public async Task Logout()
    {
        string message = "已退出账户。";
        try
        {
            await Api.Send("auth/logout", new { });
        }
        catch (Exception)
        {
            message = "本地已退出；服务端注销未确认，原会话按服务器时限失效。";
        }
        finally
        {
            ForgetSession();
            Message(message);
        }
    }

    public async Task LoadRuns()
    {
        var epoch = generation;
        var api = Api;
        var runs = (await api.Send("runs", ct: Token)).Decode<RunInfo[]>();
        if (epoch != generation || !ReferenceEquals(api, Api))
            return;
        Runs = runs;
        if (!Runs.Any(r => r.RunId == RunID))
            RunID = Runs.FirstOrDefault()?.RunId ?? "";
    }

    public async Task Passkey(bool register, string? accountID = null, string bindingID = "default")
    {
        var cfg = Configuration ?? throw new InvalidOperationException("请先连接后端。");
        var epoch = generation;
        var api = Api;
        var ct = Token;
        bool Current() => epoch == generation && ReferenceEquals(api, Api) && !ct.IsCancellationRequested;
        var binding = bindingID == "default" ? cfg.NativePasskey : cfg.LegacyBindings.Single(x => x.Id == bindingID);
        var path = "auth/native-passkeys/" + (register ? "register" : "login");
        var begin = await api.Send(path + "/begin", new { id = accountID ?? "", binding_id = binding.Id });
        if (!Current())
            return;
        api.Csrf = begin.S("csrf");
        var credential = await NativeWebAuthn.Perform(begin["options"]!["publicKey"]!, binding, register, ct);
        if (!Current())
            return;
        var result = await api.Send(path + "/finish?challenge_id=" + Uri.EscapeDataString(begin.S("challenge_id")) + "&binding_id=" + Uri.EscapeDataString(binding.Id), credential);
        if (Current())
            await Accept(result);
    }

    public string Query(IDictionary<string, string>? filters = null, int page = 1)
    {
        var values = new Dictionary<string, string>
        {
            {
                "run_id",
                RunID
            },
            {
                "page",
                page.ToString()
            },
            {
                "page_size",
                "50"
            }
        };
        if (filters != null)
            foreach (var(k, v)in filters)
                if (v != "")
                    values[k] = v;
        return string.Join("&", values.Select(x => Uri.EscapeDataString(x.Key) + "=" + Uri.EscapeDataString(x.Value)));
    }

    public void Dispose()
    {
        NewScope();
        readScope.Dispose();
        Api?.Dispose();
    }
}

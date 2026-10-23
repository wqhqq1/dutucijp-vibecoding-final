using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json.Nodes;

namespace Reconciliation.Windows.Services;
public sealed class BackendClient : IDisposable
{
    HttpClient client;
    public Uri Address { get; }
    public string Csrf { get; set; } = "";
    public int ActiveWrites => Volatile.Read(ref writes);

    int writes;
    public BackendClient(string address)
    {
        Address = Normalize(address);
        client = new(new HttpClientHandler { CookieContainer = new(), UseCookies = true, AllowAutoRedirect = false })
        {
            BaseAddress = Address,
            Timeout = TimeSpan.FromSeconds(100)
        };
    }

    public static Uri Normalize(string value)
    {
        if (value.Contains("\\") || !System.Text.RegularExpressions.Regex.IsMatch(value.Trim(), @"\Ahttps?://[^/?#]+/?\z", System.Text.RegularExpressions.RegexOptions.IgnoreCase) || !Uri.TryCreate(value.Trim(), UriKind.Absolute, out var u) || u.Scheme is not ("http" or "https") || string.IsNullOrEmpty(u.Host) || u.UserInfo != "" || u.AbsolutePath != "/" || u.Query != "" || u.Fragment != "")
            throw new ArgumentException("请输入完整 HTTP/HTTPS 后端入口，不含路径、账号或参数。");
        return new Uri(u.GetComponents(UriComponents.SchemeAndServer, UriFormat.UriEscaped).TrimEnd('/') + "/");
    }

    public async Task<JsonNode> Send(string path, object? body = null, HttpMethod? method = null, CancellationToken ct = default)
    {
        method ??= body is null ? HttpMethod.Get : HttpMethod.Post;
        bool write = method != HttpMethod.Get && method != HttpMethod.Head;
        if (write)
            Interlocked.Increment(ref writes);
        try
        {
            using var req = new HttpRequestMessage(method, "api/" + path.TrimStart('/'));
            req.Headers.Add("Origin", Address.GetLeftPart(UriPartial.Authority));
            if (body != null)
                req.Content = new StringContent(body is JsonNode j ? j.ToJsonString() : System.Text.Json.JsonSerializer.Serialize(body, Json.Options), Encoding.UTF8, "application/json");
            if (write && Csrf != "")
                req.Headers.Add("X-CSRF-Token", Csrf);
            using var response = await client.SendAsync(req, ct);
            var text = await response.Content.ReadAsStringAsync(ct);
            JsonNode? node = null;
            try
            {
                node = JsonNode.Parse(text);
            }
            catch (System.Text.Json.JsonException)
            {
                throw new InvalidDataException($"后端返回了非 JSON 内容（HTTP {(int)response.StatusCode}），请检查地址。");
            }

            if (!response.IsSuccessStatusCode)
                throw new ApiException((int)response.StatusCode, node?.S("code") ?? "", node?.S("message")is { Length: > 0 } message ? message : $"请求未完成（HTTP {(int)response.StatusCode}）", node);
            return node ?? new JsonObject();
        }
        catch (HttpRequestException e)when (write)
        {
            throw new HttpRequestException(e.Message + "；写入结果尚未确认，请刷新核对实际结果，勿重复提交。", e);
        }
        catch (InvalidDataException e)when (write)
        {
            throw new InvalidDataException(e.Message + "；请刷新核对写入结果，勿重复提交。", e);
        }
        finally
        {
            if (write)
                Interlocked.Decrement(ref writes);
        }
    }

    public async Task<byte[]> Export(string query, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, "api/exports?" + query);
        request.Headers.Add("Origin", Address.GetLeftPart(UriPartial.Authority));
        using var response = await client.SendAsync(request, ct);
        if (!response.IsSuccessStatusCode)
        {
            JsonNode? n;
            try
            {
                n = JsonNode.Parse(await response.Content.ReadAsStringAsync(ct));
            }
            catch (System.Text.Json.JsonException)
            {
                throw new InvalidDataException($"导出失败：后端返回非 JSON 内容（HTTP {(int)response.StatusCode}），请检查服务。");
            }

            throw new ApiException((int)response.StatusCode, n.S("code"), n.S("message"), n);
        }

        return await response.Content.ReadAsByteArrayAsync(ct);
    }

    public void Dispose() => client.Dispose();
}

public sealed class ApiException(int status, string code, string message, JsonNode? payload) : Exception(message)
{
    public int Status => status;
    public string Code => code;
    public JsonNode? Payload => payload;
}

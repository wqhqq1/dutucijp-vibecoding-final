using System.Collections.Concurrent;
using System.Net;
using System.Net.Sockets;
using System.Text;

sealed record Request(string Method, string Target, Dictionary<string,string> Headers, string Body);
sealed record Reply(int Status = 200, string Body = "{}", string[]? Headers = null, bool Disconnect = false);

// Bound only to an ephemeral IPv4 loopback port. No production server/account/database involved.
sealed class TestServer : IAsyncDisposable {
    readonly TcpListener listener = new(IPAddress.Loopback, 0);
    readonly CancellationTokenSource stop = new();
    readonly ConcurrentBag<Task> clients = new();
    readonly Func<Request,Task<Reply>> handler;
    readonly Task accept;
    public ConcurrentQueue<Request> Requests { get; } = new();
    public string Address { get; }
    public TestServer(Func<Request,Task<Reply>> handler) {
        this.handler = handler; listener.Start();
        Address = "http://127.0.0.1:" + ((IPEndPoint)listener.LocalEndpoint).Port;
        accept = AcceptLoop();
    }
    async Task AcceptLoop() {
        try { while (!stop.IsCancellationRequested) {
            var connection = await listener.AcceptTcpClientAsync(stop.Token);
            clients.Add(Handle(connection));
        }} catch (OperationCanceledException) {} catch (SocketException) when (stop.IsCancellationRequested) {} catch (ObjectDisposedException) when (stop.IsCancellationRequested) {}
    }
    async Task Handle(TcpClient connection) {
        using (connection) try {
            var stream = connection.GetStream();
            using var reader = new StreamReader(stream, Encoding.UTF8, false, 4096, true);
            var first = await reader.ReadLineAsync(stop.Token);
            if (first == null) return;
            var pieces = first.Split(' ');
            var headers = new Dictionary<string,string>(StringComparer.OrdinalIgnoreCase);
            while (await reader.ReadLineAsync(stop.Token) is { Length: > 0 } line) {
                var colon = line.IndexOf(':'); headers[line[..colon]] = line[(colon+1)..].Trim();
            }
            var length = headers.TryGetValue("Content-Length", out var raw) ? int.Parse(raw) : 0;
            var chars = new char[length]; var read = 0;
            while (read < length) { var count = await reader.ReadAsync(chars.AsMemory(read), stop.Token); if (count == 0) break; read += count; }
            var request = new Request(pieces[0], pieces[1], headers, new string(chars, 0, read));
            Requests.Enqueue(request);
            var reply = await handler(request);
            if (reply.Disconnect) return;
            var bytes = Encoding.UTF8.GetBytes(reply.Body);
            var response = $"HTTP/1.1 {reply.Status} Test\r\nContent-Length: {bytes.Length}\r\nConnection: close\r\nContent-Type: application/json\r\n";
            if (reply.Headers != null) foreach (var header in reply.Headers) response += header + "\r\n";
            await stream.WriteAsync(Encoding.ASCII.GetBytes(response+"\r\n"), stop.Token);
            await stream.WriteAsync(bytes, stop.Token);
        } catch (IOException) {} catch (OperationCanceledException) {} catch (ObjectDisposedException) {}
    }
    public async ValueTask DisposeAsync() {
        stop.Cancel(); listener.Stop(); await accept;
        try { await Task.WhenAll(clients).WaitAsync(TimeSpan.FromSeconds(2)); } catch (TimeoutException) {}
        stop.Dispose();
    }
}

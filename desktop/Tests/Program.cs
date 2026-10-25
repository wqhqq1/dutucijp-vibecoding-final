using System.Net.Http;
using System.Text.Json.Nodes;
using Reconciliation.Windows;
using Reconciliation.Windows.Services;
using Reconciliation.Windows.ViewModels;

var results = new List<(string Name, bool Passed, string Detail)>();
async Task Check(string name, Func<Task> action) {
    try { await action(); results.Add((name,true,"")); Console.WriteLine("PASS " + name); }
    catch (Exception e) { results.Add((name,false,e.GetType().Name+": "+e.Message)); Console.WriteLine("FAIL " + name+": "+e.Message); }
}
void Assert(bool valid, string reason) { if (!valid) throw new Exception(reason); }
async Task<T> Throws<T>(Func<Task> action) where T:Exception {
    try { await action(); } catch(T e) { return e; } throw new Exception("Expected "+typeof(T).Name);
}
Task<Reply> Response(Reply reply) => Task.FromResult(reply);
TaskCompletionSource<bool> Gate() => new(TaskCreationOptions.RunContinuationsAsynchronously);

await Check("源时间编辑保留小数精度且改回原值保持原字符串", async () => {
    var original="2010-01-02T08:00:00.123456";
    Assert(SourceTimestamp.Edit(original,new(2010,1,2),8,0,"0.1234560")==original,"restore exact original");
    Assert(SourceTimestamp.Edit(original,new(2010,1,3),8,0,"00.123456")=="2010-01-03T08:00:00.123456","date edit precision");
    Assert(SourceTimestamp.Edit("",new(2010,1,2),8,0,"5.123456789")=="2010-01-02T08:00:05.123456789","new precision");
    foreach(var invalid in new[]{"60","-1","NaN","0.1234567890",""}) await Throws<ArgumentException>(()=>Task.FromResult(SourceTimestamp.Edit(original,new(2010,1,2),8,0,invalid)));
});

await Check("完整HTTP/HTTPS地址及端口规范化", () => {
    Assert(BackendClient.Normalize("  HTTP://localhost:8080  ").AbsoluteUri=="http://localhost:8080/", "trim/case/port");
    Assert(BackendClient.Normalize("https://example.test:8443/").Port==8443,"custom TLS port");
    Assert(BackendClient.Normalize("http://[::1]:8081").Host=="[::1]","IPv6"); return Task.CompletedTask;
});
await Check("拒绝不完整地址/协议/凭据/路径/查询/片段", async () => {
    foreach (var value in new[]{"localhost:8080", "//localhost", "ftp://localhost", "http://", "http://u:p@localhost", "http://localhost/api", "http://localhost/?x=1", "http://localhost/#fragment", "http://localhost:99999"})
        await Throws<ArgumentException>(() => Task.FromResult(BackendClient.Normalize(value)));
});
await Check("原输入的点段路径不能绕过入口校验", async () => {
    foreach(var address in new[]{"http://localhost/private/..","http://localhost/.","http://localhost/a/../",@"http://localhost\api"})
        await Throws<ArgumentException>(() => Task.FromResult(BackendClient.Normalize(address)));
});
await Check("真实HTTP保留动态Cookie并发送Origin和写CSRF", async () => {
    await using var server=new TestServer(r=>Response(r.Target=="/api/auth/login"
        ? new(Body:"{\"ok\":true}",Headers:["Set-Cookie: session_custom_93=isolated; Path=/; HttpOnly; SameSite=Lax"])
        : new()));
    using var api=new BackendClient(server.Address);
    await api.Send("auth/login",new {id="synthetic"}); api.Csrf="synthetic-csrf";
    await api.Send("cases",new{version=1}); await api.Send("runs");
    var calls=server.Requests.ToArray();
    Assert(calls.Length==3,"call count");
    Assert(calls.All(r=>r.Headers.GetValueOrDefault("Origin")==server.Address),"Origin");
    Assert(calls[1].Headers.GetValueOrDefault("Cookie")=="session_custom_93=isolated","dynamic cookie");
    Assert(calls[1].Headers.GetValueOrDefault("X-CSRF-Token")=="synthetic-csrf","write csrf");
    Assert(!calls[2].Headers.ContainsKey("X-CSRF-Token"),"GET must not carry write CSRF");
    Assert(JsonNode.Parse(calls[1].Body)!.I("version")==1,"JSON body");
});
await Check("读请求可取消且写计数归零", async () => {
    var received=Gate(); var release=Gate();
    await using var server=new TestServer(async r=>{received.TrySetResult(true); await release.Task; return new();});
    using var api=new BackendClient(server.Address); using var cts=new CancellationTokenSource();
    var pending=api.Send("slow",ct:cts.Token); await received.Task.WaitAsync(TimeSpan.FromSeconds(5));
    cts.Cancel(); await Throws<OperationCanceledException>(()=>pending); release.TrySetResult(true);
    Assert(api.ActiveWrites==0,"read/write accounting");
});
await Check("HTTP JSON错误保留状态/代码/消息/载荷", async () => {
    await using var server=new TestServer(r=>Response(new(409,"{\"code\":\"version_conflict\",\"message\":\"changed\",\"current\":{\"version\":2}}")));
    using var api=new BackendClient(server.Address);
    var error=await Throws<ApiException>(()=>api.Send("cases",new{version=1}));
    Assert(error.Status==409&&error.Code=="version_conflict"&&error.Message=="changed"&&error.Payload!["current"].I("version")==2,"error structure");
    Assert(api.ActiveWrites==0,"counter after HTTP failure");
});
await Check("非JSON成功/失败响应给出可读错误", async () => {
    await using var server=new TestServer(r=>Response(new(r.Target.EndsWith("bad")?502:200,"<html>proxy</html>")));
    using var api=new BackendClient(server.Address);
    foreach(var path in new[]{"ok","bad"}) { var error=await Throws<InvalidDataException>(()=>api.Send(path)); Assert(error.Message.Contains("非 JSON"),"readable non-JSON message"); }
});
await Check("写断线不自动重试且计数释放", async () => {
    await using var server=new TestServer(r=>Response(new(Disconnect:true)));
    using var api=new BackendClient(server.Address);
    await Throws<HttpRequestException>(()=>api.Send("write",new{id="synthetic"}));
    Assert(server.Requests.Count==1,"write was retried"); Assert(api.ActiveWrites==0,"counter after disconnect");
});
await Check("在途写计数直至响应完成", async () => {
    var received=Gate(); var release=Gate();
    await using var server=new TestServer(async r=>{received.TrySetResult(true);await release.Task;return new();});
    using var api=new BackendClient(server.Address); var pending=api.Send("write",new{});
    await received.Task.WaitAsync(TimeSpan.FromSeconds(5)); Assert(api.ActiveWrites==1,"in-flight write not counted");
    release.SetResult(true); await pending; Assert(api.ActiveWrites==0,"write did not finish");
});
await Check("新BackendClient不继承另一个实例Cookie", async () => {
    await using var server=new TestServer(r=>Response(new(Headers:["Set-Cookie: backend_identity=isolated; Path=/"])));
    using(var first=new BackendClient(server.Address)) await first.Send("first");
    using(var second=new BackendClient(server.Address)) await second.Send("second");
    Assert(!server.Requests.Last().Headers.ContainsKey("Cookie"),"cookie leak across client instances");
});
await Check("CSV导出保留Origin并正确返回字节", async () => {
    await using var server=new TestServer(r=>Response(new(Body:"id,quantity\nsynthetic,2\n")));
    using var api=new BackendClient(server.Address);var bytes=await api.Export("run_id=isolated",default);
    Assert(System.Text.Encoding.UTF8.GetString(bytes).StartsWith("id,quantity"),"CSV bytes");
    Assert(server.Requests.Single().Headers.GetValueOrDefault("Origin")==server.Address,"export Origin missing");
});
await Check("CSV导出非JSON错误给出可读错误", async () => {
    await using var server=new TestServer(r=>Response(new(502,"<html>proxy</html>")));
    using var api=new BackendClient(server.Address);await Throws<InvalidDataException>(()=>api.Export("",default));
});
await Check("重定向不自动跟随到其他服务", async () => {
    await using var destination=new TestServer(r=>Response(new()));
    await using var server=new TestServer(r=>Response(new(302,"{}",["Location: "+destination.Address+"/api/destination"])));
    using var api=new BackendClient(server.Address);var error=await Throws<ApiException>(()=>api.Send("redirect"));
    Assert(error.Status==302&&destination.Requests.IsEmpty,"redirect followed");
});
await Check("无请求体DELETE仍携带写CSRF且不重试HTTP失败", async () => {
    await using var server=new TestServer(r=>Response(new(503,"{\"code\":\"unavailable\",\"message\":\"synthetic maintenance\"}")));
    using var api=new BackendClient(server.Address){Csrf="synthetic-csrf"};
    await Throws<ApiException>(()=>api.Send("resource",method:HttpMethod.Delete));
    var request=server.Requests.Single();Assert(request.Method=="DELETE"&&request.Headers.GetValueOrDefault("X-CSRF-Token")=="synthetic-csrf"&&api.ActiveWrites==0,"DELETE semantics");
});

Reply Config(string identity="synthetic-backend") => new(Body:Json.Node(new ClientConfiguration("1.0.0",identity,new("default","localhost","http://localhost"),[])).ToJsonString());
Reply SessionReply(string id="synthetic-account") => new(Body:Json.Node(new {session=new UserSession(id,"finance",id,"all",[],"complete","business",true,false,"synthetic-csrf",true),recovery_codes=new[]{"synthetic-recovery"}}).ToJsonString(),Headers:["Set-Cookie: dynamic_backend_cookie=isolated; Path=/; HttpOnly"]);
Reply Standard(Request request,string identity="synthetic-backend") => request.Target switch {
    "/api/health"=>new(Body:"{\"application\":\"returns-recon\"}"),
    "/api/auth/client-config"=>Config(identity),
    "/api/auth/login"=>SessionReply(),
    "/api/runs"=>new(Body:Json.Node(new[]{new RunInfo("synthetic-run","synthetic-data","all","c1","r1")}).ToJsonString()),
    _=>new()
};
AppViewModel Model(Action<ClientSettings>? save=null) => new(new ClientSettings(),save??(_=>{}));

await Check("连接探测正确并只持久化规范化入口", async () => {
    await using var server=new TestServer(r=>Response(Standard(r)));
    var saved=new List<ClientSettings>(); using var vm=Model(saved.Add);
    await vm.Connect("  "+server.Address+"/  ","Dark");
    Assert(saved.Count==1&&saved[0].Address==server.Address&&saved[0].Theme=="Dark"&&saved[0].Configured,"settings persistence");
    Assert(vm.Configuration?.BackendId=="synthetic-backend"&&vm.Session==null,"configuration");
    Assert(server.Requests.Select(r=>r.Target).SequenceEqual(new[]{"/api/health","/api/auth/client-config"}),"probe sequence");
});
await Check("错误入口/不兼容配置保留连接会话与已保存设置", async () => {
    await using var good=new TestServer(r=>Response(Standard(r)));
    await using var bad=new TestServer(r=>Response(new(Body:"{\"application\":\"another-service\"}")));
    await using var incompatible=new TestServer(r=>Response(r.Target=="/api/health"?Standard(r):new(Body:"{\"api_version\":\"0.1\"}")));
    var saved=new List<ClientSettings>();using var vm=Model(saved.Add);await vm.Connect(good.Address,"Dark");await vm.Authenticate("auth/login",new{});
    var api=vm.Api;var settings=vm.Settings;var session=vm.Session;var generation=vm.Generation;
    foreach(var address in new[]{"bad-address",bad.Address,incompatible.Address}) {
        await Throws<Exception>(()=>vm.Connect(address,"Light"));
        Assert(ReferenceEquals(vm.Api,api)&&vm.Settings==settings&&vm.Session==session&&vm.Generation==generation&&saved.Count==1,"failed probe changed active connection");
    }
});
await Check("切换后端清空Cookie/CSRF/会话/恢复码/运行并失效旧读令牌", async () => {
    await using var first=new TestServer(r=>Response(Standard(r,"backend-A")));
    await using var second=new TestServer(r=>Response(Standard(r,"backend-B")));
    using var vm=Model(); await vm.Connect(first.Address,"Dark"); await vm.Authenticate("auth/login",new{});
    Assert(vm.Session?.Complete==true&&vm.Runs.Length==1&&vm.RecoveryCodes.Length==1,"login fixture");
    var oldToken=vm.Token;var oldGeneration=vm.Generation;await vm.Connect(second.Address,"Light");
    Assert(oldToken.IsCancellationRequested&&vm.Generation>oldGeneration,"read generation");
    Assert(vm.Session==null&&vm.Api.Csrf==""&&vm.RecoveryCodes.Length==0&&vm.Runs.Length==0&&vm.RunID=="","session remnants");
    await vm.Api.Send("check",new{});var request=second.Requests.Last();
    Assert(!request.Headers.ContainsKey("Cookie")&&!request.Headers.ContainsKey("X-CSRF-Token"),"credentials crossed backend boundary");
});
await Check("在途写阻止后端切换且不探测目标", async () => {
    var received=Gate();var release=Gate();
    await using var first=new TestServer(async r=>{if(r.Target=="/api/write"){received.TrySetResult(true);await release.Task;}return Standard(r);});
    await using var second=new TestServer(r=>Response(Standard(r)));
    using var vm=Model();await vm.Connect(first.Address,"Dark");var pending=vm.Api.Send("write",new{});await received.Task.WaitAsync(TimeSpan.FromSeconds(5));
    try {await Throws<InvalidOperationException>(()=>vm.Connect(second.Address,"Light"));Assert(second.Requests.IsEmpty&&vm.Settings.Address==first.Address,"blocked switch probed/changed backend");}
    finally {release.TrySetResult(true);await pending;}
});
await Check("探测期间新发起写操作仍阻止最终切换", async () => {
    var probeReceived=Gate();var probeRelease=Gate();var writeReceived=Gate();var writeRelease=Gate();
    await using var first=new TestServer(async r=>{if(r.Target=="/api/write"){writeReceived.TrySetResult(true);await writeRelease.Task;}return Standard(r);});
    await using var second=new TestServer(async r=>{if(r.Target=="/api/auth/client-config"){probeReceived.TrySetResult(true);await probeRelease.Task;}return Standard(r);});
    using var vm=Model();await vm.Connect(first.Address,"Dark");var connect=vm.Connect(second.Address,"Light");await probeReceived.Task.WaitAsync(TimeSpan.FromSeconds(5));
    var write=vm.Api.Send("write",new{});await writeReceived.Task.WaitAsync(TimeSpan.FromSeconds(5));probeRelease.TrySetResult(true);
    try {await Throws<InvalidOperationException>(()=>connect);Assert(vm.Settings.Address==first.Address,"probe/write race changed backend");}
    finally{writeRelease.TrySetResult(true);await write;}
});
await Check("旧代次Guard异常不覆盖新页面反馈且Busy释放", async () => {
    using var vm=Model();var received=Gate();var release=Gate();
    var pending=vm.Guard(async()=>{received.SetResult(true);await release.Task;throw new IOException("stale failure");});
    await received.Task;Assert(vm.Busy,"busy flag missing");vm.NewScope();vm.Message("new feedback");release.SetResult(true);await pending;
    Assert(!vm.Busy&&vm.Error==""&&vm.Notice=="new feedback","old error contaminated new generation");
});
await Check("当前代次失败释放Busy并提示写结果核查", async () => {
    using var vm=Model();await vm.Guard(()=>throw new TaskCanceledException("timeout"));
    Assert(!vm.Busy&&vm.Error.Contains("勿重复提交"),"failed action state/message");
});
await Check("登录后读取运行失败仍通知界面会话并释放Busy", async () => {
    await using var server=new TestServer(r=>Response(r.Target=="/api/runs"?new(503,"{\"code\":\"runs_unavailable\",\"message\":\"synthetic runs unavailable\"}"):Standard(r)));
    using var vm=Model();await vm.Connect(server.Address,"Dark");int notifications=0;vm.SessionChanged+=()=>notifications++;
    await vm.Guard(()=>vm.Authenticate("auth/login",new{}));
    Assert(notifications==1&&vm.Session?.Complete==true&&vm.Api.Csrf=="synthetic-csrf","new business session not announced after run-list failure");
    Assert(!vm.Busy&&vm.Error.Contains("synthetic runs unavailable")&&vm.Runs.Length==0,"failure feedback/busy/run state");
});
await Check("旧代次登录结果不恢复会话", async () => {
    var received=Gate();var release=Gate();
    await using var server=new TestServer(async r=>{if(r.Target=="/api/auth/login"){received.TrySetResult(true);await release.Task;}return Standard(r);});
    using var vm=Model();await vm.Connect(server.Address,"Dark");var login=vm.Authenticate("auth/login",new{});await received.Task.WaitAsync(TimeSpan.FromSeconds(5));vm.NewScope();release.TrySetResult(true);await login;
    Assert(vm.Session==null&&vm.Api.Csrf==""&&vm.Runs.Length==0&&vm.RecoveryCodes.Length==0,"stale authentication accepted");
});
await Check("退出后清空客户端Cookie/CSRF/会话并保持后端设置", async () => {
    await using var server=new TestServer(r=>Response(Standard(r)));
    using var vm=Model();await vm.Connect(server.Address,"Dark");await vm.Authenticate("auth/login",new{});var token=vm.Token;
    await vm.Logout();var logout=server.Requests.Last();
    Assert(logout.Target=="/api/auth/logout"&&logout.Headers.GetValueOrDefault("X-CSRF-Token")=="synthetic-csrf","logout authentication");
    Assert(vm.Session==null&&vm.Api.Csrf==""&&vm.RecoveryCodes.Length==0&&vm.Runs.Length==0&&vm.RunID==""&&token.IsCancellationRequested&&vm.Settings.Address==server.Address,"logout state");
    await vm.Api.Send("check");Assert(!server.Requests.Last().Headers.ContainsKey("Cookie"),"logout retained CookieContainer");
});
await Check("切换页面取消旧运行读取不发布旧结果", async () => {
    var received=Gate();var release=Gate();
    await using var server=new TestServer(async r=>{if(r.Target=="/api/runs"){received.TrySetResult(true);await release.Task;}return Standard(r);});
    using var vm=Model();await vm.Connect(server.Address,"Dark");var pending=vm.Guard(vm.LoadRuns);await received.Task.WaitAsync(TimeSpan.FromSeconds(5));vm.NewScope();release.TrySetResult(true);await pending;
    Assert(vm.Runs.Length==0&&vm.RunID==""&&vm.Error==""&&!vm.Busy,"stale run response/state");
});
await Check("设置保存失败保留旧API/会话/设置一致性", async () => {
    await using var first=new TestServer(r=>Response(Standard(r,"backend-A")));
    await using var second=new TestServer(r=>Response(Standard(r,"backend-B")));
    bool fail=false;using var vm=Model(_=>{if(fail)throw new IOException("synthetic storage failure");});
    await vm.Connect(first.Address,"Dark");await vm.Authenticate("auth/login",new{});var api=vm.Api;var session=vm.Session;var settings=vm.Settings;fail=true;
    await Throws<IOException>(()=>vm.Connect(second.Address,"Light"));
    Assert(ReferenceEquals(api,vm.Api)&&session==vm.Session&&settings==vm.Settings&&vm.Configuration?.BackendId=="backend-A","partial connection update after persistence failure");
    await vm.Api.Send("check");
});
Reply BeginPasskey() => new(Body:"{\"csrf\":\"synthetic-challenge-csrf\",\"challenge_id\":\"isolated/id?x=1\",\"options\":{\"publicKey\":{\"challenge\":\"synthetic-challenge\"}}}");
await Check("合成Passkey流程使用begin上下文和finish查询转义", async () => {
    NativeWebAuthn.Reset();NativeWebAuthn.Handler=(options,binding,register,ct)=> {
        Assert(options.S("challenge")=="synthetic-challenge"&&binding.Id=="default"&&!register&&!ct.IsCancellationRequested,"native context");
        return Task.FromResult<JsonNode>(new JsonObject{["id"]="synthetic-credential"});
    };
    await using var server=new TestServer(r=>Response(r.Target.EndsWith("/begin")?BeginPasskey():r.Target.Contains("/finish?")?SessionReply():Standard(r)));
    using var vm=Model();await vm.Connect(server.Address,"Dark");await vm.Passkey(false);
    var begin=server.Requests.Single(r=>r.Target.EndsWith("/begin"));var finish=server.Requests.Single(r=>r.Target.Contains("/finish?"));
    Assert(JsonNode.Parse(begin.Body)!.S("binding_id")=="default"&&JsonNode.Parse(begin.Body)!.S("id")=="","discoverable begin");
    Assert(finish.Target.Contains("challenge_id=isolated%2Fid%3Fx%3D1")&&finish.Headers.GetValueOrDefault("X-CSRF-Token")=="synthetic-challenge-csrf","finish challenge/CSRF");
    Assert(NativeWebAuthn.Calls==1&&vm.Session?.Complete==true,"synthetic flow state");NativeWebAuthn.Reset();
});
await Check("Passkey迟到begin代次失效不调用设备或发布CSRF", async () => {
    NativeWebAuthn.Reset();var received=Gate();var release=Gate();
    await using var server=new TestServer(async r=>{if(r.Target.EndsWith("/begin")){received.TrySetResult(true);await release.Task;return BeginPasskey();}return Standard(r);});
    using var vm=Model();await vm.Connect(server.Address,"Dark");var pending=vm.Passkey(false);await received.Task.WaitAsync(TimeSpan.FromSeconds(5));vm.NewScope();release.TrySetResult(true);await pending;
    Assert(NativeWebAuthn.Calls==0&&vm.Api.Csrf==""&&vm.Session==null&&!server.Requests.Any(r=>r.Target.Contains("/finish?")),"late begin advanced old flow");
});
await Check("合成设备迟到返回跨后端切换不发送finish", async () => {
    NativeWebAuthn.Reset();var received=Gate();var release=Gate();CancellationToken nativeToken=default;
    NativeWebAuthn.Handler=async(options,binding,register,ct)=>{nativeToken=ct;received.TrySetResult(true);await release.Task;return new JsonObject{["id"]="synthetic-credential"};};
    await using var first=new TestServer(r=>Response(r.Target.EndsWith("/begin")?BeginPasskey():Standard(r,"backend-A")));
    await using var second=new TestServer(r=>Response(Standard(r,"backend-B")));
    using var vm=Model();await vm.Connect(first.Address,"Dark");var pending=vm.Passkey(false);await received.Task.WaitAsync(TimeSpan.FromSeconds(5));
    await vm.Connect(second.Address,"Light");Assert(nativeToken.IsCancellationRequested,"native scope not canceled");release.TrySetResult(true);await pending;
    Assert(vm.Api.Csrf==""&&vm.Session==null&&!first.Requests.Concat(second.Requests).Any(r=>r.Target.Contains("/finish?")),"stale native result crossed backend boundary");NativeWebAuthn.Reset();
});
await Check("Passkey迟到finish保留写切换锁且代次失效不登录", async () => {
    NativeWebAuthn.Reset();NativeWebAuthn.Handler=(options,binding,register,ct)=>Task.FromResult<JsonNode>(new JsonObject{["id"]="synthetic-credential"});
    var received=Gate();var release=Gate();
    await using var first=new TestServer(async r=>{if(r.Target.Contains("/finish?")){received.TrySetResult(true);await release.Task;return SessionReply();}return r.Target.EndsWith("/begin")?BeginPasskey():Standard(r,"backend-A");});
    await using var second=new TestServer(r=>Response(Standard(r,"backend-B")));
    using var vm=Model();await vm.Connect(first.Address,"Dark");var pending=vm.Passkey(false);await received.Task.WaitAsync(TimeSpan.FromSeconds(5));
    await Throws<InvalidOperationException>(()=>vm.Connect(second.Address,"Light"));vm.NewScope();release.TrySetResult(true);await pending;
    Assert(vm.Session==null&&vm.RecoveryCodes.Length==0&&vm.Runs.Length==0,"late finish restored old session");await vm.Connect(second.Address,"Light");
    Assert(vm.Api.Csrf==""&&vm.Configuration?.BackendId=="backend-B","backend switch cleanup");NativeWebAuthn.Reset();
});

await Check("账户选择搜索角色国家/地区与待办状态计算", () => {
    var user=Json.Node(new{id="user-0042",display_name="测试仓库",role="warehouse",state="new",country_scope="selected",countries=new[]{"United Kingdom"}});
    Assert(AccountDirectory.Matches(user,"0042","warehouse","United Kingdom"),"ID/role/country");
    Assert(AccountDirectory.Matches(user,"测试仓库","",""),"display name");
    Assert(AccountDirectory.Matches(user,"KINGDOM","",""),"case-insensitive country search");
    Assert(!AccountDirectory.Matches(user,"","finance","")&&!AccountDirectory.Matches(user,"","","France"),"filter exclusions");
    Assert(AccountDirectory.Pending(Json.Node(new[]{new{status="pending"},new{status="approved"}}).AsArray())==1,"pending only");
    Assert(AccountDirectory.Unconfigured(Json.Node(new[]{new{state="new"},new{state="password"},new{state="factor"},new{state="complete"},new{state="deleted"}}).AsArray())==3,"not configured only");
    return Task.CompletedTask;
});
await Check("管理员角标计数刷新并在退出后清零", async () => {
    await using var server=new TestServer(r=>Response(r.Target=="/api/warehouse-requests"?new(Body:"[{\"status\":\"pending\"},{\"status\":\"approved\"}]"):r.Target=="/api/admin/accounts"?new(Body:"[{\"state\":\"new\"},{\"state\":\"factor\"},{\"state\":\"complete\"}]"):Standard(r)));
    using var vm=Model();await vm.Connect(server.Address,"Dark");await vm.Accept(Json.Node(new{session=new UserSession("synthetic-admin","admin","admin","all",[],"complete","business",true,false,"csrf",true)}));
    await vm.RefreshAttention();Assert(vm.PendingRequests==1&&vm.UnconfiguredAccounts==2,"counts");
    await vm.Logout();Assert(vm.PendingRequests==0&&vm.UnconfiguredAccounts==0,"logout clears");
});
await Check("旧后端迟到角标不污染新后端", async () => {
    var received=Gate();var release=Gate();
    await using var first=new TestServer(async r=>{if(r.Target=="/api/warehouse-requests"){received.TrySetResult(true);await release.Task;return new(Body:"[{\"status\":\"pending\"}]");}return Standard(r);});
    await using var second=new TestServer(r=>Response(Standard(r,"second")));
    using var vm=Model();await vm.Connect(first.Address,"Dark");await vm.Authenticate("auth/login",new{});
    var refresh=vm.RefreshAttention();await received.Task;await vm.Connect(second.Address,"Dark");release.TrySetResult(true);
    try {await refresh;}catch(OperationCanceledException){}catch(ObjectDisposedException){}
    Assert(vm.PendingRequests==0&&vm.UnconfiguredAccounts==0,"stale count published");
});
await Check("派单覆盖单国多国缺国家/地区且查询选择不受限制", () => {
    var uk=Json.Node(new{id="uk-only",role="warehouse",country_scope="selected",countries=new[]{"United Kingdom"}});
    Assert(AccountDirectory.Covers(uk,["United Kingdom"]),"own country");
    Assert(!AccountDirectory.Covers(uk,["France"])&&!AccountDirectory.Covers(uk,["United Kingdom","France"]),"must cover every selected country");
    Assert(!AccountDirectory.Covers(uk,[""])&&AccountDirectory.Covers(uk,null),"unknown country / query picker");
    uk["country_scope"]="all";Assert(AccountDirectory.Covers(uk,["France",""]),"global including unknown");
    uk["country_scope"]="selected";uk["role"]="admin";Assert(AccountDirectory.Covers(uk,["France"]),"server admin semantics");
    return Task.CompletedTask;
});

async Task VerifyCountrySelection((string Country,int Count)[] groups, Dictionary<string,string> excluded, string[] expected, int maxRequests) {
    int requests=0;
    await using var server=new TestServer(r=>{
        requests++;
        var q=r.Target.Split('?',2)[1].Split('&').Select(x=>x.Split('=',2)).ToDictionary(x=>Uri.UnescapeDataString(x[0]),x=>Uri.UnescapeDataString(x[1]));
        Assert(q["page_size"]=="1"&&q["sort"]=="country"&&q["direction"]=="asc","bounded country rank query");
        Assert(q["group_by"]=="invoice"&&q["group_key"]=="test/发票"&&q["recommended_only"]=="true","preserve selection filters");
        var visible=groups.Where(g=>!q.TryGetValue("country",out var c)||g.Country==c).ToArray();
        int offset=int.Parse(q["page"])-1;var country=visible.FirstOrDefault(g=>{if(offset<g.Count)return true;offset-=g.Count;return false;}).Country;
        object[] items=country==null?[]:[new {cancel=new {country}}];
        return Response(new(Body:Json.Node(new{total=visible.Sum(g=>g.Count),items}).ToJsonString()));
    });
    using var api=new BackendClient(server.Address);
    var actual=await AssignmentScope.ReviewCountries(api,"set/isolated",new Dictionary<string,string>{{"group_by","invoice"},{"group_key","test/发票"},{"recommended_only","true"}},excluded,CancellationToken.None);
    Assert(actual.SequenceEqual(expected),"all-page selection countries "+string.Join(',',actual));
    Assert(requests<=maxRequests,"country discovery loaded per-record pages");
}
await Check("百万条全选仅按国家/地区计数跳页不加载全量",()=>VerifyCountrySelection([("France",600000),("United Kingdom",400000)],[],["France","United Kingdom"],4));
await Check("全选排除完整国家/地区并保留未排尽缺国家/地区记录",()=>VerifyCountrySelection([("",3),("France",2),("United Kingdom",2)],new(){{"fr-1","France"},{"fr-2","France"},{"empty-1",""}},["","United Kingdom"],10));
await Check("全选排除全部缺国家/地区及法国后仅剩英国",()=>VerifyCountrySelection([("",3),("France",2),("United Kingdom",2)],new(){{"fr-1","France"},{"fr-2","France"},{"empty-1",""},{"empty-2",""},{"empty-3",""}},["United Kingdom"],10));
Console.WriteLine($"RESULT {results.Count(r=>r.Passed)}/{results.Count} passed; {results.Count(r=>!r.Passed)} failed");
Environment.ExitCode=results.Any(r=>!r.Passed)?1:0;

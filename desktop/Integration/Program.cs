using System.Diagnostics;
using System.Globalization;
using System.Security.Cryptography;
using System.Text.Json.Nodes;
using Reconciliation.Windows;
using Reconciliation.Windows.Services;
using Reconciliation.Windows.ViewModels;

// Run only against an explicitly created disposable backend. No default address.
if(args.Length != 2) throw new ArgumentException("Usage: <isolated URL> <safe result.json>");
var results=new List<object>();
using var vm=new AppViewModel(new ClientSettings(args[0]),_=>{});
await vm.Connect(args[0],"Default");
var unconfigured=await vm.Api.Send("auth/setup/accounts");
var id=unconfigured.AsArray().First(x=>x.S("role") is "admin" or "finance")!.S("id");
await vm.Authenticate("auth/setup/start",new{id});
await vm.Authenticate("auth/setup/password",new{password=Convert.ToBase64String(RandomNumberGenerator.GetBytes(32))});
var enrollment=await vm.Api.Send("auth/totp/enroll",new{});
await vm.Authenticate("auth/totp/confirm",new{code=Totp(enrollment.S("secret"))});
if(vm.RecoveryCodes.Length==0)throw new Exception("Recovery flow did not issue codes");
await vm.Authenticate("auth/setup/complete",new{saved=true});
if(vm.Session?.Complete!=true)throw new Exception("Normal setup failed");
results.Add(new{check="normal setup/password/TOTP/recovery acknowledgement",passed=true});
var run=vm.Runs.Single(x=>x.Scope=="all");vm.RunID=run.RunId;
foreach(var (name,filter,page) in new[]{("first page",new Dictionary<string,string>(),1),("invoice descending",new Dictionary<string,string>{{"sort","invoice"},{"direction","desc"}},1),("deep page",new Dictionary<string,string>(),20000),("country filter",new Dictionary<string,string>{{"country","United Kingdom"},{"sort","date"},{"direction","desc"}},1)}){
 var timer=Stopwatch.StartNew();var data=await vm.Api.Send("transactions?"+vm.Query(filter,page));timer.Stop();
 var items=data.A("items");if(items.Count>50||items.Count==0||data.I("total")<1000)throw new Exception("Paging validation failed");
 if(items.Any(x=>x?["price"] is not JsonValue v||!v.TryGetValue<string>(out _)))throw new Exception("Price must remain a string");
 if(items.Any(x=>!DateTime.TryParseExact(x.S("time"),"yyyy-MM-dd'T'HH:mm:ss.ffffff",CultureInfo.InvariantCulture,DateTimeStyles.None,out _)))throw new Exception("Source time precision changed");
 if(filter.ContainsKey("sort") && items.Zip(items.Skip(1)).Any(pair=>String.CompareOrdinal(pair.First.S(filter["sort"]=="date"?"time":filter["sort"]),pair.Second.S(filter["sort"]=="date"?"time":filter["sort"]))<0))throw new Exception("Descending order changed");
 if(filter.ContainsKey("country") && items.Any(x=>x.S("country")!=filter["country"]))throw new Exception("Country filter changed");
 var repeated=await vm.Api.Send("transactions?"+vm.Query(filter,page));
 if(!items.Select(x=>x.S("id")).SequenceEqual(repeated.A("items").Select(x=>x.S("id"))))throw new Exception("Unstable page order");
 results.Add(new{check=name,passed=true,rows=items.Count,total=data.I("total"),milliseconds=timer.Elapsed.TotalMilliseconds});
}
var overview=await vm.Api.Send("overview?"+vm.Query());
var cases=await vm.Api.Send("cases?"+vm.Query());
if(cases.A("items").Count>50)throw new Exception("Unbounded cases");
var firstCase=cases.A("items").First()!.S("id");
var evidence=await vm.Api.Send("cases/"+Uri.EscapeDataString(firstCase)+"/evidence?page_size=50");
if(evidence["case"] is null||evidence["cancel"] is null||evidence["candidates"] is null)throw new Exception("Evidence shape");
var review=await vm.Api.Send("review-sets",new{run_id=vm.RunID});
await vm.Api.Send("review-sets/"+review.S("id")+"?page=1&page_size=50");
var csv=await vm.Api.Export(vm.Query(new Dictionary<string,string>{{"customer","17850"}}),CancellationToken.None);
if(csv.Length<100)throw new Exception("CSV missing");
results.Add(new{check="overview/case evidence/recommendations/filtered CSV",passed=true,csv_bytes=csv.Length});
await vm.Logout();
var output=new{actual_time=DateTimeOffset.Now,scope="Official two-year disposable copy; production C# networking/state and real Go HTTP. Synthetic test account with normal setup. No physical authenticator or full UI acceptance.",backend_id=vm.Configuration!.BackendId,results};
File.WriteAllText(args[1],Json.Node(output).ToJsonString(new(){WriteIndented=true}));
Console.WriteLine($"PASS {results.Count} official-data C#/Go integration checks; no credentials recorded.");

static string Totp(string secret){
 var bytes=new List<byte>();int buffer=0,bits=0;foreach(char c in secret){int n="ABCDEFGHIJKLMNOPQRSTUVWXYZ234567".IndexOf(c);if(n<0)continue;buffer=(buffer<<5)|n;bits+=5;if(bits>=8){bits-=8;bytes.Add((byte)(buffer>>bits));}}
 var counter=BitConverter.GetBytes(DateTimeOffset.UtcNow.ToUnixTimeSeconds()/30);if(BitConverter.IsLittleEndian)Array.Reverse(counter);
 var hash=HMACSHA1.HashData(bytes.ToArray(),counter);int o=hash[^1]&15;int value=((hash[o]&127)<<24)|(hash[o+1]<<16)|(hash[o+2]<<8)|hash[o+3];return(value%1000000).ToString("D6",CultureInfo.InvariantCulture);
}

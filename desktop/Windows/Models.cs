using System.Text.Json;
using System.Text.Json.Nodes;

namespace Reconciliation.Windows;
public record NativeBinding(string Id, string RpId, string Origin);
public record ClientConfiguration(string ApiVersion, string BackendId, NativeBinding NativePasskey, NativeBinding[] LegacyBindings);
public record UserSession(string Id, string Role, string DisplayName, string CountryScope, string[] Countries, string State, string Scope, bool HasTotp, bool HasPasskey, string Csrf, bool CanManageDataJobs)
{
    public bool Complete => Scope == "business" && State == "complete";
    public string Name => string.IsNullOrEmpty(DisplayName) ? Id : DisplayName;
}

public record RunInfo(string RunId, string DatasetId, string Scope, string CleaningVersion, string RuleVersion)
{
    public string Label => $"{Labels.Of(Scope)} · {CleaningVersion} / {RuleVersion}";
}

public record DataPage<T>(T[] Items, int Total, int Page, int PageSize);
public record ApiProblem(string? Code, string Message, JsonObject? Current = null);
public static class Json
{
    public static readonly JsonSerializerOptions Options = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        PropertyNameCaseInsensitive = true
    };
    public static string S(this JsonNode? n, string key) => n?[key]?.ToString() ?? "";
    public static int I(this JsonNode? n, string key) => int.TryParse(n.S(key), out var v) ? v : 0;
    public static bool B(this JsonNode? n, string key) => n.S(key) == "true";
    public static JsonArray A(this JsonNode? n, string key) => n?[key] as JsonArray ?? new();
    public static T Decode<T>(this JsonNode n) => n.Deserialize<T>(Options) ?? throw new InvalidDataException("服务响应为空");
    public static JsonNode Node(object value) => JsonSerializer.SerializeToNode(value, Options)!;
}

public static class SourceTimestamp
{
    public static string Edit(string original, DateTime date, int hour, int minute, string seconds)
    {
        if (!System.Text.RegularExpressions.Regex.IsMatch(seconds, @"^\d{1,2}(\.\d{1,9})?$") || !decimal.TryParse(seconds, System.Globalization.NumberStyles.AllowDecimalPoint, System.Globalization.CultureInfo.InvariantCulture, out var value) || value < 0 || value >= 60)
            throw new ArgumentException("秒须在 0 到 60 之间，最多保留 9 位小数。");
        string result = $"{date:yyyy-MM-dd}T{hour:00}:{minute:00}:{(seconds.IndexOf('.')is var dot && dot >= 0 ? seconds[..dot].PadLeft(2, '0') + seconds[dot..] : seconds.PadLeft(2, '0'))}";
        // The service uses local source timestamps; never introduce a timezone conversion.
        if (original.Length >= 19 && DateTime.TryParseExact(original[..19], "yyyy-MM-dd'T'HH:mm:ss", System.Globalization.CultureInfo.InvariantCulture, System.Globalization.DateTimeStyles.None, out var prior) && prior.Date == date.Date && prior.Hour == hour && prior.Minute == minute && decimal.TryParse(original[17..], System.Globalization.NumberStyles.AllowDecimalPoint, System.Globalization.CultureInfo.InvariantCulture, out var oldSeconds) && oldSeconds == value)
            return original;
        return result;
    }
}

public static class AccountDirectory
{
    public static bool Covers(JsonNode account, IEnumerable<string>? countries) => countries == null || account.S("role") == "admin" || account.S("country_scope") == "all" || countries.All(c => c.Length > 0 && account.A("countries").Any(x => x?.ToString() == c));
    public static bool Matches(JsonNode account, string query, string role, string country) => (role == "" || account.S("role") == role) && (country == "" || account.S("country_scope") == "all" || account.A("countries").Any(x => x?.ToString() == country)) && (query.Trim() == "" || string.Join(" ", account.S("id"), account.S("display_name"), account.S("role"), string.Join(" ", account.A("countries").Select(x => x?.ToString()))).Contains(query.Trim(), StringComparison.OrdinalIgnoreCase));
    public static int Pending(JsonArray requests) => requests.Count(x => x.S("status") == "pending");
    public static int Unconfigured(JsonArray accounts) => accounts.Count(x => x.S("state")is "new" or "password" or "factor" or "recovering");
}

public static class Labels
{
    static readonly Dictionary<string, string> Map = new()
    {
        ["missing_identity"] = "缺客户或国家/地区",
        ["suspected_duplicate_cancel"] = "取消行疑似重复",
        ["ambiguous_candidates"] = "多个合格候选",
        ["suspected_duplicate_sale"] = "候选销售疑似重复",
        ["no_identity_match"] = "无同身份销售",
        ["no_strictly_earlier_sale"] = "无更早销售",
        ["insufficient_remaining_quantity"] = "可退余量不足",
        ["unique"] = "当前唯一",
        ["duplicate"] = "完全重复归组",
        ["basket"] = "整单关联",
        ["nearest"] = "按时间推荐",
        ["tied"] = "最近并列",
        ["matched"] = "已配对",
        ["suspected"] = "疑似",
        ["no_original"] = "无合格原单",
        ["pending"] = "待处理",
        ["in_progress"] = "处理中",
        ["closed"] = "已关闭",
        ["finance"] = "财务",
        ["warehouse"] = "仓库",
        ["admin"] = "管理员",
        ["benchmark"] = "首表一万行",
        ["year1"] = "首年度全部",
        ["all"] = "两个年度全部",
        ["new"] = "待设密码",
        ["password"] = "待绑定因素",
        ["factor"] = "待保存恢复码",
        ["complete"] = "已配置",
        ["deleted"] = "已删除",
        ["approved"] = "已批准",
        ["rejected"] = "已驳回",
        ["queued"] = "排队中",
        ["running"] = "执行中",
        ["succeeded"] = "已完成",
        ["failed"] = "失败",
        ["skipped"] = "已跳过",
        ["ready"] = "可提交",
        ["preview"] = "预览",
        ["confirm"] = "确认配对",
        ["approve"] = "确认并结案",
        ["assign"] = "派单",
        ["start"] = "开始处理",
        ["close"] = "结案",
        ["exact_invoice"] = "整单推荐",
        ["nearest"] = "最近销售",
        ["qualified"] = "合格候选"
    };
    public static string Of(string value) => Map.GetValueOrDefault(value, value);
    public static readonly string[] CaseReasons = ["missing_identity", "suspected_duplicate_cancel", "ambiguous_candidates", "suspected_duplicate_sale", "no_identity_match", "no_strictly_earlier_sale", "insufficient_remaining_quantity"];
    static readonly Dictionary<string, string> Fields = new()
    {
        ["id"] = "标识",
        ["source_id"] = "来源标识",
        ["source_row"] = "来源行号",
        ["sheet"] = "工作表",
        ["sheet_name"] = "工作表",
        ["invoice"] = "单号",
        ["customer"] = "客户编号",
        ["stock"] = "商品编号",
        ["description"] = "商品说明",
        ["country"] = "国家/地区",
        ["price"] = "精确单价",
        ["quantity"] = "数量",
        ["time"] = "原始时间",
        ["kind"] = "类型",
        ["flags"] = "数据标记",
        ["raw_json"] = "原始单元格",
        ["excluded"] = "是否排除",
        ["excluded_reason"] = "排除原因",
        ["duplicate_of"] = "重复组代表行",
        ["original_quantity"] = "原始数量",
        ["current_remaining"] = "当前可退余量",
        ["remaining"] = "余量",
        ["sale_id"] = "销售源行",
        ["cancel_id"] = "退货源行",
        ["case_id"] = "差异标识",
        ["run_id"] = "运行标识",
        ["dataset_id"] = "数据集",
        ["assignee"] = "责任人",
        ["assigned_by"] = "派单人",
        ["assigned_at"] = "派单时间",
        ["status"] = "状态",
        ["version"] = "版本",
        ["reason"] = "原因",
        ["reasons"] = "原因统计",
        ["message"] = "说明",
        ["conclusion"] = "核查结论",
        ["action"] = "操作",
        ["actor"] = "操作人",
        ["occurred_at"] = "操作时间",
        ["created_at"] = "创建时间",
        ["updated_at"] = "更新时间",
        ["generation"] = "核查轮次",
        ["before_json"] = "操作前",
        ["after_json"] = "操作后",
        ["evidence"] = "凭证与依据",
        ["changes"] = "修正内容",
        ["sale"] = "销售资料",
        ["duplicate_group"] = "重复组",
        ["document_ref"] = "凭证号",
        ["line_ref"] = "凭证明细号",
        ["source"] = "来源",
        ["initial"] = "首次配对结果",
        ["effective"] = "当前有效值",
        ["original"] = "原始值",
        ["allocation"] = "已确认分配",
        ["allocations"] = "分配明细",
        ["missing_materials"] = "缺少资料",
        ["next_step"] = "下一步",
        ["outcome"] = "处理结果",
        ["waiting"] = "是否待补充",
        ["display_name"] = "显示名",
        ["role"] = "角色",
        ["countries"] = "负责国家/地区",
        ["country_scope"] = "国家/地区范围",
        ["state"] = "配置状态",
        ["total"] = "总计",
        ["items"] = "项目",
        ["before"] = "操作前",
        ["after"] = "操作后",
        ["request_id"] = "操作请求标识"
    };
    public static string Field(string key) => Fields.GetValueOrDefault(key, key);
}

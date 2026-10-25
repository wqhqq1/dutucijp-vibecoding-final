using System.Text.Json.Nodes;

namespace Reconciliation.Windows.Services;
public static class AssignmentScope
{
    // Jump over each country using its server-side count. Only one row is read
    // per country, regardless of how many records the all-pages selection holds.
    public static async Task<string[]> ReviewCountries(BackendClient api, string setID, IDictionary<string, string> filter, IReadOnlyDictionary<string, string> excluded, CancellationToken ct)
    {
        var query = new Dictionary<string, string>(filter)
        {
            ["page_size"] = "1",
            ["sort"] = "country",
            ["direction"] = "asc"
        };
        async Task<JsonNode> Read(int page, string? country = null)
        {
            var values = new Dictionary<string, string>(query)
            {
                ["page"] = page.ToString(System.Globalization.CultureInfo.InvariantCulture)
            };
            if (country != null)
                values["country"] = country;
            string qs = string.Join("&", values.Where(x => x.Value != "").Select(x => Uri.EscapeDataString(x.Key) + "=" + Uri.EscapeDataString(x.Value)));
            return await api.Send($"review-sets/{Uri.EscapeDataString(setID)}?{qs}", ct: ct);
        }

        static string Country(JsonNode page) => (page.A("items").FirstOrDefault()?["cancel"]).S("country");
        var first = await Read(1);
        int total = first.I("total"), offset = 0;
        var result = new List<string>();
        while (offset < total)
        {
            var point = offset == 0 ? first : await Read(offset + 1);
            if (point.I("total") != total || point.A("items").Count != 1)
                throw new InvalidDataException("批量范围已变化，请刷新后重新选择。");
            string country = Country(point);
            int count;
            if (country.Length > 0)
                count = (await Read(1, country)).I("total");
            else
            {
                // Empty country is not an API equality filter. Find its sorted
                // prefix by rank, including the case where every row is empty.
                int low = offset + 1, high = total + 1;
                while (low < high)
                {
                    int mid = low + (high - low) / 2;
                    var probe = await Read(mid);
                    if (probe.I("total") != total)
                        throw new InvalidDataException("批量范围已变化，请刷新。");
                    if (Country(probe) == "")
                        low = mid + 1;
                    else
                        high = mid;
                }

                count = low - offset - 1;
            }

            if (count <= 0 || count > total - offset)
                throw new InvalidDataException("无法核对批量国家/地区范围，请刷新。");
            if (count > excluded.Values.Count(c => c == country))
                result.Add(country);
            offset += count;
        }

        return result.ToArray();
    }
}

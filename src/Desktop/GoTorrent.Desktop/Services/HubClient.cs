using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;
using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IHubClient"/> over a real <c>GoTorrent.Hub</c> instance's
/// REST API. A plain <see cref="HttpClient"/>, same reasoning as
/// <see cref="EngineClient"/> - the Hub's own server-to-node calls use a
/// Polly-resilience-wrapped client because a node might be flaky across a
/// real network, but this is a single request/response from a desktop
/// app to a Hub the user themselves configured, with nothing to retry
/// around.
/// </summary>
public sealed class HubClient : IHubClient, IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(10);

    private readonly HttpClient _http;

    public HubClient(HubOptions options)
    {
        _http = new HttpClient { BaseAddress = options.BaseAddress, Timeout = RequestTimeout };
        if (!string.IsNullOrEmpty(options.Token))
        {
            _http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", options.Token);
        }
    }

    public async Task<HubLoginResult> LoginAsync(string userName, string password, CancellationToken cancellationToken)
    {
        var body = new LoginRequest { UserName = userName, Password = password };
        using var response = await _http.PostAsJsonAsync("api/v1/auth/login", body, JsonOptions, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
        var result = await response.Content.ReadFromJsonAsync<HubLoginResult>(JsonOptions, cancellationToken);
        return result ?? throw new HubRequestException("The Hub returned an empty login response.");
    }

    public async Task<IReadOnlyList<ActivityHistoryEntry>> GetCompletedAsync(int take, CancellationToken cancellationToken)
    {
        using var response = await _http.GetAsync($"api/v1/history/completed?take={take}", cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
        var entries = await response.Content.ReadFromJsonAsync<List<ActivityHistoryEntry>>(JsonOptions, cancellationToken);
        return entries ?? [];
    }

    public async Task<ActivityHistorySummary> GetSummaryAsync(CancellationToken cancellationToken)
    {
        using var response = await _http.GetAsync("api/v1/history/summary", cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
        var summary = await response.Content.ReadFromJsonAsync<ActivityHistorySummary>(JsonOptions, cancellationToken);
        return summary ?? throw new HubRequestException("The Hub returned an empty summary response.");
    }

    private static async Task EnsureSuccessAsync(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        if (response.IsSuccessStatusCode)
        {
            return;
        }

        // Unlike gottrentd's uniform {"error": "..."} envelope, the Hub's
        // failure bodies vary by route - AuthController's lockout message
        // is a plain string, a 401 with no body at all is common for a
        // wrong password or an expired token, and ASP.NET Core's default
        // validation failures are ProblemDetails JSON. Reading as text and
        // falling back to the status line covers all three without
        // guessing a shape that might not be there.
        var text = await response.Content.ReadAsStringAsync(cancellationToken);
        var message = string.IsNullOrWhiteSpace(text)
            ? $"The Hub returned {(int)response.StatusCode} {response.ReasonPhrase}"
            : text.Trim('"', ' ', '\n', '\r');
        throw new HubRequestException(message);
    }

    private sealed class LoginRequest
    {
        public string? UserName { get; set; }
        public string? Password { get; set; }
    }

    public void Dispose() => _http.Dispose();
}

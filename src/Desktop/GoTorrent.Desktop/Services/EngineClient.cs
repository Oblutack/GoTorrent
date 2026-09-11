using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;
using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IEngineClient"/> over gottrentd's real REST API. A plain
/// <see cref="HttpClient"/>, not the Hub's Polly-wrapped one — the Hub
/// calls across a network to nodes that might be flaky; Desktop calls a
/// daemon on the same machine, where a retry/circuit-breaker pipeline
/// buys little and a DI container to host it would be more machinery
/// than this app needs yet.
/// </summary>
public sealed class EngineClient : IEngineClient, IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    private readonly HttpClient _http;

    public EngineClient(EngineOptions options)
    {
        _http = new HttpClient { BaseAddress = options.BaseAddress };
        _http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", options.Token);
    }

    public async Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken)
    {
        var torrents = await _http.GetFromJsonAsync<List<TorrentSummary>>("api/v1/torrents", JsonOptions, cancellationToken);
        return torrents ?? [];
    }

    public async Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken)
    {
        var stats = await _http.GetFromJsonAsync<SessionStats>("api/v1/session", JsonOptions, cancellationToken);
        return stats ?? throw new InvalidOperationException("gottrentd returned an empty session response.");
    }

    public void Dispose() => _http.Dispose();
}

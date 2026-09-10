using System.Net;
using System.Net.Http.Json;
using System.Text.Json;
using GoTorrent.Hub.Core.Engine;

namespace GoTorrent.Hub.Infrastructure.Engine;

/// <summary>
/// <see cref="IEngineClient"/> over gottrentd's real REST API. Registered
/// as a typed HttpClient (see <see cref="DependencyInjection.AddEngineClient"/>)
/// with its BaseAddress/Authorization header and resilience pipeline
/// (retry + circuit breaker) configured entirely at registration time —
/// this class stays unaware of both, which is the actual point of a typed
/// client: transport concerns live in DI wiring, not scattered across
/// call sites or duplicated in every method here.
/// </summary>
public sealed class EngineClient(HttpClient httpClient) : IEngineClient
{
    // gottrentd's JSON is camelCase throughout (Go's encoding/json
    // defaults, used as-is by internal/api) - Web defaults match that
    // naming policy and are case-insensitive on the way in, so this never
    // has to chase a casing mismatch as the Go side's DTOs evolve.
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    public async Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken)
    {
        var torrents = await httpClient.GetFromJsonAsync<List<TorrentSummary>>(
            "api/v1/torrents", JsonOptions, cancellationToken);
        return torrents ?? [];
    }

    public async Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken)
    {
        var stats = await httpClient.GetFromJsonAsync<SessionStats>(
            "api/v1/session", JsonOptions, cancellationToken);
        return stats ?? throw new InvalidOperationException("gottrentd returned an empty session response.");
    }

    public async Task<AddTorrentResult> AddTorrentAsync(AddTorrentRequest request, CancellationToken cancellationToken)
    {
        var response = await httpClient.PostAsJsonAsync("api/v1/torrents", request, JsonOptions, cancellationToken);

        if (response.StatusCode == HttpStatusCode.Conflict)
        {
            var body = await response.Content.ReadAsStringAsync(cancellationToken);
            throw new EngineDuplicateTorrentException(body);
        }
        response.EnsureSuccessStatusCode();

        var result = await response.Content.ReadFromJsonAsync<AddTorrentResult>(JsonOptions, cancellationToken);
        return result ?? throw new InvalidOperationException("gottrentd returned an empty add-torrent response.");
    }
}

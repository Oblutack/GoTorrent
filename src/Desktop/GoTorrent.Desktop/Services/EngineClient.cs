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

    public async Task<string> AddMagnetAsync(string magnet, string? category, string? downloadDir, CancellationToken cancellationToken)
    {
        var body = new AddRequest { Magnet = magnet, Category = category, DownloadDir = downloadDir };
        using var response = await _http.PostAsJsonAsync("api/v1/torrents", body, JsonOptions, cancellationToken);
        return await ReadInfoHashOrThrowAsync(response, cancellationToken);
    }

    public async Task<string> AddTorrentFileAsync(byte[] fileBytes, string fileName, string? category, string? downloadDir, CancellationToken cancellationToken)
    {
        using var content = new MultipartFormDataContent
        {
            { new ByteArrayContent(fileBytes), "torrent", fileName },
        };
        if (!string.IsNullOrEmpty(category))
        {
            content.Add(new StringContent(category), "category");
        }
        if (!string.IsNullOrEmpty(downloadDir))
        {
            content.Add(new StringContent(downloadDir), "downloadDir");
        }

        using var response = await _http.PostAsync("api/v1/torrents", content, cancellationToken);
        return await ReadInfoHashOrThrowAsync(response, cancellationToken);
    }

    public Task PauseAsync(string infoHash, CancellationToken cancellationToken) =>
        TorrentActionAsync(infoHash, "pause", cancellationToken);

    public Task ResumeAsync(string infoHash, CancellationToken cancellationToken) =>
        TorrentActionAsync(infoHash, "resume", cancellationToken);

    public async Task DeleteAsync(string infoHash, bool deleteData, CancellationToken cancellationToken)
    {
        var url = $"api/v1/torrents/{infoHash}";
        if (deleteData)
        {
            url += "?deleteData=true";
        }
        using var response = await _http.DeleteAsync(url, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
    }

    private async Task TorrentActionAsync(string infoHash, string action, CancellationToken cancellationToken)
    {
        using var response = await _http.PostAsync($"api/v1/torrents/{infoHash}/{action}", content: null, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
    }

    public async Task<TorrentDetail> GetTorrentDetailAsync(string infoHash, CancellationToken cancellationToken)
    {
        using var response = await _http.GetAsync($"api/v1/torrents/{infoHash}", cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
        var detail = await response.Content.ReadFromJsonAsync<TorrentDetail>(JsonOptions, cancellationToken);
        return detail ?? throw new InvalidOperationException("gottrentd returned an empty torrent detail response.");
    }

    public async Task<IReadOnlyList<FileEntry>> GetFilesAsync(string infoHash, CancellationToken cancellationToken)
    {
        var files = await _http.GetFromJsonAsync<List<FileEntry>>($"api/v1/torrents/{infoHash}/files", JsonOptions, cancellationToken);
        return files ?? [];
    }

    public async Task<IReadOnlyList<PeerEntry>> GetPeersAsync(string infoHash, CancellationToken cancellationToken)
    {
        var peers = await _http.GetFromJsonAsync<List<PeerEntry>>($"api/v1/torrents/{infoHash}/peers", JsonOptions, cancellationToken);
        return peers ?? [];
    }

    public async Task<IReadOnlyList<TrackerEntry>> GetTrackersAsync(string infoHash, CancellationToken cancellationToken)
    {
        var trackers = await _http.GetFromJsonAsync<List<TrackerEntry>>($"api/v1/torrents/{infoHash}/trackers", JsonOptions, cancellationToken);
        return trackers ?? [];
    }

    public async Task<PiecesInfo> GetPiecesAsync(string infoHash, CancellationToken cancellationToken)
    {
        var pieces = await _http.GetFromJsonAsync<PiecesInfo>($"api/v1/torrents/{infoHash}/pieces", JsonOptions, cancellationToken);
        return pieces ?? new PiecesInfo(0, 0, []);
    }

    public Task<SessionLimits> GetSessionLimitsAsync(CancellationToken cancellationToken) =>
        SetSessionLimitsAsync(downLimitKB: null, upLimitKB: null, cancellationToken);

    public async Task<SessionLimits> SetSessionLimitsAsync(long? downLimitKB, long? upLimitKB, CancellationToken cancellationToken)
    {
        var body = new PatchSessionRequest { DownLimitKB = downLimitKB, UpLimitKB = upLimitKB };
        var request = new HttpRequestMessage(HttpMethod.Patch, "api/v1/session") { Content = JsonContent.Create(body, options: JsonOptions) };
        using var response = await _http.SendAsync(request, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
        var limits = await response.Content.ReadFromJsonAsync<SessionLimits>(JsonOptions, cancellationToken);
        return limits ?? throw new InvalidOperationException("gottrentd returned an empty session-limits response.");
    }

    public async Task<TorrentSummary> PatchTorrentAsync(string infoHash, PatchTorrentOptions options, CancellationToken cancellationToken)
    {
        var body = new PatchTorrentRequest
        {
            Category = options.Category,
            Tags = options.Tags,
            QueuePosition = options.QueuePosition,
            ForceStart = options.ForceStart,
            Sequential = options.Sequential,
        };
        var request = new HttpRequestMessage(HttpMethod.Patch, $"api/v1/torrents/{infoHash}") { Content = JsonContent.Create(body, options: JsonOptions) };
        using var response = await _http.SendAsync(request, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
        var summary = await response.Content.ReadFromJsonAsync<TorrentSummary>(JsonOptions, cancellationToken);
        return summary ?? throw new InvalidOperationException("gottrentd returned an empty torrent response.");
    }

    public async Task AddTrackerAsync(string infoHash, string url, CancellationToken cancellationToken)
    {
        var body = new AddTrackerRequest { Url = url };
        using var response = await _http.PostAsJsonAsync($"api/v1/torrents/{infoHash}/trackers", body, JsonOptions, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
    }

    public async Task SetFilePriorityAsync(string infoHash, int fileIndex, string priority, CancellationToken cancellationToken)
    {
        var body = new PatchFilePriorityRequest { Priority = priority };
        var request = new HttpRequestMessage(HttpMethod.Patch, $"api/v1/torrents/{infoHash}/files/{fileIndex}") { Content = JsonContent.Create(body, options: JsonOptions) };
        using var response = await _http.SendAsync(request, cancellationToken);
        await EnsureSuccessAsync(response, cancellationToken);
    }

    private static async Task<string> ReadInfoHashOrThrowAsync(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        await EnsureSuccessAsync(response, cancellationToken);
        var added = await response.Content.ReadFromJsonAsync<AddResponse>(JsonOptions, cancellationToken);
        return added?.InfoHash ?? throw new InvalidOperationException("gottrentd returned an empty add response.");
    }

    private static async Task EnsureSuccessAsync(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        if (response.IsSuccessStatusCode)
        {
            return;
        }

        ErrorBody? error = null;
        try
        {
            error = await response.Content.ReadFromJsonAsync<ErrorBody>(JsonOptions, cancellationToken);
        }
        catch (JsonException)
        {
        }

        throw new EngineRequestException(error?.Error ?? $"gottrentd returned {(int)response.StatusCode} {response.ReasonPhrase}");
    }

    private sealed class AddRequest
    {
        public string? Magnet { get; set; }
        public string? Category { get; set; }
        public string? DownloadDir { get; set; }
    }

    private sealed record AddResponse(string InfoHash);

    private sealed record ErrorBody(string Error);

    private sealed class PatchSessionRequest
    {
        public long? DownLimitKB { get; set; }
        public long? UpLimitKB { get; set; }
    }

    private sealed class PatchTorrentRequest
    {
        public string? Category { get; set; }
        public IReadOnlyList<string>? Tags { get; set; }
        public int? QueuePosition { get; set; }
        public bool? ForceStart { get; set; }
        public bool? Sequential { get; set; }
    }

    private sealed class AddTrackerRequest
    {
        public string? Url { get; set; }
    }

    private sealed class PatchFilePriorityRequest
    {
        public string? Priority { get; set; }
    }

    public void Dispose() => _http.Dispose();
}

using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// Asks GitHub's own Releases API for this repo's latest tag - no
/// dedicated update server, since GitHub Releases already is one and
/// this project publishes there anyway (see the repo root's own
/// <c>.goreleaser.yml</c>). A plain <see cref="HttpClient"/>, not the
/// Polly-resilience-wrapped one the Hub's <c>EngineClient</c> uses - a
/// single best-effort background check on startup has much less need
/// for retry/circuit-breaker machinery than a call this app's own
/// day-to-day operation depends on.
/// </summary>
public sealed class GitHubUpdateChecker : IUpdateChecker
{
    private const string LatestReleaseUrl = "https://api.github.com/repos/Oblutack/GoTorrent/releases/latest";
    private readonly HttpClient _http;

    public GitHubUpdateChecker()
    {
        _http = new HttpClient { Timeout = TimeSpan.FromSeconds(5) };
        // GitHub's API rejects a request with no User-Agent header outright
        // (a 403, not a helpful error) - this is the one place this app's
        // own version is worth advertising for real, rather than just to
        // itself.
        _http.DefaultRequestHeaders.UserAgent.Add(new ProductInfoHeaderValue("GoTorrent.Desktop", AppVersion.Current));
    }

    public async Task<string?> GetLatestVersionTagAsync(CancellationToken cancellationToken)
    {
        try
        {
            using var response = await _http.GetAsync(LatestReleaseUrl, cancellationToken);
            if (!response.IsSuccessStatusCode)
            {
                return null;
            }
            using var stream = await response.Content.ReadAsStreamAsync(cancellationToken);
            using var doc = await JsonDocument.ParseAsync(stream, cancellationToken: cancellationToken);
            return doc.RootElement.TryGetProperty("tag_name", out var tag) ? tag.GetString() : null;
        }
        catch
        {
            // Offline, GitHub unreachable, a malformed response - none of
            // this is worth surfacing to the user for a background,
            // advisory-only check; see the interface's own doc comment.
            return null;
        }
    }
}

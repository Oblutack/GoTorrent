using GoTorrent.Hub.Core.Engine;
using Microsoft.Extensions.Logging;

namespace GoTorrent.Hub.Core.Rss;

/// <summary>
/// Runs one full pass over every enabled rule: fetch its feed, find items
/// that match and haven't been processed yet, add each to the engine, and
/// record it as processed regardless of whether the add succeeded or hit
/// a duplicate — either way there's no reason to try that same item
/// again. A transient failure (the feed is briefly unreachable, the
/// engine is briefly unreachable) is logged and this moves on to the next
/// item/rule rather than aborting the whole cycle — one bad rule or one
/// bad poll must never starve every other rule of its own chance to run.
/// </summary>
/// <remarks>
/// Deliberately its own class, independent of however it ends up being
/// scheduled (a <c>BackgroundService</c> on a timer, in this project's
/// case) — the actual "what does one poll cycle do" logic is exactly what
/// a unit test should exercise directly, against fakes for all four
/// dependencies, without needing a real timer, a real database, or a real
/// feed anywhere near the test.
/// </remarks>
public sealed class RssPollCycleRunner(
    IRssRuleRepository rules,
    IRssFeedReader feedReader,
    IProcessedFeedItemStore processedStore,
    IEngineClient engineClient,
    ILogger<RssPollCycleRunner> logger)
{
    public async Task RunAsync(CancellationToken cancellationToken)
    {
        var enabledRules = (await rules.GetAllAsync(cancellationToken)).Where(r => r.Enabled);
        foreach (var rule in enabledRules)
        {
            cancellationToken.ThrowIfCancellationRequested();
            await ProcessRuleAsync(rule, cancellationToken);
        }
    }

    private async Task ProcessRuleAsync(RssRule rule, CancellationToken cancellationToken)
    {
        IReadOnlyList<FeedItem> items;
        try
        {
            items = await feedReader.ReadAsync(rule.FeedUrl, cancellationToken);
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogWarning(ex, "RSS rule {RuleName}: could not read feed {FeedUrl}", rule.Name, rule.FeedUrl);
            return;
        }

        foreach (var item in items)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (!RssRuleMatcher.Matches(rule, item))
            {
                continue;
            }
            if (await processedStore.IsProcessedAsync(rule.Id, item.Key, cancellationToken))
            {
                continue;
            }
            await TryAddAsync(rule, item, cancellationToken);
        }
    }

    private async Task TryAddAsync(RssRule rule, FeedItem item, CancellationToken cancellationToken)
    {
        try
        {
            var isMagnet = item.Link?.StartsWith("magnet:", StringComparison.OrdinalIgnoreCase) == true;
            var request = new AddTorrentRequest(
                Magnet: isMagnet ? item.Link : null,
                Url: isMagnet ? null : item.Link,
                Category: rule.Category,
                Tags: null,
                DownloadDir: rule.DownloadDir);
            await engineClient.AddTorrentAsync(request, cancellationToken);
            logger.LogInformation("RSS rule {RuleName}: added {Title}", rule.Name, item.Title);
        }
        catch (EngineDuplicateTorrentException)
        {
            // Already managed by the engine - not a failure, just means a
            // previous cycle (or something else entirely) got there
            // first. Still worth marking processed below so this rule
            // doesn't keep re-attempting it forever.
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogWarning(ex, "RSS rule {RuleName}: failed to add {Title}", rule.Name, item.Title);
            return; // Not marked processed - worth retrying on the next poll.
        }
        await processedStore.MarkProcessedAsync(rule.Id, item.Key, cancellationToken);
    }
}

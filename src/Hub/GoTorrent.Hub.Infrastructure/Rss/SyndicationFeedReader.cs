using System.ServiceModel.Syndication;
using System.Xml;
using GoTorrent.Hub.Core.Rss;

namespace GoTorrent.Hub.Infrastructure.Rss;

/// <summary>
/// <see cref="IRssFeedReader"/> over a real RSS/Atom feed, via the
/// standard (if legacy) <c>System.ServiceModel.Syndication</c> — still the
/// maintained, first-party way to parse either format in .NET without
/// hand-rolling one more XML dialect this project doesn't actually need
/// full control over (unlike bencode/KRPC/the BitTorrent wire protocol on
/// the Go side, RSS is not something this project has any reason to
/// implement from spec itself).
/// </summary>
public sealed class SyndicationFeedReader(HttpClient httpClient) : IRssFeedReader
{
    public async Task<IReadOnlyList<FeedItem>> ReadAsync(string feedUrl, CancellationToken cancellationToken)
    {
        await using var stream = await httpClient.GetStreamAsync(feedUrl, cancellationToken);

        // DtdProcessing.Prohibit + a null XmlResolver: a feed URL is
        // inherently untrusted input (it's whatever a rule's author
        // typed in), so this must not fetch external DTD subsets/entities
        // on the parser's own initiative - the classic XXE class of bug.
        var settings = new XmlReaderSettings { DtdProcessing = DtdProcessing.Prohibit, XmlResolver = null };
        using var xmlReader = XmlReader.Create(stream, settings);
        var feed = SyndicationFeed.Load(xmlReader);

        return feed.Items.Select(item => new FeedItem(
            Title: item.Title?.Text ?? string.Empty,
            Link: item.Links.FirstOrDefault()?.Uri?.ToString(),
            Guid: item.Id,
            PublishedAt: item.PublishDate == default ? null : item.PublishDate)).ToList();
    }
}

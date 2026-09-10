using Microsoft.EntityFrameworkCore.Storage.ValueConversion;

namespace GoTorrent.Hub.Infrastructure.Persistence;

/// <summary>
/// Stores a <see cref="DateTimeOffset"/> as its UTC <see cref="long"/>
/// tick count instead of SQLite's default TEXT representation.
/// </summary>
/// <remarks>
/// A real, necessary fix, not a style preference: EF Core's SQLite
/// provider cannot translate a <c>WHERE</c>/<c>ORDER BY</c> comparison
/// against a TEXT-mapped <see cref="DateTimeOffset"/> column at all —
/// <c>SessionSnapshotRepository.GetSinceAsync</c>/<c>PruneOlderThanAsync</c>
/// and <c>TorrentHistoryRepository.GetRecentAsync</c> threw
/// <see cref="InvalidOperationException"/>/<see cref="NotSupportedException"/>
/// at query time until this converter was added — caught by their own
/// tests, not found by inspection. A tick count is a plain integer SQLite
/// compares and sorts natively, with no such gap.
/// </remarks>
public sealed class DateTimeOffsetToTicksConverter()
    : ValueConverter<DateTimeOffset, long>(dt => dt.UtcTicks, ticks => new DateTimeOffset(ticks, TimeSpan.Zero));

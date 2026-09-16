using System.Globalization;
using Avalonia.Data.Converters;

namespace GoTorrent.Desktop.Converters;

/// <summary>
/// Formats a KiB/s <c>double</c> (<c>TorrentRowViewModel.DownloadRateKBps</c>/
/// <c>UploadRateKBps</c>) as "X.X KiB/s" using
/// <see cref="CultureInfo.InvariantCulture"/> explicitly - a plain XAML
/// <c>StringFormat='{}{0:F1}'</c> binding is locale-sensitive (renders
/// "0,5" instead of "0.5" on a comma-decimal machine, a real bug already
/// caught and fixed once in <c>SessionRatioDisplay</c> and left as a
/// known, flagged issue on the pre-existing per-torrent Ratio column and
/// the Peers tab's own Down/Up columns) - new numeric display code in
/// this project uses an explicit invariant conversion instead of
/// <c>StringFormat</c>, not a second copy of the same bug.
/// </summary>
public sealed class KBpsDisplayConverter : IValueConverter
{
    public static readonly KBpsDisplayConverter Instance = new();

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        value is double kbps ? FormattableString.Invariant($"{kbps:F1} KiB/s") : value?.ToString();

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        throw new NotSupportedException();
}

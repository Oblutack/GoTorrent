using System.Collections;
using System.Globalization;
using System.Linq;
using Avalonia.Data.Converters;

namespace GoTorrent.Desktop.Converters;

/// <summary>
/// Joins a string collection (<c>TorrentRowViewModel.Tags</c>,
/// specifically - the "More columns" Stage 4 item's Tags column) with
/// ", " for display - the default <c>ToString()</c> a plain binding
/// would otherwise show for a list is its runtime type name, not its
/// contents.
/// </summary>
public sealed class StringListJoinConverter : IValueConverter
{
    public static readonly StringListJoinConverter Instance = new();

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        value is IEnumerable enumerable and not string
            ? string.Join(", ", enumerable.Cast<object>())
            : value?.ToString();

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        throw new NotSupportedException();
}

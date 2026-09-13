using System.Collections;
using System.Globalization;
using Avalonia.Data.Converters;

namespace GoTorrent.Desktop.Converters;

/// <summary>
/// True when the bound <see cref="ICollection"/> has zero items (or is
/// null) - Stage 3's empty-state overlays (empty torrent list, empty
/// search result, empty Files/Peers/Trackers tabs) are plain
/// <c>TextBlock</c>s laid under the real grid in the same cell, shown
/// only through this. Pass <c>ConverterParameter=Invert</c> for the
/// opposite ("has at least one item") when the same source collection
/// needs to hide something instead.
/// </summary>
public sealed class CollectionEmptyConverter : IValueConverter
{
    public static readonly CollectionEmptyConverter Instance = new();

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture)
    {
        var isEmpty = value is not ICollection { Count: > 0 };
        var invert = string.Equals(parameter as string, "Invert", StringComparison.OrdinalIgnoreCase);
        return invert ? !isEmpty : isEmpty;
    }

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        throw new NotSupportedException();
}

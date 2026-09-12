using System.Globalization;
using Avalonia.Data.Converters;
using Avalonia.Media;

namespace GoTorrent.Desktop.Converters;

/// <summary>
/// Maps a <c>torrent.State</c> string (see <c>TorrentSummary.State</c> -
/// a plain string, not an enum, since gottrentd can add a new state
/// before this client knows about it) to a color, for the torrent list's
/// state badges. An unrecognized state - the exact "Go side added a new
/// one" case - falls back to a neutral gray rather than throwing.
/// </summary>
public sealed class StateToBrushConverter : IValueConverter
{
    public static readonly StateToBrushConverter Instance = new();

    private static readonly IBrush Seeding = new SolidColorBrush(Color.Parse("#3DD68C"));
    private static readonly IBrush Downloading = new SolidColorBrush(Color.Parse("#4EA1F3"));
    private static readonly IBrush Paused = new SolidColorBrush(Color.Parse("#8B8FA3"));
    private static readonly IBrush Checking = new SolidColorBrush(Color.Parse("#C792EA"));
    private static readonly IBrush FetchingMetadata = new SolidColorBrush(Color.Parse("#F5A623"));
    private static readonly IBrush Error = new SolidColorBrush(Color.Parse("#F44747"));
    private static readonly IBrush Default = new SolidColorBrush(Color.Parse("#6B7080"));

    public object? Convert(object? value, Type targetType, object? parameter, CultureInfo culture) => (value as string) switch
    {
        "Seeding" => Seeding,
        "Downloading" => Downloading,
        "Paused" => Paused,
        "CheckingFiles" => Checking,
        "FetchingMetadata" => FetchingMetadata,
        "Error" => Error,
        _ => Default,
    };

    public object? ConvertBack(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        throw new NotSupportedException();
}

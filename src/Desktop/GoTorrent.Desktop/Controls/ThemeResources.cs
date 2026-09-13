using Avalonia;
using Avalonia.Media;
using Avalonia.Styling;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// Looks up a themed brush/colour from App.axaml's resource dictionary by
/// key - the seam hand-drawn <see cref="Control"/> subclasses
/// (<see cref="PieceMapControl"/>, <see cref="SpeedGraphControl"/>) use to
/// consume the same design tokens XAML-bound elements get via
/// <c>{StaticResource ...}</c>, since a <c>Render</c> override has no
/// equivalent binding syntax available to it. Falls back to the given
/// colour rather than throwing if a key is ever missing or the app
/// resources aren't available yet (e.g. a control instantiated outside a
/// running <c>Application</c>, as nothing in this project's tests do
/// today, but nothing here should crash if that ever changes).
/// </summary>
internal static class ThemeResources
{
    public static IBrush Brush(string key, IBrush fallback) =>
        Application.Current?.TryGetResource(key, ThemeVariant.Default, out var value) == true && value is IBrush brush
            ? brush
            : fallback;

    public static Color Color(string key, Color fallback) =>
        Application.Current?.TryGetResource(key, ThemeVariant.Default, out var value) == true && value is ISolidColorBrush brush
            ? brush.Color
            : fallback;
}

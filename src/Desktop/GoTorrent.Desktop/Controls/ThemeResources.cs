using Avalonia;
using Avalonia.Media;
using Avalonia.Styling;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// Looks up a themed brush/colour from App.axaml's resource dictionary by
/// key - the seam hand-drawn <see cref="Control"/> subclasses
/// (<see cref="PieceMapControl"/>, <see cref="SpeedGraphControl"/>) use to
/// consume the same design tokens XAML-bound elements get via
/// <c>{DynamicResource ...}</c>, since a <c>Render</c> override has no
/// equivalent binding syntax available to it. Falls back to the given
/// colour rather than throwing if a key is ever missing or the app
/// resources aren't available yet (e.g. a control instantiated outside a
/// running <c>Application</c>, as nothing in this project's tests do
/// today, but nothing here should crash if that ever changes).
/// </summary>
/// <remarks>
/// A real, confirmed-live bug once Stage 3's light theme added actual
/// per-variant <c>ThemeDictionaries</c> entries for these keys (before
/// that, <c>GtXxx</c> resources had only one value regardless of theme,
/// so this bug was invisible): passing <see cref="ThemeVariant.Default"/>
/// to <c>Application.Current.TryGetResource</c> does <b>not</b> resolve
/// through <c>Application.Current.ActualThemeVariant</c> the way a
/// <c>{DynamicResource}</c> binding on a real <c>StyledElement</c> does -
/// it silently misses the themed dictionary and falls through to this
/// method's own hardcoded fallback colour every time, regardless of
/// which theme is actually active. Caught by comparing a live screenshot
/// of the piece map in light mode against dark mode and finding the
/// "missing piece" shade identical in both - passing
/// <c>Application.Current.ActualThemeVariant</c> explicitly instead is
/// what actually reaches the right dictionary.
/// </remarks>
internal static class ThemeResources
{
    public static IBrush Brush(string key, IBrush fallback) =>
        Application.Current is { } app && app.TryGetResource(key, app.ActualThemeVariant, out var value) && value is IBrush brush
            ? brush
            : fallback;

    public static Color Color(string key, Color fallback) =>
        Application.Current is { } app && app.TryGetResource(key, app.ActualThemeVariant, out var value) && value is ISolidColorBrush brush
            ? brush.Color
            : fallback;
}

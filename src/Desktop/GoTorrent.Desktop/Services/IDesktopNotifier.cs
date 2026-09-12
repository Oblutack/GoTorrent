namespace GoTorrent.Desktop.Services;

/// <summary>
/// A real OS notification - deliberately not Avalonia's own
/// <c>WindowNotificationManager</c>, which is an in-window overlay only
/// (confirmed by inspecting it while building 6.3's tray-icon slice), not
/// a real notification a user would see with the app minimized to tray or
/// not focused at all - the exact case "notify on completion" exists for.
/// </summary>
public interface IDesktopNotifier
{
    /// <summary>
    /// Wires this notifier to a real native window handle - called once,
    /// from <c>App.axaml.cs</c>, right after the main window exists. Every
    /// <see cref="Notify"/> call before this is a silent no-op.
    /// </summary>
    void Attach(IntPtr ownerWindowHandle);

    /// <summary>Shows a real OS notification with the given title and body text.</summary>
    void Notify(string title, string message);
}

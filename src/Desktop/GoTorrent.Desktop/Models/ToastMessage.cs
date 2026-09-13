namespace GoTorrent.Desktop.Models;

/// <summary>
/// One toast notification - <c>MainViewModel.ToastRequested</c> raises
/// these, <c>MainWindow</c> renders them through Avalonia's
/// <c>WindowNotificationManager</c> (an in-window overlay, not a real OS
/// toast - see <c>Services/IDesktopNotifier</c> for the one genuine OS
/// notification this app sends, on torrent completion). This is Stage
/// 3's replacement for the single <c>ConnectionError</c> text block
/// doing double duty as both "we've lost connection to gottrentd" (a
/// persistent state, still shown as its own banner) and "this one action
/// just failed" (inherently transient) - only the latter goes through
/// toasts now.
/// </summary>
/// <param name="ActionLabel">
/// When set, alongside <see cref="Action"/>, the toast renders a
/// clickable action (e.g. "Undo") - used by the delete-with-undo flow.
/// Null for a plain informational/error toast.
/// </param>
public sealed record ToastMessage(string Text, ToastSeverity Severity, string? ActionLabel = null, Action? Action = null);

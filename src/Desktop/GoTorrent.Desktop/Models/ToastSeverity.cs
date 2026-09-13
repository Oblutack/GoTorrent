namespace GoTorrent.Desktop.Models;

/// <summary>
/// How <see cref="ToastMessage"/> should render - a small ViewModel-owned
/// enum rather than binding <c>MainViewModel</c> directly to Avalonia's own
/// <c>NotificationType</c>, so the ViewModel stays UI-framework-agnostic
/// the way the rest of this codebase already keeps it (no Avalonia types
/// anywhere in <c>ViewModels</c>/<c>Models</c> until now). <c>MainWindow</c>
/// maps this to <c>NotificationType</c> when actually showing one.
/// </summary>
public enum ToastSeverity
{
    Info,
    Success,
    Warning,
    Error,
}

using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>An in-memory <see cref="IDesktopNotifier"/> - no real OS notification shown in tests.</summary>
public sealed class FakeDesktopNotifier : IDesktopNotifier
{
    public List<(string Title, string Message)> Notifications { get; } = [];

    public void Attach(IntPtr ownerWindowHandle)
    {
    }

    public void Notify(string title, string message) => Notifications.Add((title, message));
}

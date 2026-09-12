namespace GoTorrent.Desktop.Services;

/// <summary>Whether this app is registered to launch when the user logs in.</summary>
public interface IAutostartService
{
    bool IsEnabled();

    void SetEnabled(bool enabled);
}

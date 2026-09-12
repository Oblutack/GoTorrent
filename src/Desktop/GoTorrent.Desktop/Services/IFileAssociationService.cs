namespace GoTorrent.Desktop.Services;

/// <summary>Whether this app is registered to open .torrent files and magnet: links.</summary>
public interface IFileAssociationService
{
    bool IsRegistered();

    void SetRegistered(bool enabled);
}

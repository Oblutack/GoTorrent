namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// The result of a successful add, mirroring gottrentd's own
/// <c>internal/api.AddResponse</c> (<c>{"infoHash": "..."}</c>).
/// </summary>
public sealed record AddTorrentResult(string InfoHash);

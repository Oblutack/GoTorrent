using GoTorrent.Hub.Core.Engine;

namespace GoTorrent.Hub.Core.Nodes;

/// <summary>One node's torrent, tagged with which node it came from.</summary>
public sealed record AggregatedTorrentSummary(Guid NodeId, string NodeName, TorrentSummary Torrent);

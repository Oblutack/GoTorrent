using System;
using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Media;
using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// Renders the selected torrent's connected peers (<see cref="Peers"/>,
/// the same live <c>PeerRow</c> collection the Peers tab's table already
/// shows) as a live graph rather than rows in a table - ROADMAP.md's own
/// framing for this item. One peer per spoke around a centre "you" node,
/// encoding the three things asked for without needing a legend, by
/// reusing colour conventions this app already established elsewhere:
///
/// <list type="bullet">
/// <item><b>Contribution</b> - each spoke is drawn as two segments (centre
/// to midpoint, midpoint to peer), one per direction, thickness scaled by
/// that direction's live KiB/s rate (the same numbers the Peers tab table
/// and the General tab's fleet speed graph already compute/show).</item>
/// <item><b>Choke state</b> - the download half is <c>GtDownload</c> blue
/// when the peer isn't choking us, or dimmed to <c>GtBorder</c> when it
/// is; the upload half is <c>GtUpload</c> orange/dimmed the same way for
/// whether we're choking them - the exact download/upload colour pairing
/// <see cref="SparklineControl"/>/<see cref="SpeedGraphControl"/> already
/// use, so "blue half missing" reads as "not downloading from this peer"
/// without inventing a new colour language.</item>
/// <item><b>Progress</b> - the peer's own node is shaded between
/// <c>GtBorder</c> (0%) and <c>GtSuccess</c> (100%) by
/// <see cref="PeerRow.Progress"/>, the identical lerp
/// <see cref="PieceMapControl"/> already uses for "how complete" -
/// reused rather than inventing a second one.</item>
/// </list>
///
/// A dashed spoke (vs. solid) marks an inbound connection
/// (<see cref="PeerRow.Outbound"/> false) - a peer that connected to us,
/// rather than one we dialed - since that distinction is already tracked
/// and otherwise invisible in this view.
/// </summary>
public sealed class SwarmControl : Control
{
    public static readonly StyledProperty<ObservableCollection<PeerRow>?> PeersProperty =
        AvaloniaProperty.Register<SwarmControl, ObservableCollection<PeerRow>?>(nameof(Peers));

    public ObservableCollection<PeerRow>? Peers
    {
        get => GetValue(PeersProperty);
        set => SetValue(PeersProperty, value);
    }

    private const double MinNodeRadius = 6;
    private const double MaxNodeRadiusBoost = 7;
    private const double CentreNodeRadius = 9;
    private const double MinLineThickness = 1;
    private const double MaxLineThickness = 4;

    protected override void OnPropertyChanged(AvaloniaPropertyChangedEventArgs change)
    {
        base.OnPropertyChanged(change);
        if (change.Property != PeersProperty)
        {
            return;
        }
        if (change.OldValue is ObservableCollection<PeerRow> oldPeers)
        {
            oldPeers.CollectionChanged -= OnPeersCollectionChanged;
        }
        if (change.NewValue is ObservableCollection<PeerRow> newPeers)
        {
            newPeers.CollectionChanged += OnPeersCollectionChanged;
        }
        InvalidateVisual();
    }

    private void OnPeersCollectionChanged(object? sender, NotifyCollectionChangedEventArgs e) => InvalidateVisual();

    public override void Render(DrawingContext context)
    {
        base.Render(context);
        var peers = Peers;
        var count = peers?.Count ?? 0;
        if (Bounds.Width <= 0 || Bounds.Height <= 0)
        {
            return;
        }

        var centre = new Point(Bounds.Width / 2, Bounds.Height / 2);
        var download = ThemeResources.Color("GtDownload", Colors.DodgerBlue);
        var upload = ThemeResources.Color("GtUpload", Colors.OrangeRed);
        var missing = ThemeResources.Color("GtBorder", Color.FromRgb(64, 64, 64));
        var have = ThemeResources.Color("GtSuccess", Color.FromRgb(60, 179, 113));
        var centreColor = ThemeResources.Color("GtAccent", Colors.CornflowerBlue);

        if (count == 0)
        {
            return;
        }

        context.DrawEllipse(new SolidColorBrush(centreColor), null, centre, CentreNodeRadius, CentreNodeRadius);

        // Peers around the ring get smaller as the swarm grows, so a
        // torrent with dozens of connections still fits legibly - the
        // same "scale to what the control can actually show" reasoning
        // PieceMapControl already applies for a large piece count.
        var ringRadius = (Math.Min(Bounds.Width, Bounds.Height) / 2) - MinNodeRadius - MaxNodeRadiusBoost - 4;
        if (ringRadius < 10)
        {
            return;
        }
        var maxNodeRadius = count > 12 ? Math.Max(MinNodeRadius, MinNodeRadius + (MaxNodeRadiusBoost * 12 / count)) : MinNodeRadius + MaxNodeRadiusBoost;

        var maxRate = 0.0;
        foreach (var p in peers!)
        {
            maxRate = Math.Max(maxRate, Math.Max(p.DownloadRateKBps, p.UploadRateKBps));
        }

        var i = 0;
        foreach (var peer in peers!)
        {
            var angle = (2 * Math.PI * i / count) - (Math.PI / 2);
            var peerPoint = new Point(centre.X + (ringRadius * Math.Cos(angle)), centre.Y + (ringRadius * Math.Sin(angle)));
            var midPoint = new Point((centre.X + peerPoint.X) / 2, (centre.Y + peerPoint.Y) / 2);

            var downloadThickness = LineThickness(peer.DownloadRateKBps, maxRate);
            var uploadThickness = LineThickness(peer.UploadRateKBps, maxRate);
            var downloadColor = peer.PeerChoking ? Dim(download, missing) : download;
            var uploadColor = peer.AmChoking ? Dim(upload, missing) : upload;
            var dashStyle = peer.Outbound ? null : new DashStyle([3, 3], 0);

            context.DrawLine(new Pen(new SolidColorBrush(downloadColor), downloadThickness, dashStyle), centre, midPoint);
            context.DrawLine(new Pen(new SolidColorBrush(uploadColor), uploadThickness, dashStyle), midPoint, peerPoint);

            var nodeRadius = MinNodeRadius + ((maxRate <= 0 ? 0 : (Math.Max(peer.DownloadRateKBps, peer.UploadRateKBps) / maxRate)) * (maxNodeRadius - MinNodeRadius));
            var fraction = Math.Clamp(peer.Progress, 0, 1);
            var nodeColor = new Color(
                255,
                (byte)(missing.R + ((have.R - missing.R) * fraction)),
                (byte)(missing.G + ((have.G - missing.G) * fraction)),
                (byte)(missing.B + ((have.B - missing.B) * fraction)));
            context.DrawEllipse(new SolidColorBrush(nodeColor), null, peerPoint, nodeRadius, nodeRadius);

            i++;
        }
    }

    private static double LineThickness(double rateKBps, double maxRate) =>
        maxRate <= 0 ? MinLineThickness : MinLineThickness + ((rateKBps / maxRate) * (MaxLineThickness - MinLineThickness));

    /// <summary>Blends a direction's colour 60% toward the missing/border
    /// colour when that direction is choked - dimmed, not hidden, so a
    /// choked peer's spoke is still visible as "connected, just idle."</summary>
    private static Color Dim(Color color, Color towards) => new(
        255,
        (byte)(color.R + ((towards.R - color.R) * 0.6)),
        (byte)(color.G + ((towards.G - color.G) * 0.6)),
        (byte)(color.B + ((towards.B - color.B) * 0.6)));
}

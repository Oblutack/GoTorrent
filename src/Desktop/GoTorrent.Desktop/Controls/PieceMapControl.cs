using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Media;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// Renders <see cref="Pieces"/> (one bool per piece - have/missing) as a
/// grid of coloured cells - hand-drawn rather than one <c>Control</c> per
/// piece: a real torrent can have tens of thousands of pieces, and this
/// is the "the single most satisfying thing to watch and almost no
/// client shows it well" feature ROADMAP.md calls out for 6.2. There is
/// no third "in-flight" state, unlike some clients' piece maps - gottrentd's
/// real API only ever reports have/not-have (<c>GET .../pieces</c>'s
/// bitfield, kept live by <c>pieceVerified</c> WS events), with no signal
/// anywhere for which pieces are currently mid-download.
///
/// <para>
/// <see cref="PieceOwners"/> (optional, parallel to <see cref="Pieces"/>)
/// adds peer attribution on top: a have piece whose owner is known is
/// shaded by a colour derived deterministically from that peer's address
/// (<see cref="ColorForOwner"/>) rather than the plain default "have"
/// colour. This is necessarily incomplete, and honestly so - gottrentd's
/// <c>pieceVerified</c> WS event only carries an owner going forward, so a
/// piece verified before the app connected (already-downloaded content, or
/// simply not yet observed this session) has an unknown owner and falls
/// back to the default colour, same as if attribution were never wired up
/// at all.
/// </para>
/// </summary>
public sealed class PieceMapControl : Control
{
    public static readonly StyledProperty<ObservableCollection<bool>?> PiecesProperty =
        AvaloniaProperty.Register<PieceMapControl, ObservableCollection<bool>?>(nameof(Pieces));

    public ObservableCollection<bool>? Pieces
    {
        get => GetValue(PiecesProperty);
        set => SetValue(PiecesProperty, value);
    }

    /// <summary>
    /// One entry per piece, parallel to <see cref="Pieces"/> - the peer
    /// address that delivered it, or null/empty when unknown (see the
    /// class doc comment). Optional: a null-bound collection just means
    /// every have piece uses the plain default colour, unchanged from
    /// before this property existed.
    /// </summary>
    public static readonly StyledProperty<ObservableCollection<string?>?> PieceOwnersProperty =
        AvaloniaProperty.Register<PieceMapControl, ObservableCollection<string?>?>(nameof(PieceOwners));

    public ObservableCollection<string?>? PieceOwners
    {
        get => GetValue(PieceOwnersProperty);
        set => SetValue(PieceOwnersProperty, value);
    }

    private const double MinCellSize = 3;
    private const double MaxCellSize = 14;
    private const double Gap = 1;

    /// <summary>Sentinel dictionary key for "have, but owner unknown."</summary>
    private const string UnknownOwner = "";

    /// <summary>
    /// Completion is bucketed into 11 shades (0%, 10%, ..., 100%) so a
    /// large torrent - where many real pieces are aggregated into each
    /// on-screen cell, see <see cref="Render"/> - draws through at most
    /// 11 <see cref="StreamGeometry"/> fills (one per shade actually in
    /// use) instead of one draw call per cell.
    /// </summary>
    private const int ShadeBuckets = 10;

    protected override void OnPropertyChanged(AvaloniaPropertyChangedEventArgs change)
    {
        base.OnPropertyChanged(change);
        if (change.Property == PiecesProperty)
        {
            if (change.OldValue is ObservableCollection<bool> oldPieces)
            {
                oldPieces.CollectionChanged -= OnPiecesCollectionChanged;
            }
            if (change.NewValue is ObservableCollection<bool> newPieces)
            {
                newPieces.CollectionChanged += OnPiecesCollectionChanged;
            }
            InvalidateVisual();
        }
        else if (change.Property == PieceOwnersProperty)
        {
            if (change.OldValue is ObservableCollection<string?> oldOwners)
            {
                oldOwners.CollectionChanged -= OnPiecesCollectionChanged;
            }
            if (change.NewValue is ObservableCollection<string?> newOwners)
            {
                newOwners.CollectionChanged += OnPiecesCollectionChanged;
            }
            InvalidateVisual();
        }
    }

    private void OnPiecesCollectionChanged(object? sender, NotifyCollectionChangedEventArgs e) => InvalidateVisual();

    public override void Render(DrawingContext context)
    {
        base.Render(context);
        var pieces = Pieces;
        var totalPieces = pieces?.Count ?? 0;
        if (pieces is null || totalPieces == 0 || Bounds.Width <= 0 || Bounds.Height <= 0)
        {
            return;
        }

        // Bound the number of on-screen cells to what the control's own
        // area can actually show at a legible size, regardless of how
        // many real pieces the torrent has - each cell then aggregates
        // ceil(totalPieces/cellCount) of them, shaded by what fraction
        // are verified. This is the actual fix for the real bug it
        // replaces: the previous version drew one rectangle per piece
        // and simply `break`-ed once it ran past the control's height,
        // so a big torrent in a short pane silently rendered a partial
        // map with no indication anything past that point was missing.
        // Aggregating means the map always represents the whole torrent.
        var maxCols = Math.Max(1, (int)(Bounds.Width / (MinCellSize + Gap)));
        var maxRows = Math.Max(1, (int)(Bounds.Height / (MinCellSize + Gap)));
        var cellCount = Math.Min(totalPieces, maxCols * maxRows);
        var cols = Math.Min(cellCount, maxCols);
        var rows = (int)Math.Ceiling(cellCount / (double)cols);
        var cellWidth = Math.Clamp((Bounds.Width - ((cols - 1) * Gap)) / cols, MinCellSize, MaxCellSize);
        var cellHeight = Math.Min(cellWidth, Math.Max(MinCellSize, (Bounds.Height - ((rows - 1) * Gap)) / Math.Max(rows, 1)));
        var piecesPerCell = (int)Math.Ceiling(totalPieces / (double)cellCount);
        var owners = PieceOwners;

        // Grouped by (dominant owner in this cell, shade bucket) rather than
        // just bucket, same "batch same-coloured cells into one draw call"
        // reasoning as before ShadeBuckets existed for fraction alone - a
        // real swarm only ever has a handful of active peers, so this stays
        // a small number of distinct groups, not one draw call per cell.
        var cellsByGroup = new Dictionary<(string Owner, int Bucket), List<Rect>>();
        var ownerTally = new Dictionary<string, int>();
        for (var i = 0; i < cellCount; i++)
        {
            var start = i * piecesPerCell;
            var end = Math.Min(totalPieces, start + piecesPerCell);
            var have = 0;
            ownerTally.Clear();
            for (var p = start; p < end; p++)
            {
                if (!pieces[p])
                {
                    continue;
                }
                have++;
                var owner = (owners is not null && p < owners.Count ? owners[p] : null) ?? UnknownOwner;
                ownerTally[owner] = ownerTally.GetValueOrDefault(owner) + 1;
            }
            var fraction = end == start ? 0.0 : have / (double)(end - start);
            var bucket = (int)Math.Round(fraction * ShadeBuckets);

            // Dominant = whichever owner contributed the most have pieces to
            // this cell - a pragmatic single colour per cell rather than
            // blending several peers' colours together, which would just
            // look muddy at aggregated (multi-piece-per-cell) zoom levels.
            var dominant = UnknownOwner;
            var dominantCount = 0;
            foreach (var (owner, count) in ownerTally)
            {
                if (count > dominantCount)
                {
                    dominant = owner;
                    dominantCount = count;
                }
            }

            var row = i / cols;
            var col = i % cols;
            var rect = new Rect(col * (cellWidth + Gap), row * (cellHeight + Gap), cellWidth, cellHeight);

            var key = (dominant, bucket);
            if (!cellsByGroup.TryGetValue(key, out var rects))
            {
                rects = [];
                cellsByGroup[key] = rects;
            }
            rects.Add(rect);
        }

        foreach (var (group, rects) in cellsByGroup)
        {
            var geometry = new StreamGeometry();
            using (var geometryContext = geometry.Open())
            {
                foreach (var rect in rects)
                {
                    geometryContext.BeginFigure(rect.TopLeft, isFilled: true);
                    geometryContext.LineTo(rect.TopRight);
                    geometryContext.LineTo(rect.BottomRight);
                    geometryContext.LineTo(rect.BottomLeft);
                    geometryContext.EndFigure(isClosed: true);
                }
            }
            context.DrawGeometry(BrushForOwnerAndBucket(group.Owner, group.Bucket), null, geometry);
        }
    }

    private static IBrush BrushForOwnerAndBucket(string owner, int bucket)
    {
        var fraction = bucket / (double)ShadeBuckets;
        var missing = ThemeResources.Color("GtBorder", Color.FromRgb(64, 64, 64));
        var have = owner == UnknownOwner
            ? ThemeResources.Color("GtSuccess", Color.FromRgb(60, 179, 113))
            : ColorForOwner(owner);
        return new SolidColorBrush(new Color(
            255,
            (byte)(missing.R + ((have.R - missing.R) * fraction)),
            (byte)(missing.G + ((have.G - missing.G) * fraction)),
            (byte)(missing.B + ((have.B - missing.B) * fraction))));
    }

    /// <summary>
    /// A stable colour for a given peer address - same peer always gets the
    /// same colour across redraws (and across sessions, since it's a pure
    /// function of the address string, not an incrementing index that would
    /// shift as peers connect/disconnect in a different order). A simple
    /// string hash picks the hue; fixed saturation/value keep every peer's
    /// colour similarly vivid and legible against both the dark and light
    /// palette's GtBorder background.
    /// </summary>
    private static Color ColorForOwner(string peerAddr)
    {
        unchecked
        {
            var hash = 2166136261u; // FNV-1a
            foreach (var c in peerAddr)
            {
                hash ^= c;
                hash *= 16777619u;
            }
            var hue = hash % 360;
            return HsvToColor(hue, 0.55, 0.85);
        }
    }

    private static Color HsvToColor(double hue, double saturation, double value)
    {
        var c = value * saturation;
        var x = c * (1 - Math.Abs(((hue / 60) % 2) - 1));
        var m = value - c;
        var (r1, g1, b1) = hue switch
        {
            < 60 => (c, x, 0.0),
            < 120 => (x, c, 0.0),
            < 180 => (0.0, c, x),
            < 240 => (0.0, x, c),
            < 300 => (x, 0.0, c),
            _ => (c, 0.0, x),
        };
        return new Color(255, (byte)((r1 + m) * 255), (byte)((g1 + m) * 255), (byte)((b1 + m) * 255));
    }
}

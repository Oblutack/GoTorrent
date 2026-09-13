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

    private const double MinCellSize = 3;
    private const double MaxCellSize = 14;
    private const double Gap = 1;

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
        if (change.Property != PiecesProperty)
        {
            return;
        }
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

        var cellsByBucket = new Dictionary<int, List<Rect>>();
        for (var i = 0; i < cellCount; i++)
        {
            var start = i * piecesPerCell;
            var end = Math.Min(totalPieces, start + piecesPerCell);
            var have = 0;
            for (var p = start; p < end; p++)
            {
                if (pieces[p])
                {
                    have++;
                }
            }
            var fraction = end == start ? 0.0 : have / (double)(end - start);
            var bucket = (int)Math.Round(fraction * ShadeBuckets);

            var row = i / cols;
            var col = i % cols;
            var rect = new Rect(col * (cellWidth + Gap), row * (cellHeight + Gap), cellWidth, cellHeight);

            if (!cellsByBucket.TryGetValue(bucket, out var rects))
            {
                rects = [];
                cellsByBucket[bucket] = rects;
            }
            rects.Add(rect);
        }

        foreach (var (bucket, rects) in cellsByBucket)
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
            context.DrawGeometry(BrushForBucket(bucket), null, geometry);
        }
    }

    private static IBrush BrushForBucket(int bucket)
    {
        var fraction = bucket / (double)ShadeBuckets;
        var missing = ThemeResources.Color("GtBorder", Color.FromRgb(64, 64, 64));
        var have = ThemeResources.Color("GtSuccess", Color.FromRgb(60, 179, 113));
        return new SolidColorBrush(new Color(
            255,
            (byte)(missing.R + ((have.R - missing.R) * fraction)),
            (byte)(missing.G + ((have.G - missing.G) * fraction)),
            (byte)(missing.B + ((have.B - missing.B) * fraction))));
    }
}

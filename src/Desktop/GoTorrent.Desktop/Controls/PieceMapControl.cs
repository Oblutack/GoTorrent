using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Media;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// Renders <see cref="Pieces"/> (one bool per piece - have/missing) as a
/// grid of coloured cells, hand-drawn rather than one <c>Control</c> per
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

    private static readonly IBrush HaveBrush = Brushes.MediumSeaGreen;
    private static readonly IBrush MissingBrush = new SolidColorBrush(Color.FromRgb(64, 64, 64));

    private const double MinCellSize = 3;
    private const double MaxCellSize = 14;
    private const double Gap = 1;

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
        if (pieces is null || pieces.Count == 0 || Bounds.Width <= 0 || Bounds.Height <= 0)
        {
            return;
        }

        var count = pieces.Count;
        var cols = Math.Max(1, Math.Min(count, (int)(Bounds.Width / (MinCellSize + Gap))));
        var rows = (int)Math.Ceiling(count / (double)cols);
        var cellWidth = Math.Clamp((Bounds.Width - ((cols - 1) * Gap)) / cols, MinCellSize, MaxCellSize);
        var cellHeight = Math.Min(cellWidth, Math.Max(MinCellSize, (Bounds.Height - ((rows - 1) * Gap)) / Math.Max(rows, 1)));

        for (var i = 0; i < count; i++)
        {
            var row = i / cols;
            var y = row * (cellHeight + Gap);
            if (y > Bounds.Height)
            {
                break;
            }
            var col = i % cols;
            var x = col * (cellWidth + Gap);
            context.FillRectangle(pieces[i] ? HaveBrush : MissingBrush, new Rect(x, y, cellWidth, cellHeight));
        }
    }
}

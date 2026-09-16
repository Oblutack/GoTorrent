using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Media;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// A compact, chrome-free dual-line chart - Stage 6's "per-torrent speed
/// sparkline," fed by <see cref="ViewModels.TorrentRowViewModel.DownloadRateHistory"/>/
/// <c>UploadRateHistory</c>. Deliberately not <see cref="SpeedGraphControl"/>
/// reused at a small size: a sparkline has no gridlines, no axis label, and
/// no hover readout by definition - at the handful of pixels a detail-pane
/// row actually gives it, that chrome wouldn't be legible anyway, and a
/// bare inline trend line is the entire point. Shares
/// <see cref="SpeedGraphControl"/>'s core technique (a fixed-window scale
/// so the line scrolls rather than rescaling as new samples arrive - see
/// that control's own doc comment for the real bug this avoids) but
/// nothing else.
/// </summary>
public sealed class SparklineControl : Control
{
    public static readonly StyledProperty<ObservableCollection<double>?> DownloadSamplesProperty =
        AvaloniaProperty.Register<SparklineControl, ObservableCollection<double>?>(nameof(DownloadSamples));

    public static readonly StyledProperty<ObservableCollection<double>?> UploadSamplesProperty =
        AvaloniaProperty.Register<SparklineControl, ObservableCollection<double>?>(nameof(UploadSamples));

    /// <summary>Matches <see cref="ViewModels.TorrentRowViewModel"/>'s own rolling-history cap - see <see cref="SpeedGraphControl.WindowSize"/>'s identical reasoning for why this is a property, not a hardcoded constant.</summary>
    public static readonly StyledProperty<int> WindowSizeProperty =
        AvaloniaProperty.Register<SparklineControl, int>(nameof(WindowSize), 40);

    public ObservableCollection<double>? DownloadSamples
    {
        get => GetValue(DownloadSamplesProperty);
        set => SetValue(DownloadSamplesProperty, value);
    }

    public ObservableCollection<double>? UploadSamples
    {
        get => GetValue(UploadSamplesProperty);
        set => SetValue(UploadSamplesProperty, value);
    }

    public int WindowSize
    {
        get => GetValue(WindowSizeProperty);
        set => SetValue(WindowSizeProperty, value);
    }

    protected override void OnPropertyChanged(AvaloniaPropertyChangedEventArgs change)
    {
        base.OnPropertyChanged(change);
        if (change.Property == WindowSizeProperty)
        {
            InvalidateVisual();
            return;
        }
        if (change.Property != DownloadSamplesProperty && change.Property != UploadSamplesProperty)
        {
            return;
        }
        if (change.OldValue is ObservableCollection<double> oldSamples)
        {
            oldSamples.CollectionChanged -= OnSamplesCollectionChanged;
        }
        if (change.NewValue is ObservableCollection<double> newSamples)
        {
            newSamples.CollectionChanged += OnSamplesCollectionChanged;
        }
        InvalidateVisual();
    }

    private void OnSamplesCollectionChanged(object? sender, NotifyCollectionChangedEventArgs e) => InvalidateVisual();

    public override void Render(DrawingContext context)
    {
        base.Render(context);
        var down = DownloadSamples;
        var up = UploadSamples;
        if (Bounds.Width <= 0 || Bounds.Height <= 0)
        {
            return;
        }

        var maxValue = Math.Max(1.0, Math.Max(Max(down), Max(up)));
        var window = Math.Max(WindowSize, Math.Max(down?.Count ?? 0, up?.Count ?? 0));

        DrawSeries(context, down, DownloadPen, maxValue, window);
        DrawSeries(context, up, UploadPen, maxValue, window);
    }

    private void DrawSeries(DrawingContext context, ObservableCollection<double>? samples, IPen linePen, double maxValue, int window)
    {
        if (samples is null || samples.Count < 2)
        {
            return;
        }

        var stepX = Bounds.Width / Math.Max(1, window - 1);
        var offsetX = (window - samples.Count) * stepX;
        // A little top/bottom padding so a peak sample doesn't clip
        // against the control's own edge.
        const double verticalPadding = 2;
        var drawableHeight = Math.Max(0, Bounds.Height - (2 * verticalPadding));

        Point PointAt(int i) => new(offsetX + (i * stepX), Bounds.Height - verticalPadding - (samples[i] / maxValue * drawableHeight));

        for (var i = 1; i < samples.Count; i++)
        {
            context.DrawLine(linePen, PointAt(i - 1), PointAt(i));
        }
    }

    private static double Max(ObservableCollection<double>? samples)
    {
        var max = 0.0;
        if (samples is null)
        {
            return max;
        }
        foreach (var sample in samples)
        {
            if (sample > max)
            {
                max = sample;
            }
        }
        return max;
    }

    private static IBrush DownloadLineBrush => ThemeResources.Brush("GtDownload", Brushes.DodgerBlue);
    private static IBrush UploadLineBrush => ThemeResources.Brush("GtUpload", Brushes.OrangeRed);

    private static Pen DownloadPen => new((ISolidColorBrush)DownloadLineBrush, 1.25);
    private static Pen UploadPen => new((ISolidColorBrush)UploadLineBrush, 1.25);
}

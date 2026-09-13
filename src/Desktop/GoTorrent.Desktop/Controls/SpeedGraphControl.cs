using System.Collections.ObjectModel;
using System.Collections.Specialized;
using System.Globalization;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Media;

namespace GoTorrent.Desktop.Controls;

/// <summary>
/// Draws two live polylines (download/upload, KiB/s) from rolling sample
/// histories - hand-drawn for the same reason as
/// <see cref="PieceMapControl"/> rather than pulling in a charting
/// package for two lines. Fed by <c>MainViewModel.DownloadRateHistory</c>/
/// <c>UploadRateHistory</c>, which are themselves computed from
/// gottrentd's real <c>sessionStats</c> WS messages (a running total,
/// not a rate - see <c>MainViewModel.RecordSpeedSample</c>).
/// </summary>
public sealed class SpeedGraphControl : Control
{
    public static readonly StyledProperty<ObservableCollection<double>?> DownloadSamplesProperty =
        AvaloniaProperty.Register<SpeedGraphControl, ObservableCollection<double>?>(nameof(DownloadSamples));

    public static readonly StyledProperty<ObservableCollection<double>?> UploadSamplesProperty =
        AvaloniaProperty.Register<SpeedGraphControl, ObservableCollection<double>?>(nameof(UploadSamples));

    /// <summary>
    /// How many samples a full window represents - matches
    /// <c>MainViewModel.MaxSpeedSamples</c>, the rolling-history cap the
    /// bound collections are already kept at. Exposed as a property
    /// (rather than a hardcoded constant in this control) purely so a
    /// future caller/test could override it; <c>MainWindow.axaml</c>
    /// doesn't set it today, relying on the default matching the
    /// ViewModel's own cap.
    /// </summary>
    public static readonly StyledProperty<int> WindowSizeProperty =
        AvaloniaProperty.Register<SpeedGraphControl, int>(nameof(WindowSize), 300);

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

    private const int GridlineCount = 4;

    private int? _hoverIndex;

    public SpeedGraphControl()
    {
        PointerMoved += OnPointerMoved;
        PointerExited += OnPointerExited;
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

    /// <summary>
    /// Maps a pointer X back to a sample index, accounting for the same
    /// left-side empty gap <see cref="DrawSeries"/> draws before the
    /// window has filled - without that offset, hovering early in a
    /// session (few samples so far) would read out the wrong sample.
    /// </summary>
    private void OnPointerMoved(object? sender, PointerEventArgs e)
    {
        var count = Math.Max(DownloadSamples?.Count ?? 0, UploadSamples?.Count ?? 0);
        if (count < 2 || Bounds.Width <= 0)
        {
            SetHoverIndex(null);
            return;
        }
        var window = Math.Max(WindowSize, count);
        var stepX = Bounds.Width / Math.Max(1, window - 1);
        var offsetX = (window - count) * stepX;
        var index = (int)Math.Round((e.GetPosition(this).X - offsetX) / stepX);
        SetHoverIndex(index >= 0 && index < count ? index : null);
    }

    private void OnPointerExited(object? sender, PointerEventArgs e) => SetHoverIndex(null);

    private void SetHoverIndex(int? index)
    {
        if (_hoverIndex == index)
        {
            return;
        }
        _hoverIndex = index;
        InvalidateVisual();
    }

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

        DrawGridlines(context, maxValue);
        DrawSeries(context, down, DownloadPen, DownloadFillBrush, maxValue, window);
        DrawSeries(context, up, UploadPen, UploadFillBrush, maxValue, window);

        if (_hoverIndex is { } index)
        {
            DrawHoverReadout(context, down, up, maxValue, window, index);
        }
    }

    /// <summary>
    /// Faint horizontal lines plus a y-axis max label - without either, a
    /// peak could be 10 KiB/s or 10 MiB/s and the chart looks identical.
    /// </summary>
    private void DrawGridlines(DrawingContext context, double maxValue)
    {
        var gridPen = new Pen(GridBrush, 1);
        for (var i = 1; i < GridlineCount; i++)
        {
            var y = Bounds.Height * i / GridlineCount;
            context.DrawLine(gridPen, new Point(0, y), new Point(Bounds.Width, y));
        }

        var label = new FormattedText(
            FormatRate(maxValue),
            CultureInfo.InvariantCulture,
            FlowDirection.LeftToRight,
            Typeface.Default,
            11,
            LabelBrush);
        context.DrawText(label, new Point(4, 2));
    }

    /// <summary>
    /// Draws against a <b>fixed</b> <paramref name="window"/> of sample
    /// slots, not <c>samples.Count</c> - the real bug this replaces: the
    /// old version set <c>stepX = Width / (Count - 1)</c>, so the line
    /// visibly rescaled wider every time a new sample arrived instead of
    /// staying at one scale and scrolling. Here, until the rolling
    /// history actually reaches <see cref="WindowSize"/>, the series
    /// simply starts partway across the control (an empty gap on the
    /// left) and grows rightward at a constant scale; once full, new
    /// points really do scroll in from the right as old ones drop off
    /// (<c>MainViewModel.AppendSample</c> already trims from the front).
    /// </summary>
    private void DrawSeries(DrawingContext context, ObservableCollection<double>? samples, IPen linePen, IBrush fillBrush, double maxValue, int window)
    {
        if (samples is null || samples.Count < 2)
        {
            return;
        }

        var stepX = Bounds.Width / Math.Max(1, window - 1);
        var offsetX = (window - samples.Count) * stepX;
        var points = new Point[samples.Count];
        for (var i = 0; i < samples.Count; i++)
        {
            points[i] = new Point(offsetX + (i * stepX), Bounds.Height - (samples[i] / maxValue * Bounds.Height));
        }

        var fill = new StreamGeometry();
        using (var geometryContext = fill.Open())
        {
            geometryContext.BeginFigure(new Point(points[0].X, Bounds.Height), isFilled: true);
            foreach (var point in points)
            {
                geometryContext.LineTo(point);
            }
            geometryContext.LineTo(new Point(points[^1].X, Bounds.Height));
            geometryContext.EndFigure(isClosed: true);
        }
        context.DrawGeometry(fillBrush, null, fill);

        for (var i = 1; i < points.Length; i++)
        {
            context.DrawLine(linePen, points[i - 1], points[i]);
        }
    }

    private void DrawHoverReadout(DrawingContext context, ObservableCollection<double>? down, ObservableCollection<double>? up, double maxValue, int window, int index)
    {
        var stepX = Bounds.Width / Math.Max(1, window - 1);
        var count = Math.Max(down?.Count ?? 0, up?.Count ?? 0);
        var offsetX = (window - count) * stepX;
        var x = offsetX + (index * stepX);
        if (x < 0 || x > Bounds.Width)
        {
            return;
        }

        context.DrawLine(new Pen(GridBrush, 1), new Point(x, 0), new Point(x, Bounds.Height));

        var downValue = down is not null && index < down.Count ? down[index] : 0;
        var upValue = up is not null && index < up.Count ? up[index] : 0;
        var text = $"↓ {FormatRate(downValue)}   ↑ {FormatRate(upValue)}";
        var formatted = new FormattedText(text, CultureInfo.InvariantCulture, FlowDirection.LeftToRight, Typeface.Default, 11, LabelBrush);

        var labelX = Math.Clamp(x - (formatted.Width / 2), 0, Math.Max(0, Bounds.Width - formatted.Width));
        context.FillRectangle(TooltipBackground, new Rect(labelX - 4, Bounds.Height - 18, formatted.Width + 8, 16));
        context.DrawText(formatted, new Point(labelX, Bounds.Height - 16));
    }

    private static string FormatRate(double kbps) =>
        kbps >= 1024 ? $"{kbps / 1024:0.0} MiB/s" : $"{kbps:0} KiB/s";

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
    private static IBrush GridBrush => ThemeResources.Brush("GtBorder", Brushes.DimGray);
    private static IBrush LabelBrush => ThemeResources.Brush("GtSubtle", Brushes.Gray);
    private static IBrush TooltipBackground => ThemeResources.Brush("GtSurfaceHover", Brushes.Black);

    private static Pen DownloadPen => new((ISolidColorBrush)DownloadLineBrush, 1.5);
    private static Pen UploadPen => new((ISolidColorBrush)UploadLineBrush, 1.5);

    private static IBrush DownloadFillBrush => new SolidColorBrush(((ISolidColorBrush)DownloadLineBrush).Color, 0.15);
    private static IBrush UploadFillBrush => new SolidColorBrush(((ISolidColorBrush)UploadLineBrush).Color, 0.15);
}

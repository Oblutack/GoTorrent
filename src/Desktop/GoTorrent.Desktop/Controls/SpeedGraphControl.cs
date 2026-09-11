using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Avalonia;
using Avalonia.Controls;
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

    private static readonly IPen DownloadPen = new Pen(Brushes.DodgerBlue, 1.5);
    private static readonly IPen UploadPen = new Pen(Brushes.OrangeRed, 1.5);

    protected override void OnPropertyChanged(AvaloniaPropertyChangedEventArgs change)
    {
        base.OnPropertyChanged(change);
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
        DrawLine(context, down, DownloadPen, maxValue);
        DrawLine(context, up, UploadPen, maxValue);
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

    private void DrawLine(DrawingContext context, ObservableCollection<double>? samples, IPen pen, double maxValue)
    {
        if (samples is null || samples.Count < 2)
        {
            return;
        }

        var stepX = Bounds.Width / (samples.Count - 1);
        Point? previous = null;
        for (var i = 0; i < samples.Count; i++)
        {
            var point = new Point(i * stepX, Bounds.Height - (samples[i] / maxValue * Bounds.Height));
            if (previous is { } start)
            {
                context.DrawLine(pen, start, point);
            }
            previous = point;
        }
    }
}

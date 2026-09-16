using System;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Threading;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A read-only live display, not a form - binds its <see cref="DataContext"/>
/// straight to <see cref="MainViewModel"/> (the same shape <c>MainWindow</c>'s
/// own detail tabs already use for live data), unlike the one-shot form
/// dialogs elsewhere in this app that deliberately have no ViewModel of
/// their own. The one line a plain binding can't drive - "Connected for",
/// an elapsed duration that has to keep advancing as wall-clock time
/// passes rather than only when <see cref="MainViewModel.Session"/>
/// changes - is ticked here in code-behind instead.
/// </summary>
public partial class StatisticsWindow : Window
{
    private readonly MainViewModel _mainViewModel;
    private readonly DispatcherTimer _elapsedTimer;

    public StatisticsWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
        _elapsedTimer = new DispatcherTimer();
    }

    public StatisticsWindow(MainViewModel mainViewModel)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        DataContext = mainViewModel;

        UpdateConnectedForText();
        _elapsedTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
        _elapsedTimer.Tick += (_, _) => UpdateConnectedForText();
        _elapsedTimer.Start();
        Closed += (_, _) => _elapsedTimer.Stop();
    }

    private void UpdateConnectedForText()
    {
        if (_mainViewModel.ConnectedSince is not { } connectedSince)
        {
            ConnectedForText.Text = string.Empty;
            return;
        }
        var elapsed = DateTimeOffset.UtcNow - connectedSince;
        if (elapsed < TimeSpan.Zero)
        {
            elapsed = TimeSpan.Zero;
        }
        ConnectedForText.Text = $"{(int)elapsed.TotalHours:D2}:{elapsed.Minutes:D2}:{elapsed.Seconds:D2}";
    }

    private void OnCloseClick(object? sender, RoutedEventArgs e) => Close();
}

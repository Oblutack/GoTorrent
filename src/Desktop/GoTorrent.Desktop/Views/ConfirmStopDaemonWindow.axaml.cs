using Avalonia.Controls;
using Avalonia.Interactivity;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// Daemon supervision's "offer to keep it running on exit" - shown by
/// <c>App.axaml.cs</c>'s tray "Exit" handler only when
/// <see cref="ViewModels.MainViewModel.WeOwnRunningDaemon"/> is true. No
/// ViewModel of its own, same reasoning as <see cref="AddTorrentWindow"/>/
/// <see cref="PreferencesWindow"/> - this is a plain yes/no prompt with
/// nothing worth unit-testing. Shown via <see cref="Window.Show()"/>
/// rather than <see cref="Window.ShowDialog(Window)"/>: the main window
/// may well be hidden (minimized to tray) at exit time, and an owned
/// dialog's owner does not need to be visible for a real user, but
/// keeping this one owner-independent avoids relying on that.
/// </summary>
public partial class ConfirmStopDaemonWindow : Window
{
    public bool ShouldStopDaemon { get; private set; }

    public ConfirmStopDaemonWindow()
    {
        InitializeComponent();
    }

    private void OnStopClick(object? sender, RoutedEventArgs e)
    {
        ShouldStopDaemon = true;
        Close();
    }

    private void OnLeaveRunningClick(object? sender, RoutedEventArgs e)
    {
        ShouldStopDaemon = false;
        Close();
    }
}

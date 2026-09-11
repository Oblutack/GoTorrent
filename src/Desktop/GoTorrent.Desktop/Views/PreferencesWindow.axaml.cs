using System;
using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// No ViewModel of its own, same reasoning as <see cref="AddTorrentWindow"/>
/// - this is a thin form over <see cref="MainViewModel.GetSessionLimitsAsync"/>/
/// <see cref="MainViewModel.SetSessionLimitsAsync"/>, which are what's
/// actually unit-tested.
/// </summary>
public partial class PreferencesWindow : Window
{
    private readonly MainViewModel _mainViewModel;

    public PreferencesWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
    }

    public PreferencesWindow(MainViewModel mainViewModel)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        Opened += async (_, _) => await LoadCurrentLimitsAsync();
    }

    private async System.Threading.Tasks.Task LoadCurrentLimitsAsync()
    {
        try
        {
            var limits = await _mainViewModel.GetSessionLimitsAsync();
            DownLimitBox.Text = limits.DownLimitKB.ToString();
            UpLimitBox.Text = limits.UpLimitKB.ToString();
        }
        catch (Exception ex)
        {
            ShowError(ex.Message);
        }
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnSaveClick(object? sender, RoutedEventArgs e)
    {
        if (!long.TryParse(DownLimitBox.Text, out var down) || down < 0)
        {
            ShowError("Download limit must be a non-negative number.");
            return;
        }
        if (!long.TryParse(UpLimitBox.Text, out var up) || up < 0)
        {
            ShowError("Upload limit must be a non-negative number.");
            return;
        }

        SaveButton.IsEnabled = false;
        try
        {
            await _mainViewModel.SetSessionLimitsAsync(down, up);
            Close();
        }
        catch (Exception ex)
        {
            ShowError(ex.Message);
        }
        finally
        {
            SaveButton.IsEnabled = true;
        }
    }

    private void ShowError(string message)
    {
        ErrorText.Text = message;
        ErrorText.IsVisible = true;
    }
}

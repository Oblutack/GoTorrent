using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Threading;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// One entry in the command palette: <see cref="Category"/> groups it in
/// the list ("Jump to torrent" for every real torrent, "Action" for
/// something Ctrl+K can run right now), <see cref="Label"/> is the
/// searchable text, and <see cref="Invoke"/> is what running it actually
/// does - a plain delegate rather than an <c>ICommand</c>, since most
/// entries here (jump-to-torrent, open a dialog) aren't backed by one at
/// all, and the ones that are (Pause/Resume/...) are just as easy to wrap
/// in a closure as to re-expose through a second layer of indirection.
/// </summary>
public sealed record CommandPaletteEntry(string Category, string Label, Action Invoke);

/// <summary>
/// A generic fuzzy-ish (plain case-insensitive substring, same matching
/// the main torrent search box already uses - no new algorithm invented
/// for this one dialog) jump list, following the same no-ViewModel
/// code-behind pattern as every other one-shot dialog in this app
/// (<c>AddTorrentWindow</c>, <c>SetCategoryWindow</c>, ...): the entries
/// themselves - and what running one of them actually does - are built
/// entirely by <c>MainWindow</c> before this window is even constructed,
/// so this class knows nothing about torrents, gottrentd, or the rest of
/// the app. That keeps it trivially reusable for a future palette
/// category without this file changing at all.
/// </summary>
public partial class CommandPaletteWindow : Window
{
    private readonly IReadOnlyList<CommandPaletteEntry> _allEntries;
    private readonly ObservableCollection<CommandPaletteEntry> _filtered = [];

    public CommandPaletteWindow()
    {
        InitializeComponent();
        _allEntries = [];
    }

    public CommandPaletteWindow(IReadOnlyList<CommandPaletteEntry> entries)
    {
        InitializeComponent();
        _allEntries = entries;
        ResultsList.ItemsSource = _filtered;
        Refilter(string.Empty);

        QueryBox.TextChanged += (_, _) => Refilter(QueryBox.Text ?? string.Empty);
        Opened += (_, _) => Dispatcher.UIThread.Post(() => QueryBox.Focus());
    }

    private void Refilter(string query)
    {
        var previouslySelected = ResultsList.SelectedItem as CommandPaletteEntry;
        _filtered.Clear();
        var matches = query.Trim().Length == 0
            ? _allEntries
            : _allEntries.Where(e =>
                e.Label.Contains(query, StringComparison.OrdinalIgnoreCase) ||
                e.Category.Contains(query, StringComparison.OrdinalIgnoreCase));
        foreach (var entry in matches)
        {
            _filtered.Add(entry);
        }
        // Re-select the same entry if it's still in the filtered set (the
        // user narrowing an already-typed query shouldn't lose their
        // place), otherwise default to the first result so Enter always
        // does something without an extra arrow-key press.
        ResultsList.SelectedItem = previouslySelected is not null && _filtered.Contains(previouslySelected)
            ? previouslySelected
            : _filtered.FirstOrDefault();
    }

    private void OnQueryBoxKeyDown(object? sender, KeyEventArgs e)
    {
        switch (e.Key)
        {
            case Key.Down:
                MoveSelection(1);
                e.Handled = true;
                break;
            case Key.Up:
                MoveSelection(-1);
                e.Handled = true;
                break;
            case Key.Enter:
                InvokeSelected();
                e.Handled = true;
                break;
            case Key.Escape:
                Close();
                e.Handled = true;
                break;
        }
    }

    private void MoveSelection(int delta)
    {
        if (_filtered.Count == 0)
        {
            return;
        }
        var index = ResultsList.SelectedIndex;
        index = Math.Clamp(index < 0 ? 0 : index + delta, 0, _filtered.Count - 1);
        ResultsList.SelectedIndex = index;
        ResultsList.ScrollIntoView(ResultsList.SelectedItem!);
    }

    private void InvokeSelected()
    {
        if (ResultsList.SelectedItem is not CommandPaletteEntry entry)
        {
            return;
        }
        Close();
        entry.Invoke();
    }

    private void OnResultsListDoubleTapped(object? sender, TappedEventArgs e) => InvokeSelected();

    private void OnCloseClick(object? sender, RoutedEventArgs e) => Close();
}

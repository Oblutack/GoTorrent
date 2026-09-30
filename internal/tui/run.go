package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the terminal UI program and blocks until it exits (the user
// quits, or ctx is cancelled). addr/token pre-fill the connect screen -
// see New's own doc comment for why they don't auto-connect.
//
// themeName picks the starting theme; empty means the one remembered from
// the last run (or the first theme if there was none). ctrl+t cycles themes
// on any screen, and the choice is remembered.
func Run(ctx context.Context, addr, token, themeName string) error {
	m := New(ctx, addr, token)
	if themeName == "" {
		themeName = SavedTheme()
	}
	if i, ok := themeIndex(themeName); ok {
		m.themeIdx = i
		applyTheme(themes[i])
		m.restyleInputs()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

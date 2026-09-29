package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the terminal UI program and blocks until it exits (the user
// quits, or ctx is cancelled). addr/token pre-fill the connect screen -
// see New's own doc comment for why they don't auto-connect.
func Run(ctx context.Context, addr, token string) error {
	m := New(ctx, addr, token)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/miku0139/linode-agent/internal/ops"
)

// Run starts the interactive TUI and blocks until the user quits.
// allowWrite enables the b/s/R/x mutating keys (each still asks for y/n).
func Run(svc *ops.Service, profile string, allowWrite bool) error {
	p := tea.NewProgram(NewWithWrite(svc, profile, allowWrite), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

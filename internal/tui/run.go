package tui

import (
	"github.com/Miku0139oao/linode-agent/internal/ops"
	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the interactive TUI and blocks until the user quits.
// allowWrite enables the b/s/R/x mutating keys (each still asks for y/n).
func Run(svc *ops.Service, profile string, allowWrite bool) error {
	p := tea.NewProgram(NewWithWrite(svc, profile, allowWrite), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

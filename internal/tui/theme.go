package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme centralises every colour so a future --theme flag only touches this file.
type Theme struct {
	Accent lipgloss.AdaptiveColor
	Dim    lipgloss.AdaptiveColor
	Green  lipgloss.AdaptiveColor
	Gray   lipgloss.AdaptiveColor
	Red    lipgloss.AdaptiveColor
	Yellow lipgloss.AdaptiveColor

	Header    lipgloss.Style
	TabActive lipgloss.Style
	TabIdle   lipgloss.Style
	Status    lipgloss.Style
	Help      lipgloss.Style
	DetailBox lipgloss.Style
	HelpBox   lipgloss.Style
	Spinner   lipgloss.Style
	Footer    lipgloss.Style
}

// DefaultTheme is a calm, high-contrast palette that works on light and dark terminals.
func DefaultTheme() Theme {
	t := Theme{
		Accent: lipgloss.AdaptiveColor{Light: "#5A31F4", Dark: "#A78BFA"},
		Dim:    lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"},
		Green:  lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"},
		Gray:   lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"},
		Red:    lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"},
		Yellow: lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FBBF24"},
	}
	t.Header = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F9FAFB"}).
		Background(t.Accent).
		Padding(0, 1)
	t.TabActive = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0B0B0F"}).
		Background(t.Accent).
		Padding(0, 1)
	t.TabIdle = lipgloss.NewStyle().Foreground(t.Dim).Padding(0, 1)
	t.Status = lipgloss.NewStyle().Foreground(t.Dim)
	t.Footer = lipgloss.NewStyle().Foreground(t.Dim)
	t.Help = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#E5E7EB"})
	t.Spinner = lipgloss.NewStyle().Foreground(t.Accent)
	t.DetailBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent).
		Padding(0, 1)
	t.HelpBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Dim).
		Padding(1, 2)
	return t
}

// statusDot renders a colour+symbol pair so state is legible without colour.
func (t Theme) statusDot(status string) string {
	switch status {
	case "running", "active", "available", "finished":
		return lipgloss.NewStyle().Foreground(t.Green).Render("●")
	case "offline", "stopped", "inactive":
		return lipgloss.NewStyle().Foreground(t.Gray).Render("○")
	case "provisioning", "creating", "booting", "rebooting", "pending":
		return lipgloss.NewStyle().Foreground(t.Yellow).Render("◐")
	case "failed", "error":
		return lipgloss.NewStyle().Foreground(t.Red).Render("✕")
	default:
		return lipgloss.NewStyle().Foreground(t.Gray).Render("·")
	}
}
